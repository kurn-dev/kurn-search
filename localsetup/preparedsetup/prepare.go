package preparedsetup

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/kurn-dev/kurn-search/adapters/partscatalogsuggest"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/kurn-dev/kurn-search/internal/localproof"
)

type Options struct {
	Source, Kurn, Kurnd, Searchd, SearchCheckout, CoreSourceInventory, SearchSourceInventory, BuildDir, Out, ReadinessTenant, ReadinessPartition, KurndAddress, SearchdAddress, Profile string
	// MaxInflight is the admission limit bound into the version (0 means 1).
	MaxInflight int
	// MaxQueue bounds the requests waiting for admission in the version's
	// searchd (0 selects defaultQueueFactor × MaxInflight); it is written
	// explicitly into the searchd configuration and the manifest. Excess
	// requests are refused at once with 503 service_queue_full.
	MaxQueue int
	// Policy selects the adapter's behaviour policy for the new version.
	// "" is the default selection, partscatalogsuggest.PolicyOSA1G60
	// (generation 0.60 with the OSA-1 verifier and the triggered
	// transposition step); PolicyLegacy binds the legacy behaviour (0.65
	// with the empty-pool transposition fallback), which is what an
	// existing bound configuration without a policy member denotes; the
	// other adapter policies select themselves. The selected policy is
	// always persisted explicitly in the searchd configuration, the
	// profile diagnostic and the manifest; the list mapping and artifact
	// are the same for every policy.
	Policy string
}

// PolicyLegacy is the Options.Policy / -policy value that prepares the
// legacy behaviour: the adapter's empty policy, carried by an absent policy
// member exactly as versions prepared before the 0.60 default were bound.
const PolicyLegacy = "legacy"

// selectPolicy maps the option to the policy bound into the version.
func selectPolicy(policy string) (string, bool) {
	switch policy {
	case "":
		return partscatalogsuggest.PolicyOSA1G60, true
	case PolicyLegacy:
		return "", true
	}
	return policy, validPolicy(policy) && policy != ""
}

type Result struct {
	ManifestSHA256 string `json:"manifest_sha256"`
	Records        int    `json:"records"`
	Schema         string `json:"schema"`
	Status         string `json:"status"`
	Tenants        int    `json:"tenants"`
}
type failureReceipt struct {
	Schema string `json:"schema"`
	Status string `json:"status"`
}
type stateError struct{ state string }

func (e *stateError) Error() string   { return "preparedsetup: " + e.state }
func stateFailure(state string) error { return &stateError{state: state} }

type prepareDependencies struct {
	run                 func(context.Context, string, string, ...string) (commandResult, error)
	rename              func(string, string) error
	syncDir             func(string) error
	syncFile            func(string) error
	removeOwned         func(string, reservationOwnership) error
	load                func(string) error
	verify              func(string) (Verification, error)
	executableAssertion func(string) (buildAssertion, error)
}

func productionPrepareDependencies() prepareDependencies {
	return prepareDependencies{
		run: runCommand, rename: os.Rename, syncDir: syncDir, syncFile: syncFile, removeOwned: removeOwned,
		load:   func(path string) error { _, err := localproof.Load(path); return err },
		verify: VerifyVersion, executableAssertion: executableAssertion,
	}
}

var mappingBytes = []byte("{\n  \"format\": \"ndjson\",\n  \"id\": \"id\",\n  \"keys\": [\n    {\"path\": \"identifier\"}\n  ],\n  \"payload\": {\n    \"category\": \"category\",\n    \"id\": \"id\",\n    \"identifier\": \"identifier\",\n    \"manufacturer\": \"manufacturer\",\n    \"partition_id\": \"partition_id\",\n    \"status\": \"status\",\n    \"tenant_id\": \"tenant_id\"\n  },\n  \"list\": {\n    \"analyzer\": {\"steps\": [\"lowercase\", \"trim\"]},\n    \"match\": {\"mode\": \"ngram\", \"grams\": [2, 3], \"strip_spaces\": false, \"threshold\": 0.65, \"topk\": 100, \"exact_companion\": true},\n    \"filterable\": [\n      {\"name\": \"category\", \"path\": \"category\"},\n      {\"name\": \"manufacturer\", \"path\": \"manufacturer\"},\n      {\"name\": \"partition_id\", \"path\": \"partition_id\"},\n      {\"name\": \"status\", \"path\": \"status\"},\n      {\"name\": \"tenant_id\", \"path\": \"tenant_id\"}\n    ]\n  }\n}\n")

func Prepare(ctx context.Context, o Options) (result Result, retErr error) {
	return prepareWithDependencies(ctx, o, productionPrepareDependencies())
}

func prepareWithDependencies(ctx context.Context, o Options, deps prepareDependencies) (result Result, retErr error) {
	bound, ok := selectPolicy(o.Policy)
	if !ok {
		return Result{}, &stateError{state: "REFUSED"}
	}
	o.Policy = bound
	if o.MaxInflight == 0 {
		o.MaxInflight = defaultMaxInflight
	}
	if o.MaxQueue == 0 {
		o.MaxQueue = defaultQueueFactor * o.MaxInflight
	}
	if !validMaxQueue(o.MaxQueue) {
		return Result{}, &stateError{state: "REFUSED"}
	}
	if !validMaxInflight(o.MaxInflight) {
		return Result{}, errRefused
	}
	if ctx == nil || !validOptions(o) {
		return Result{}, errRefused
	}
	set, e := readSource(o.Source)
	if e != nil {
		return Result{}, e
	}
	projected, projectedSHA, keys, e := serializeProjected(set)
	if e != nil {
		return Result{}, e
	}
	ready, e := preflight(set, o.ReadinessTenant, o.ReadinessPartition)
	if e != nil {
		return Result{}, e
	}
	btmp, binfo, e := reserve(o.BuildDir)
	if e != nil {
		return Result{}, e
	}
	otmp, oinfo, e := reserve(o.Out)
	if e != nil {
		if deps.removeOwned(btmp, binfo) != nil {
			return Result{}, stateFailure("REFUSED_RETAINED_REMNANTS")
		}
		return Result{}, e
	}
	bpub, opub, success := false, false, false
	defer func() {
		if success {
			return
		}
		cleanupFailed := false
		if !bpub {
			cleanupFailed = deps.removeOwned(btmp, binfo) != nil
		}
		if !opub {
			cleanupFailed = deps.removeOwned(otmp, oinfo) != nil || cleanupFailed
		}
		if cleanupFailed {
			retErr = stateFailure("REFUSED_RETAINED_REMNANTS")
		}
	}()
	state := filepath.Join(otmp, "state")
	data := filepath.Join(otmp, "data")
	bin := filepath.Join(otmp, "bin")
	for _, d := range []string{state, data, bin} {
		if os.Mkdir(d, 0700) != nil {
			return Result{}, errRefused
		}
	}
	if captureOwnedDirectory(otmp, "data", &oinfo) != nil || captureOwnedDirectory(otmp, "bin", &oinfo) != nil {
		return Result{}, errRefused
	}
	if writeFile(filepath.Join(btmp, "projected.ndjson"), projected, 0400) != nil || writeFile(filepath.Join(btmp, "mapping.json"), mappingBytes, 0400) != nil {
		return Result{}, errRefused
	}
	artifact := filepath.Join(data, listName+".kurn")
	build, e := deps.run(ctx, btmp, o.Kurn, "build", "-mapping", filepath.Join(btmp, "mapping.json"), "-in", filepath.Join(btmp, "projected.ndjson"), "-artifact", artifact, "-name", listName)
	if e != nil {
		return Result{}, e
	}
	if len(build.stderr) != 0 {
		return Result{}, errRefused
	}
	if writeFile(filepath.Join(btmp, "build.stdout"), build.stdout, 0400) != nil || writeFile(filepath.Join(btmp, "build.stderr"), build.stderr, 0400) != nil {
		return Result{}, errRefused
	}
	verify, e := deps.run(ctx, btmp, o.Kurn, "verify", "-semantic", artifact)
	if e != nil || len(verify.stderr) != 0 {
		return Result{}, errRefused
	}
	rep, e := parseVerify(verify.stdout, artifact)
	if e != nil || rep.Input != projectedSHA || rep.Entries != uint64(len(set.records)) || rep.Keys != uint64(keys) {
		return Result{}, errRefused
	}
	container, _, e := fileSHA(artifact)
	if e != nil || container != rep.Container || parseBuild(build.stdout, artifact, projectedSHA, container, rep.Base, rep.Entries, rep.Keys) != nil {
		return Result{}, errRefused
	}
	if writeFile(filepath.Join(btmp, "verify.stdout"), verify.stdout, 0400) != nil || writeFile(filepath.Join(btmp, "verify.stderr"), verify.stderr, 0400) != nil {
		return Result{}, errRefused
	}
	if copyFile(filepath.Join(bin, "kurnd"), o.Kurnd, 0500) != nil || copyFile(filepath.Join(bin, "kurn-searchd"), o.Searchd, 0500) != nil {
		return Result{}, errRefused
	}
	if os.Chmod(artifact, 0400) != nil {
		return Result{}, errRefused
	}
	if deps.syncFile(artifact) != nil {
		return Result{}, errRefused
	}
	backend, kurndKeys, client, handles, e := makeSecrets(set.tenants)
	if e != nil {
		return Result{}, e
	}
	authRaw, _ := json.Marshal(map[string]any{"credentials": credentialsForAuth(set.tenants, handles), "schema": localproof.AuthenticatorSchema})
	authRaw = append(authRaw, '\n')
	readRaw, _ := json.Marshal(map[string]any{"goldens": []any{map[string]any{"auth_handle": handles[ready.tenant], "expected_complete": true, "expected_ids": []string{ready.expectedID}, "limit": 1, "name": "prepared-ready", "partition_id": ready.partition, "query": ready.query, "route": "parts-catalog-suggest", "sentinel_tenant": ready.tenant}}, "schema": localproof.ReadinessSchema})
	readRaw = append(readRaw, '\n')
	desc, e := descriptorFor(o.MaxInflight, o.Policy, localproof.KurnCompatibilityLabel)
	if e != nil {
		return Result{}, errRefused
	}
	diag := desc.DiagnosticJSON()
	controls := map[string][]byte{"backend-token": backend, "kurnd-api-keys": kurndKeys, "client-credentials.json": client, "fixture-auth.json": authRaw, "readiness.json": readRaw, "profile-diagnostic.json": diag}
	for n, v := range controls {
		if writeFile(filepath.Join(otmp, n), v, 0400) != nil {
			return Result{}, errRefused
		}
	}
	staged := configBytes(otmp, o, rep)
	if writeFile(filepath.Join(otmp, "kurn-searchd.json"), staged, 0400) != nil {
		return Result{}, errRefused
	}
	if e := deps.load(filepath.Join(otmp, "kurn-searchd.json")); e != nil {
		return Result{}, errRefused
	}
	final := configBytes(o.Out, o, rep)
	if !sameConfigExceptPaths(staged, final) {
		return Result{}, errRefused
	}
	tmpConfig := filepath.Join(otmp, ".final-config")
	if writeFile(tmpConfig, final, 0400) != nil || deps.rename(tmpConfig, filepath.Join(otmp, "kurn-searchd.json")) != nil {
		return Result{}, errRefused
	}
	m, e := buildManifest(o, set, projectedSHA, keys, rep, ready, diag, otmp, deps.executableAssertion)
	if e != nil {
		return Result{}, e
	}
	mraw, e := marshalManifest(m)
	if e != nil {
		return Result{}, e
	}
	if writeFile(filepath.Join(otmp, "manifest.json"), mraw, 0400) != nil || writeFile(filepath.Join(btmp, "manifest.json"), mraw, 0400) != nil || writeFile(filepath.Join(btmp, "manifest.sha256"), []byte(bytesSHA(mraw)+"\n"), 0400) != nil {
		return Result{}, errRefused
	}
	if os.Chmod(data, 0500) != nil || os.Chmod(bin, 0500) != nil {
		return Result{}, errRefused
	}
	for _, d := range []string{state, data, bin, otmp, btmp} {
		if deps.syncDir(d) != nil {
			return Result{}, errRefused
		}
	}
	if publishWith(btmp, o.BuildDir, deps.rename, deps.syncDir) != nil {
		if !absent(o.BuildDir) {
			return Result{}, stateFailure("UNCERTAIN")
		}
		return Result{}, stateFailure("REFUSED")
	}
	bpub = true
	if publishWith(otmp, o.Out, deps.rename, deps.syncDir) != nil {
		if !absent(o.Out) {
			return Result{}, stateFailure("UNCERTAIN")
		}
		return Result{}, stateFailure("PARTIAL")
	}
	opub = true
	if e := deps.load(filepath.Join(o.Out, "kurn-searchd.json")); e != nil {
		return Result{}, stateFailure("REFUSED_AFTER_PUBLICATION")
	}
	if _, e := deps.verify(o.Out); e != nil {
		return Result{}, stateFailure("REFUSED_AFTER_PUBLICATION")
	}
	success = true
	return Result{ManifestSHA256: bytesSHA(mraw), Records: len(set.records), Schema: "kurn-search-prepared-result/v1", Status: "PREPARED", Tenants: len(set.tenants)}, nil
}

func validOptions(o Options) bool {
	if o.Profile != "parts-catalog-suggest" || !validPolicy(o.Policy) || o.KurndAddress == o.SearchdAddress || !regularPhysical(o.Source, false) || !regularPhysical(o.Kurn, true) || !regularPhysical(o.Kurnd, true) || !regularPhysical(o.Searchd, true) || !physicalDirectory(o.SearchCheckout) {
		return false
	}
	for _, address := range []string{o.KurndAddress, o.SearchdAddress} {
		host, port, e := net.SplitHostPort(address)
		if e != nil || host != "127.0.0.1" || port == "" {
			return false
		}
	}
	for _, target := range []string{o.BuildDir, o.Out} {
		if !filepath.IsAbs(target) || within(target, o.SearchCheckout) || within(target, o.Source) {
			return false
		}
	}
	return !within(o.BuildDir, o.Out) && !within(o.Out, o.BuildDir)
}
func physicalDirectory(path string) bool {
	i, e := os.Lstat(path)
	return e == nil && i.IsDir() && i.Mode()&os.ModeSymlink == 0
}
func regularPhysical(path string, executable bool) bool {
	i, e := os.Lstat(path)
	if e != nil || !i.Mode().IsRegular() || i.Mode()&os.ModeSymlink != 0 {
		return false
	}
	return !executable || i.Mode().Perm()&0111 != 0
}
func within(path, parent string) bool {
	p, e1 := filepath.Abs(path)
	q, e2 := filepath.Abs(parent)
	if e1 != nil || e2 != nil {
		return true
	}
	rel, e := filepath.Rel(q, p)
	return e != nil || rel == "." || (!strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != "..")
}

func credentialsForAuth(tenants []string, handles map[string]string) []map[string]string {
	v := make([]map[string]string, len(tenants))
	for i, t := range tenants {
		v[i] = map[string]string{"handle": handles[t], "tenant": t}
	}
	return v
}

func configBytes(root string, o Options, r verifyReport) []byte {
	adapterDoc := map[string]any{"config_sha256": configSHA, "max_inflight": o.MaxInflight, "max_queue": o.MaxQueue, "name": "parts-catalog-suggest", "physical_list_name": listName, "timeout_millis": 30000, "worst_case_charge_bytes": 1073741824}
	if o.Policy != "" {
		adapterDoc["policy"] = o.Policy
	}
	v := map[string]any{"adapter": adapterDoc, "admission": "prepared_full", "backend_credential_file": filepath.Join(root, "backend-token"), "expected_base_sha256": r.Base, "expected_entries": r.Entries, "fixture_authenticator_file": filepath.Join(root, "fixture-auth.json"), "kurn_endpoint": "http://" + o.KurndAddress, "kurn_source_revision": localproof.KurnSourceRevision, "listen_address": o.SearchdAddress, "rate": map[string]any{"rate_per_sec": 0, "unlimited": true}, "readiness_file": filepath.Join(root, "readiness.json"), "runtime_status_file": filepath.Join(root, "state", "search-status.json"), "schema": "kurn-search-prepared-local/v1"}
	raw, _ := json.Marshal(v)
	return append(raw, '\n')
}
func sameConfigExceptPaths(a, b []byte) bool {
	var x, y map[string]any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	for _, k := range []string{"runtime_status_file", "backend_credential_file", "fixture_authenticator_file", "readiness_file"} {
		y[k] = x[k]
	}
	xb, _ := json.Marshal(x)
	yb, _ := json.Marshal(y)
	return bytes.Equal(xb, yb)
}

func buildManifest(o Options, set sourceSet, projectedSHA string, keys int, r verifyReport, ready readinessBinding, diag []byte, root string, assertion func(string) (buildAssertion, error)) (manifest, error) {
	var m manifest
	m.Schema = manifestSchema
	m.Profile.Name = "parts-catalog-suggest"
	m.Profile.Policy = o.Policy
	m.Profile.Version = "v1"
	d, _ := descriptorFor(o.MaxInflight, o.Policy, localproof.KurnCompatibilityLabel)
	ids := d.Identities()
	m.Profile.CodecSHA256 = ids.Codec.Hex()
	m.Profile.SearchSHA256 = ids.Search.Hex()
	m.Profile.BehaviorSHA256 = ids.Behavior.Hex()
	m.Profile.DiagnosticFile = "profile-diagnostic.json"
	m.Profile.DiagnosticSHA256 = bytesSHA(diag)
	m.Operational.PhysicalListName = listName
	m.Operational.ConfigSHA256 = configSHA
	m.Operational.TestedEngine = localproof.KurnCompatibilityLabel
	m.Operational.TimeoutMillis = 30000
	m.Operational.MaxInflight = o.MaxInflight
	m.Operational.MaxQueue = o.MaxQueue
	m.Operational.WorstCaseChargeBytes = 1073741824
	m.Operational.Admission = "prepared_full"
	m.Operational.ExpectedBaseSHA256 = r.Base
	m.Operational.ExpectedEntries = r.Entries
	m.Recorded.Source.SHA256 = set.sha256
	m.Recorded.Source.Bytes = set.bytes
	m.Recorded.Source.Records = int64(len(set.records))
	m.Recorded.Source.Tenants = int64(len(set.tenants))
	m.Recorded.Projected.LinesSHA256 = projectedSHA
	m.Recorded.Projected.Keys = keys
	m.Recorded.MappingSHA256 = bytesSHA(mappingBytes)
	m.Recorded.Artifact.Path = "data/" + listName + ".kurn"
	m.Recorded.Artifact.ContainerSHA256 = r.Container
	m.Recorded.Executables = map[string]byteIdentity{}
	m.Recorded.SearchContract = map[string]string{}
	m.Recorded.Files = map[string]fileIdentity{}
	paths := map[string]string{"kurn": o.Kurn, "kurnd": o.Kurnd, "kurn-searchd": o.Searchd}
	self, e := os.Executable()
	if e != nil {
		return manifest{}, errRefused
	}
	paths["kurn-search-local"] = self
	m.Asserted.Executables = map[string]buildAssertion{}
	for n, p := range paths {
		sha, size, e := fileSHA(p)
		if e != nil {
			return manifest{}, e
		}
		m.Recorded.Executables[n] = byteIdentity{SHA256: sha, Bytes: size}
		a, e := assertion(p)
		if e != nil {
			return manifest{}, e
		}
		m.Asserted.Executables[n] = a
	}
	contracts := map[string]string{"openapi_manifest_sha256": "openapi/current-manifest.json", "clients_manifest_sha256": "clients/current-manifest.json", "typescript_client_mjs_sha256": "clients/typescript/generated/client.mjs", "typescript_client_dts_sha256": "clients/typescript/generated/client.d.ts"}
	for n, p := range contracts {
		sha, _, e := fileSHA(filepath.Join(o.SearchCheckout, p))
		if e != nil {
			return manifest{}, e
		}
		m.Recorded.SearchContract[n] = sha
	}
	core, e := inventoryDigest(o.CoreSourceInventory, false)
	if e != nil {
		return manifest{}, e
	}
	search, e := inventoryDigest(o.SearchSourceInventory, false)
	if e != nil {
		return manifest{}, e
	}
	m.Asserted.CoreInventorySHA256 = core
	m.Asserted.SearchInventorySHA256 = search
	m.Reported.VerifyLevel = "full + semantic"
	m.Reported.List = listName
	m.Reported.Mode = "ngram"
	m.Reported.Format = "KURNART1 1.1"
	m.Reported.Entries = r.Entries
	m.Reported.Keys = r.Keys
	m.Reported.BaseSHA256 = r.Base
	m.Reported.ContainerSHA256 = r.Container
	m.Reported.ConfigDeclared = configSHA
	m.Reported.ConfigRecomputed = configSHA
	m.Reported.AnalyzerDeclared = analyzerSHA
	m.Reported.AnalyzerRecomputed = analyzerSHA
	m.Reported.InputSHA256 = r.Input
	m.Readiness.Tenant = ready.tenant
	m.Readiness.Partition = ready.partition
	m.Readiness.Query = ready.query
	m.Readiness.ExpectedID = ready.expectedID
	for _, name := range []string{"kurn-searchd.json", "backend-token", "fixture-auth.json", "readiness.json", "kurnd-api-keys", "client-credentials.json", "profile-diagnostic.json", "bin/kurnd", "bin/kurn-searchd", "data/" + listName + ".kurn"} {
		sha, _, e := fileSHA(filepath.Join(root, name))
		if e != nil {
			return manifest{}, e
		}
		i, _ := os.Stat(filepath.Join(root, name))
		m.Recorded.Files[name] = fileIdentity{SHA256: sha, Mode: fmt.Sprintf("%04o", i.Mode().Perm())}
	}
	return m, nil
}

func writeJSON(w io.Writer, v any) error {
	raw, e := json.Marshal(v)
	if e != nil {
		return errRefused
	}
	raw = append(raw, '\n')
	n, e := w.Write(raw)
	if e != nil || n != len(raw) {
		return errRefused
	}
	return nil
}

func Run(args []string, stdout, stderr io.Writer) int {
	return runWithPrepare(args, stdout, stderr, Prepare)
}

func runWithPrepare(args []string, stdout, stderr io.Writer, prepare func(context.Context, Options) (Result, error)) int {
	refuse := func() int {
		if stderr != nil {
			_, _ = io.WriteString(stderr, "kurn-search-local: refused\n")
		}
		return 1
	}
	if stdout == nil || stderr == nil || len(args) == 0 {
		return refuse()
	}
	if args[0] == "verify-version" {
		if len(args) != 3 || args[1] != "-out" || args[2] == "" {
			return refuse()
		}
		v, e := VerifyVersion(args[2])
		if e != nil {
			return refuse()
		}
		if writeJSON(stdout, v) != nil {
			return refuse()
		}
		return 0
	}
	if args[0] != "prepare-artifact" {
		return refuse()
	}
	if len(args[1:])%2 != 0 {
		return refuse()
	}
	values := map[string]string{}
	allowed := map[string]bool{"-profile": true, "-source": true, "-kurn": true, "-kurnd": true, "-searchd": true, "-search-checkout": true, "-core-source-inventory": true, "-search-source-inventory": true, "-build-dir": true, "-out": true, "-readiness-tenant": true, "-readiness-partition": true, "-kurnd-addr": true, "-searchd-addr": true, "-max-inflight": true, "-max-queue": true, "-policy": true}
	for i := 1; i < len(args); i += 2 {
		if !allowed[args[i]] || args[i+1] == "" || values[args[i]] != "" {
			return refuse()
		}
		values[args[i]] = args[i+1]
	}
	for _, name := range []string{"-profile", "-source", "-kurn", "-kurnd", "-searchd", "-search-checkout", "-build-dir", "-out", "-readiness-tenant", "-readiness-partition", "-kurnd-addr", "-searchd-addr"} {
		if values[name] == "" {
			return refuse()
		}
	}
	o := Options{Profile: values["-profile"], Source: values["-source"], Kurn: values["-kurn"], Kurnd: values["-kurnd"], Searchd: values["-searchd"], SearchCheckout: values["-search-checkout"], CoreSourceInventory: values["-core-source-inventory"], SearchSourceInventory: values["-search-source-inventory"], BuildDir: values["-build-dir"], Out: values["-out"], ReadinessTenant: values["-readiness-tenant"], ReadinessPartition: values["-readiness-partition"], KurndAddress: values["-kurnd-addr"], SearchdAddress: values["-searchd-addr"]}
	if raw, ok := values["-max-inflight"]; ok {
		n, ok := parseMaxInflight(raw)
		if !ok {
			return refuse()
		}
		o.MaxInflight = n
	}
	if raw, ok := values["-max-queue"]; ok {
		n, ok := parseMaxQueue(raw)
		if !ok {
			return refuse()
		}
		o.MaxQueue = n
	}
	if raw, ok := values["-policy"]; ok {
		if _, ok := selectPolicy(raw); !ok {
			return refuse()
		}
		o.Policy = raw
	}
	result, e := prepare(context.Background(), o)
	if e != nil {
		state := "REFUSED"
		if typed, ok := e.(*stateError); ok {
			state = typed.state
		}
		if writeJSON(stderr, failureReceipt{Schema: "kurn-search-prepared-result/v1", Status: state}) != nil {
			return refuse()
		}
		return 1
	}
	if writeJSON(stdout, result) != nil {
		return refuse()
	}
	return 0
}
