package fleet

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type stagedFile struct {
	name string
	raw  []byte
	sha  string
}

func Converge(planDir, stateOut string) (Result, error) {
	plan, planRaw, privateRaw, err := loadPlanDir(planDir)
	if err != nil || resolvedAbsent(stateOut) != nil || overlap(planDir, stateOut) || overlap(plan.RegistryRoot, stateOut) {
		return Result{}, fmt.Errorf("%w: plan or state root", errRefused)
	}
	for _, node := range plan.Nodes {
		if overlap(node.Root, stateOut) || overlap(node.Root, planDir) {
			return Result{}, errRefused
		}
	}
	files, bundleSHA, err := readArtifactBundle(plan)
	if err != nil {
		return Result{}, fmt.Errorf("%w: artifact bundle", errRefused)
	}
	states := make([]NodeState, 0, len(plan.Nodes))
	created := make([]string, 0, len(plan.Nodes))
	ok := false
	defer func() {
		if !ok {
			for _, path := range created {
				_ = os.RemoveAll(path)
			}
		}
	}()
	noop := 0
	for _, node := range plan.Nodes {
		wasNoop, err := stageNode(node.Root, files, bundleSHA)
		if err != nil {
			return Result{}, fmt.Errorf("%w: node stage", errRefused)
		}
		if wasNoop {
			noop++
		} else {
			created = append(created, node.Root)
		}
		states = append(states, NodeState{BundleSHA256: bundleSHA, ID: node.ID, Noop: wasNoop, Root: ""})
	}
	signals, err := observeFleet(plan)
	if err != nil {
		return Result{}, fmt.Errorf("%w: observation: %v", errRefused, err)
	}
	observation, observationRaw, err := observationFromSignals(plan, digest(planRaw), signals)
	if err != nil {
		return Result{}, fmt.Errorf("%w: observation admission", errRefused)
	}
	_, auditRaw, err := initialAudit(plan, digest(planRaw), observation)
	if err != nil {
		return Result{}, fmt.Errorf("%w: audit", errRefused)
	}
	state := State{
		ArtifactID: plan.ArtifactID, AuditSHA256: digest(auditRaw), Evidence: plan.Evidence, FleetID: plan.FleetID,
		Generation: plan.Generation, Nodes: states, ObservationSHA256: observation.ObservationSHA256, PlanSHA256: digest(planRaw),
		PredecessorArtifactID: plan.PredecessorArtifactID, RouteDecisionSHA256: plan.RouteDecisionSHA256,
		Schema: StateSchema, Status: observation.Decision,
	}
	stateRaw, err := canonical(state)
	if err != nil || privateTransaction(stateOut, map[string][]byte{
		"audit.json": auditRaw, "observation.json": observationRaw, "plan.json": planRaw, "private.json": privateRaw, "state.json": stateRaw,
	}) != nil {
		return Result{}, fmt.Errorf("%w: state transaction", errRefused)
	}
	ok = true
	return Result{Decision: observation.Decision, FleetID: plan.FleetID, Generation: plan.Generation, NoopNodes: noop,
		PlanSHA256: digest(planRaw), Schema: ResultSchema, ServedStampSHA256: observation.ServedStampSHA256, Status: "CONVERGED"}, nil
}

func readArtifactBundle(plan Plan) ([]stagedFile, string, error) {
	expectation, err := registryBundleExpectation(plan.RegistryRoot, plan.ArtifactID)
	if err != nil || expectation != plan.Bundle {
		return nil, "", errRefused
	}
	root := filepath.Join(plan.RegistryRoot, "artifacts", plan.ArtifactID, "bundle")
	if _, err := resolvedPrivateDirectory(root); err != nil {
		return nil, "", errRefused
	}
	files := make([]stagedFile, 0, 3)
	for _, name := range []string{"config.json", "base.jsonl", "base.idx"} {
		raw, err := readRegular(filepath.Join(root, name), MaxBundleBytes, modePointer(0o600))
		if err != nil {
			return nil, "", errRefused
		}
		files = append(files, stagedFile{name: name, raw: raw, sha: digest(raw)})
	}
	identity := make([]struct {
		Name   string `json:"name"`
		SHA256 string `json:"sha256"`
	}, len(files))
	for index, file := range files {
		identity[index] = struct {
			Name   string `json:"name"`
			SHA256 string `json:"sha256"`
		}{file.name, file.sha}
	}
	value, err := domainDigest("kurn-search-fleet-node-bundle/v1", identity)
	return files, value, err
}

func stageNode(root string, files []stagedFile, bundleSHA string) (bool, error) {
	if _, err := os.Lstat(root); err == nil {
		actual, verifyErr := verifyNodeRoot(root)
		return verifyErr == nil && actual == bundleSHA, func() error {
			if verifyErr != nil || actual != bundleSHA {
				return errRefused
			}
			return nil
		}()
	} else if !errors.Is(err, os.ErrNotExist) || resolvedAbsent(root) != nil {
		return false, errRefused
	}
	temporary := filepath.Join(filepath.Dir(root), "."+filepath.Base(root)+".s4-stage")
	if _, err := os.Lstat(temporary); !errors.Is(err, os.ErrNotExist) || os.Mkdir(temporary, 0o700) != nil {
		return false, errRefused
	}
	ok := false
	defer func() {
		if !ok {
			_ = os.RemoveAll(temporary)
		}
	}()
	// This is the reviewed public ship order. The retained predecessor is in
	// the immutable registry, not in the node root.
	for _, stale := range []string{"base.idx", "journal"} {
		if err := os.Remove(filepath.Join(temporary, stale)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return false, errRefused
		}
	}
	byName := map[string]stagedFile{}
	for _, file := range files {
		byName[file.name] = file
		temporaryFile := filepath.Join(temporary, "."+file.name+".tmp")
		if writePrivate(temporaryFile, file.raw) != nil || !equalRaw(temporaryFile, file.raw) {
			return false, errRefused
		}
	}
	for _, name := range []string{"config.json", "base.jsonl", "base.idx"} {
		file, present := byName[name]
		if !present || os.Rename(filepath.Join(temporary, "."+name+".tmp"), filepath.Join(temporary, name)) != nil ||
			!equalRaw(filepath.Join(temporary, name), file.raw) {
			return false, errRefused
		}
	}
	directory, err := os.Open(temporary)
	if err != nil || directory.Sync() != nil || directory.Close() != nil || os.Rename(temporary, root) != nil {
		return false, errRefused
	}
	actual, err := verifyNodeRoot(root)
	if err != nil || actual != bundleSHA {
		return false, errRefused
	}
	ok = true
	return false, nil
}

func verifyNodeRoot(root string) (string, error) {
	if exactDirectory(root, []string{"base.idx", "base.jsonl", "config.json"}) != nil {
		return "", errRefused
	}
	identity := make([]struct {
		Name   string `json:"name"`
		SHA256 string `json:"sha256"`
	}, 0, 3)
	for _, name := range []string{"config.json", "base.jsonl", "base.idx"} {
		raw, err := readRegular(filepath.Join(root, name), MaxBundleBytes, modePointer(0o600))
		if err != nil {
			return "", errRefused
		}
		identity = append(identity, struct {
			Name   string `json:"name"`
			SHA256 string `json:"sha256"`
		}{name, digest(raw)})
	}
	return domainDigest("kurn-search-fleet-node-bundle/v1", identity)
}

type observedFleet struct {
	auditPresent        bool
	freshnessGeneration uint64
	goldenPresent       bool
	goldenNodes         int
	liveProcessPairs    int
	metrics             []MetricValue
	processPresent      bool
	readyNodes          int
	requiredNodes       int
	servedStamps        []servedStampIdentity
}

func observationFromSignals(plan Plan, planSHA string, signals observedFleet) (Observation, []byte, error) {
	served, err := observedServedStampDigest(signals.servedStamps)
	if err != nil || served != plan.Evidence.ServedStampSHA256 {
		return Observation{}, nil, errRefused
	}
	observation := Observation{
		AuditPresent: signals.auditPresent, Decision: "", FreshnessGeneration: signals.freshnessGeneration,
		GoldenPresent: signals.goldenPresent, GoldenNodes: signals.goldenNodes, LiveProcessPairs: signals.liveProcessPairs,
		ManagementTopology: ManagementTopology, Metrics: append([]MetricValue(nil), signals.metrics...), PlanSHA256: planSHA,
		ProcessPresent: signals.processPresent, ReadyNodes: signals.readyNodes, RequiredNodes: signals.requiredNodes, Schema: ObservationSchema,
		ServedStampSHA256: served,
	}
	decision, err := EvaluateObservation(plan, observation)
	if err != nil || decision != "HEALTHY" {
		return Observation{}, nil, errRefused
	}
	observation.Decision = decision
	identity := observation
	identity.ObservationSHA256 = ""
	observation.ObservationSHA256, err = domainDigest("kurn-search-fleet-observation/v1", identity)
	if err != nil {
		return Observation{}, nil, errRefused
	}
	raw, err := canonical(observation)
	return observation, raw, err
}

func EvaluateObservation(plan Plan, observation Observation) (string, error) {
	if observation.Schema != ObservationSchema || observation.ManagementTopology != ManagementTopology ||
		!validSHA(observation.PlanSHA256) || !validSHA(observation.ServedStampSHA256) {
		return "", errRefused
	}
	if observation.ReadyNodes < 0 || observation.RequiredNodes != len(plan.Nodes) || observation.ReadyNodes > observation.RequiredNodes ||
		observation.GoldenNodes < 0 || observation.GoldenNodes > observation.RequiredNodes || observation.LiveProcessPairs < 0 || observation.LiveProcessPairs > observation.RequiredNodes ||
		observation.GoldenPresent != (observation.GoldenNodes == observation.RequiredNodes) || observation.ProcessPresent != (observation.LiveProcessPairs == observation.RequiredNodes) ||
		observation.FreshnessGeneration == 0 || observation.FreshnessGeneration > plan.MaxObservationGeneration {
		return "", errRefused
	}
	if len(observation.Metrics) != len(plan.Metrics) {
		return "", errRefused
	}
	for index, metric := range observation.Metrics {
		rule := plan.Metrics[index]
		if metric.Name != rule.Name || metric.Value < rule.Minimum || metric.Value > rule.Maximum {
			return "", errRefused
		}
	}
	if observation.ServedStampSHA256 != plan.Evidence.ServedStampSHA256 {
		return "DEGRADED", nil
	}
	if !observation.ProcessPresent || observation.ReadyNodes != observation.RequiredNodes {
		return "RECOVERY-REQUIRED", nil
	}
	if !observation.AuditPresent || !observation.GoldenPresent || observation.FreshnessGeneration != plan.RequiredObservationGeneration {
		return "HOLD", nil
	}
	return "HEALTHY", nil
}

func initialAudit(plan Plan, planSHA string, observation Observation) (Audit, []byte, error) {
	actor, err := roleActor(plan.Roles, "service-operator")
	if err != nil {
		return Audit{}, nil, errRefused
	}
	record := AuditRecord{ActorID: actor, Decision: observation.Decision, Generation: plan.Generation,
		ObservationSHA256: observation.ObservationSHA256, PlanSHA256: planSHA, PredecessorSHA256: plan.PredecessorPlanSHA256,
		Role: "service-operator"}
	record.RecordSHA256, err = domainDigest("kurn-search-fleet-audit-record/v1", record)
	if err != nil {
		return Audit{}, nil, errRefused
	}
	audit := Audit{Records: []AuditRecord{record}, Schema: AuditSchema}
	raw, err := canonical(audit)
	return audit, raw, err
}

func compareNodeStates(left, right []NodeState) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
