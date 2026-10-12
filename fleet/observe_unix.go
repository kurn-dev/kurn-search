//go:build unix

package fleet

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/kurn-dev/kurn-search/backend/kurnhttp"
)

const maxProcessOutput = 64 << 10

type liveChild struct {
	command *exec.Cmd
	done    chan error
	waited  bool
	waitErr error
}

func (child *liveChild) pollExited() bool {
	if child == nil || child.command == nil || child.command.Process == nil {
		return true
	}
	if child.waited {
		return true
	}
	select {
	case child.waitErr = <-child.done:
		child.waited = true
		return true
	default:
		return false
	}
}

func (child *liveChild) stop() error {
	if child == nil || child.command == nil || child.command.Process == nil {
		return nil
	}
	if child.pollExited() {
		return errRefused
	}
	if syscall.Kill(-child.command.Process.Pid, syscall.SIGTERM) != nil {
		return errRefused
	}
	timer := time.NewTimer(12 * time.Second)
	defer timer.Stop()
	select {
	case child.waitErr = <-child.done:
		child.waited = true
		if !cleanTermination(child.waitErr, child.command.ProcessState) {
			return errRefused
		}
		return nil
	case <-timer.C:
		_ = syscall.Kill(-child.command.Process.Pid, syscall.SIGKILL)
		if !child.waited {
			child.waitErr = <-child.done
			child.waited = true
		}
		return errRefused
	}
}

func cleanTermination(waitErr error, state *os.ProcessState) bool {
	if state == nil {
		return false
	}
	if waitErr == nil && state.ExitCode() == 0 {
		return true
	}
	status, ok := state.Sys().(syscall.WaitStatus)
	return ok && status.Signaled() && status.Signal() == syscall.SIGTERM
}

type cappedBuffer struct {
	buffer   bytes.Buffer
	overflow bool
}

func (writer *cappedBuffer) Write(raw []byte) (int, error) {
	written := len(raw)
	remaining := maxProcessOutput - writer.buffer.Len()
	if remaining <= 0 {
		writer.overflow = true
		return written, nil
	}
	if len(raw) > remaining {
		writer.overflow = true
		raw = raw[:remaining]
	}
	_, _ = writer.buffer.Write(raw)
	return written, nil
}

type liveNode struct {
	kurnd       *liveChild
	searchd     *liveChild
	kurndScan   <-chan error
	searchdErr  *cappedBuffer
	observeRoot string
}

func observeFleet(plan Plan) (result observedFleet, err error) {
	if validatePlan(plan) != nil {
		return observedFleet{}, fmt.Errorf("%w: plan preflight", errRefused)
	}
	baseContext, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)
	defer stopSignals()
	ctx, cancel := context.WithTimeout(baseContext, time.Duration(plan.ProbeDeadlineMillis)*time.Millisecond)
	defer cancel()
	nodes := make([]liveNode, 0, len(plan.Nodes))
	defer func() {
		cleanupOK := true
		for index := len(nodes) - 1; index >= 0; index-- {
			if nodes[index].searchd != nil && nodes[index].searchd.stop() != nil {
				cleanupOK = false
			}
			if nodes[index].kurnd != nil && nodes[index].kurnd.stop() != nil {
				cleanupOK = false
			}
			if nodes[index].kurndScan != nil {
				if scanErr := <-nodes[index].kurndScan; scanErr != nil {
					cleanupOK = false
				}
			}
			if nodes[index].searchdErr != nil && (nodes[index].searchdErr.overflow || nodes[index].searchdErr.buffer.Len() != 0) {
				cleanupOK = false
			}
			if nodes[index].observeRoot != "" && os.RemoveAll(nodes[index].observeRoot) != nil {
				cleanupOK = false
			}
		}
		if !cleanupOK && err == nil {
			result = observedFleet{}
			err = errRefused
		}
	}()

	readyNodes := 0
	goldenNodes := 0
	stamps := make([]servedStampIdentity, 0, len(plan.Nodes))
	for _, node := range plan.Nodes {
		if ctx.Err() != nil || recheckExecutable(plan.Executables.Kurnd, KurndSHA256) != nil ||
			recheckExecutable(plan.Executables.KurnSearchd, KurnSearchdSHA256) != nil {
			return observedFleet{}, fmt.Errorf("%w: executable preflight", errRefused)
		}
		credential, readErr := readRegular(node.BackendCredentialFile, 64<<10, modePointer(0o600))
		if readErr != nil || validateCredential(node.BackendCredentialFile) != nil {
			return observedFleet{}, fmt.Errorf("%w: credential", errRefused)
		}
		processRoot := filepath.Dir(filepath.Dir(node.Root))
		if resolved, resolveErr := resolvedPrivateDirectory(processRoot); resolveErr != nil || resolved != processRoot {
			return observedFleet{}, fmt.Errorf("%w: process root", errRefused)
		}
		observeRoot := filepath.Join(processRoot, ".s4-observe")
		if _, statErr := os.Lstat(observeRoot); !errors.Is(statErr, os.ErrNotExist) || os.Mkdir(observeRoot, 0o700) != nil {
			return observedFleet{}, fmt.Errorf("%w: observation root", errRefused)
		}
		live := liveNode{observeRoot: observeRoot}
		nodes = append(nodes, live)
		current := &nodes[len(nodes)-1]
		kurnd, baseURL, scanDone, startErr := startKurnd(ctx, plan.Executables.Kurnd.Path, filepath.Dir(node.Root), node.BackendCredentialFile)
		current.kurnd, current.kurndScan = kurnd, scanDone
		if startErr != nil {
			return observedFleet{}, fmt.Errorf("%w: kurnd start: %v", errRefused, startErr)
		}
		raw, requestErr := getSignal(ctx, baseURL+"/v1/lists/"+PhysicalListName, string(credential), true)
		if requestErr != nil {
			return observedFleet{}, fmt.Errorf("%w: list signal", errRefused)
		}
		signalValue, parseErr := ParseManagementSignal(raw)
		stamp, stampErr := kurnhttp.ParseStamp(signalValue.Version)
		if parseErr != nil || stampErr != nil || signalValue.Entries != int(plan.Bundle.Entries) ||
			stamp.BaseSHA256 != plan.Bundle.BaseSHA256 || stamp.ConfigSHA256 != plan.Bundle.ConfigSHA256 ||
			stamp.Entries != plan.Bundle.Entries || stamp.JournalBytes != 0 || stamp.Raw != expectedStoreStamp(plan.Bundle) {
			return observedFleet{}, fmt.Errorf("%w: full stamp", errRefused)
		}
		stamps = append(stamps, servedStampIdentity{NodeID: node.ID, Stamp: stamp.Raw})
		searchd, statusAddress, stderr, startErr := startSearchd(ctx, plan.Executables.KurnSearchd.Path, observeRoot, baseURL, credential, plan)
		current.searchd, current.searchdErr = searchd, stderr
		if startErr != nil {
			return observedFleet{}, fmt.Errorf("%w: searchd start", errRefused)
		}
		if _, requestErr := getSignal(ctx, baseURL+"/readyz", "", false); requestErr != nil {
			return observedFleet{}, fmt.Errorf("%w: readiness", errRefused)
		}
		readyNodes++
		if goldenProbe(ctx, "http://"+statusAddress, stamp.Raw) != nil {
			return observedFleet{}, fmt.Errorf("%w: golden", errRefused)
		}
		goldenNodes++
	}
	alivePairs := 0
	for index := range nodes {
		if !nodes[index].kurnd.pollExited() && !nodes[index].searchd.pollExited() {
			alivePairs++
		}
	}
	metrics := make([]MetricValue, len(plan.Metrics))
	for index, rule := range plan.Metrics {
		value := int64(readyNodes)
		if rule.Name == "fleet_nodes_expected" {
			value = int64(alivePairs)
		}
		if value < rule.Minimum || value > rule.Maximum {
			return observedFleet{}, fmt.Errorf("%w: metric %s=%d ready=%d alive=%d", errRefused, rule.Name, value, readyNodes, alivePairs)
		}
		metrics[index] = MetricValue{Name: rule.Name, Value: value}
	}
	actor, actorErr := roleActor(plan.Roles, "service-operator")
	if actorErr != nil || actor == "" {
		return observedFleet{}, fmt.Errorf("%w: service actor", errRefused)
	}
	observedGeneration := uint64(0)
	if readyNodes == len(plan.Nodes) && goldenNodes == len(plan.Nodes) {
		observedGeneration++
	}
	return observedFleet{
		auditPresent: len(stamps) == len(plan.Nodes) && actor != "", freshnessGeneration: observedGeneration,
		goldenPresent: goldenNodes == len(plan.Nodes), goldenNodes: goldenNodes, liveProcessPairs: alivePairs,
		metrics: metrics, processPresent: alivePairs == len(plan.Nodes),
		readyNodes: readyNodes, requiredNodes: len(plan.Nodes), servedStamps: stamps,
	}, nil
}

func recheckExecutable(value Executable, expected string) error {
	resolved, sha, err := resolvedExecutable(value.Path)
	if err != nil || resolved != value.Path || sha != expected || value.SHA256 != expected {
		return errRefused
	}
	return nil
}

func startKurnd(ctx context.Context, path, dataRoot, credentialPath string) (*liveChild, string, <-chan error, error) {
	args := []string{"-addr", "127.0.0.1:0", "-data", dataRoot, "-journal-fsync", "none", "-journal-fsync-interval", "2ms",
		"-query-mem-budget-mb", "0", "-query-queue-timeout", "2s", "-api-keys-file", credentialPath}
	command := exec.Command(path, args...)
	command.Env = []string{"LANG=C", "LC_ALL=C"}
	command.Stdin = nil
	command.Stdout = io.Discard
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stderr, pipeErr := command.StderrPipe()
	if pipeErr != nil || command.Start() != nil {
		return nil, "", nil, fmt.Errorf("%w: pipe or exec", errRefused)
	}
	child := &liveChild{command: command, done: make(chan error, 1)}
	go func() { child.done <- command.Wait() }()
	address := make(chan string, 1)
	scanDone := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(stderr)
		scanner.Buffer(make([]byte, 4096), maxProcessOutput)
		total := 0
		sent := false
		for scanner.Scan() {
			line := scanner.Text()
			total += len(line) + 1
			if total > maxProcessOutput {
				scanDone <- errRefused
				return
			}
			const prefix = "kurn: listening on "
			if !sent {
				if offset := strings.Index(line, prefix); offset >= 0 {
					address <- strings.TrimSpace(line[offset+len(prefix):])
					sent = true
				}
			}
		}
		if scanner.Err() != nil || !sent {
			scanDone <- errRefused
			return
		}
		scanDone <- nil
	}()
	select {
	case value := <-address:
		host, _, splitErr := net.SplitHostPort(value)
		if splitErr != nil || host != "127.0.0.1" {
			return child, "", scanDone, fmt.Errorf("%w: listen address", errRefused)
		}
		return child, "http://" + value, scanDone, nil
	case <-ctx.Done():
		return child, "", scanDone, fmt.Errorf("%w: listen deadline", errRefused)
	case child.waitErr = <-child.done:
		child.waited = true
		return child, "", scanDone, fmt.Errorf("%w: pre-listen exit: %v", errRefused, child.waitErr)
	}
}

func startSearchd(ctx context.Context, path, root, backendURL string, credential []byte, plan Plan) (*liveChild, string, *cappedBuffer, error) {
	credentialPath := filepath.Join(root, "backend.credential")
	authPath := filepath.Join(root, "fixture-auth.json")
	readinessPath := filepath.Join(root, "readiness.json")
	statusPath := filepath.Join(root, "runtime-status.json")
	configPath := filepath.Join(root, "searchd-config.json")
	writeJSON := func(path string, value any) error {
		raw, marshalErr := json.Marshal(value)
		if marshalErr != nil {
			return errRefused
		}
		return writePrivate(path, raw)
	}
	if writePrivate(credentialPath, credential) != nil ||
		writeJSON(authPath, map[string]any{"schema": "kurn-search-fixture-auth/v1", "credentials": []any{map[string]any{"handle": "client-token", "tenant": "tenant-main"}}}) != nil ||
		writeJSON(readinessPath, map[string]any{"schema": "kurn-search-readiness/v1", "goldens": []any{map[string]any{
			"name": NeutralGoldenID, "route": "catalog-identifier", "auth_handle": "client-token", "sentinel_tenant": "tenant-main",
			"query": "SKU-314", "partition_id": "partition-main", "limit": 25,
			"expected_ids": []string{"main-exact", "main-prefix", "main-suffix", "main-word"}, "expected_complete": true,
		}}}) != nil ||
		writeJSON(configPath, map[string]any{
			"schema": "kurn-search-private-local-proof/v1", "listen_address": "127.0.0.1:0", "runtime_status_file": statusPath,
			"kurn_endpoint": backendURL, "backend_credential_file": credentialPath, "kurn_source_revision": KurnSourceRevision,
			"rate": map[string]any{"unlimited": true, "rate_per_sec": 0},
			"adapter": map[string]any{"name": "catalog-identifier", "physical_list_name": PhysicalListName,
				"config_sha256": plan.Bundle.ConfigSHA256, "timeout_millis": plan.ProbeDeadlineMillis,
				"max_inflight": 1, "worst_case_charge_bytes": uint64(1 << 30)},
			"fixture_authenticator_file": authPath, "readiness_file": readinessPath,
		}) != nil {
		return nil, "", nil, errRefused
	}
	stderr := new(cappedBuffer)
	command := exec.Command(path, "-config", configPath)
	command.Env = []string{"LANG=C", "LC_ALL=C"}
	command.Stdin = nil
	command.Stdout = io.Discard
	command.Stderr = stderr
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if command.Start() != nil {
		return nil, "", stderr, errRefused
	}
	child := &liveChild{command: command, done: make(chan error, 1)}
	go func() { child.done <- command.Wait() }()
	for {
		raw, readErr := readRegular(statusPath, MaxControlBytes, modePointer(0o600))
		if readErr == nil {
			var status struct {
				Schema  string `json:"schema"`
				Ready   bool   `json:"ready"`
				Address string `json:"address"`
			}
			decoder := json.NewDecoder(bytes.NewReader(raw))
			decoder.DisallowUnknownFields()
			if rejectDuplicates(raw) != nil || decoder.Decode(&status) != nil || requireEOF(decoder) != nil {
				return child, "", stderr, errRefused
			}
			host, _, splitErr := net.SplitHostPort(status.Address)
			if status.Schema != "kurn-search-private-local-status/v1" || !status.Ready || splitErr != nil || host != "127.0.0.1" {
				return child, "", stderr, errRefused
			}
			return child, status.Address, stderr, nil
		}
		if child.pollExited() {
			return child, "", stderr, errRefused
		}
		select {
		case <-ctx.Done():
			return child, "", stderr, errRefused
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func getSignal(ctx context.Context, endpoint, bearer string, requireJSON bool) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, errRefused
	}
	if bearer != "" {
		request.Header.Set("Authorization", "Bearer "+bearer)
	}
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return errRefused }}
	response, err := client.Do(request)
	if err != nil {
		return nil, errRefused
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || (requireJSON && !strings.HasPrefix(response.Header.Get("Content-Type"), "application/json")) {
		return nil, errRefused
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, MaxControlBytes+1))
	if err != nil || len(raw) > MaxControlBytes {
		return nil, errRefused
	}
	return raw, nil
}

func goldenProbe(ctx context.Context, baseURL, expectedStamp string) error {
	body, err := json.Marshal(map[string]any{"query": "SKU-314", "partition_id": "partition-main", "limit": 25})
	if err != nil {
		return errRefused
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/v1/search/catalog-identifier", bytes.NewReader(body))
	if err != nil {
		return errRefused
	}
	request.Header.Set("Authorization", "Bearer client-token")
	request.Header.Set("Content-Type", "application/json")
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return errRefused }}
	response, err := client.Do(request)
	if err != nil {
		return errRefused
	}
	defer response.Body.Close()
	raw, readErr := io.ReadAll(io.LimitReader(response.Body, MaxControlBytes+1))
	var decoded struct {
		Complete bool `json:"complete"`
		Hits     []struct {
			EntryID string `json:"entry_id"`
		} `json:"hits"`
		Lists []struct {
			Stamp string `json:"stamp"`
		} `json:"lists"`
	}
	expected := []string{"main-exact", "main-prefix", "main-suffix", "main-word"}
	if response.StatusCode != http.StatusOK || readErr != nil || len(raw) > MaxControlBytes || rejectDuplicates(raw) != nil ||
		json.Unmarshal(raw, &decoded) != nil || !decoded.Complete || len(decoded.Hits) != len(expected) || len(decoded.Lists) != 1 || decoded.Lists[0].Stamp != expectedStamp {
		return errRefused
	}
	for index, id := range expected {
		if decoded.Hits[index].EntryID != id {
			return errRefused
		}
	}
	return nil
}
