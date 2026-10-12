package fleet

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
)

func TestStrictCanonicalCodec(t *testing.T) {
	value := RecoverySpec{EvidenceReviewerActorID: "reviewer", ExpectedServedStampSHA256: hexValue('a'),
		ExpectedStateSHA256: hexValue('b'), Mode: "RESTORE", RecoveryGeneration: 2, RecoveryOperatorActorID: "recovery",
		Schema: RecoverySpecSchema, StepDeadlineMillis: 1000, TargetArtifactID: hexValue('c')}
	raw, err := MarshalRecoverySpec(value)
	if err != nil {
		t.Fatal(err)
	}
	var decoded RecoverySpec
	if decode(raw, MaxControlBytes, &decoded) != nil || !reflect.DeepEqual(value, decoded) {
		t.Fatal("canonical recovery spec did not round-trip")
	}
	for _, mutated := range [][]byte{
		bytes.TrimSuffix(raw, []byte{'\n'}),
		append([]byte(" "), raw...),
		[]byte(`{"schema":"kurn-search-fleet-recovery-spec/v1","schema":"kurn-search-fleet-recovery-spec/v1"}` + "\n"),
		append(append([]byte(nil), raw...), []byte("{}\n")...),
		[]byte("{\xff}\n"),
	} {
		if decode(mutated, MaxControlBytes, &decoded) == nil {
			t.Fatal("non-canonical or duplicate JSON was accepted")
		}
	}
}

func TestIdentityConstantsAreExact(t *testing.T) {
	if KurnSourceRevision != "f7423cd8cbb8165caf8aa750f56586a0ee621249" || KurnCompatibility != "v0.6.0-1-gf7423cd" ||
		KurnSHA256 != "a03086474de8c0c32dd501798b0ea0c0fdd1a93906722fa893eb8c5f4a8a6c55" ||
		KurndSHA256 != "240f20897a8d34e14123c343f1df11a6dadcae7deceff97c781323cdeee988e9" ||
		KurnSearchdSHA256 != "aed2bbb6f80ab1469bf5200c7ecc15080a9ac1a8e141bb06cd4c25591c78ee07" {
		t.Fatal("reviewed binary identity constants drifted")
	}
}

func TestNodesAreSortedDistinctAndAtLeastThree(t *testing.T) {
	parent := privateTemp(t)
	nodes := []NodeSpec{nodeForTest(t, parent, "node-a"), nodeForTest(t, parent, "node-b"), nodeForTest(t, parent, "node-c")}
	if validateNodes(nodes) != nil {
		t.Fatal("valid neutral inventory refused")
	}
	reordered := append([]NodeSpec(nil), nodes...)
	reordered[0].ID, reordered[1].ID = reordered[1].ID, reordered[0].ID
	duplicate := append([]NodeSpec(nil), nodes...)
	duplicate[1].ID = duplicate[0].ID
	overlapping := append([]NodeSpec(nil), nodes...)
	overlapping[1].Root = filepath.Join(nodes[0].Root, "child")
	for _, mutated := range [][]NodeSpec{nodes[:2], reordered, duplicate, overlapping} {
		if validateNodes(mutated) == nil {
			t.Fatal("invalid node inventory accepted")
		}
	}
}

func TestRoleSeparationIsClosed(t *testing.T) {
	roles := testRoles("fleet-a")
	if validateRoles("fleet-a", roles) != nil {
		t.Fatal("valid separated role matrix refused")
	}
	mutated := append([]RoleAssignment(nil), roles...)
	mutated[1].ActorID = mutated[0].ActorID
	if validateRoles("fleet-a", mutated) == nil {
		t.Fatal("self-approved role substitution accepted")
	}
	mutated = append([]RoleAssignment(nil), roles...)
	mutated[0].ScopeID = "fleet-b"
	if validateRoles("fleet-a", mutated) == nil {
		t.Fatal("wrong-scope role accepted")
	}
	mutated = append([]RoleAssignment(nil), roles...)
	mutated[0].Role = "unknown"
	if validateRoles("fleet-a", mutated) == nil {
		t.Fatal("unknown role accepted")
	}
}

func TestOwnershipLedgerIsGenerationBoundAndClosed(t *testing.T) {
	spec := evidenceSpec(t)
	ledger := ownershipLedgerForTest(spec)
	if ValidateOwnershipLedger(spec, ledger) != nil {
		t.Fatal("valid fixture ownership ledger refused")
	}
	mutations := []func(*OwnershipLedger){
		func(value *OwnershipLedger) { value.Schema = "wrong" },
		func(value *OwnershipLedger) { value.Approvals = value.Approvals[:4] },
		func(value *OwnershipLedger) { value.Approvals[0].ActorID = value.Approvals[1].ActorID },
		func(value *OwnershipLedger) { value.Approvals[0].ScopeID = "other-fleet" },
		func(value *OwnershipLedger) { value.Approvals[0].ValidThroughGeneration = 0 },
		func(value *OwnershipLedger) { value.Approvals[0].PredecessorPlanSHA256 = hexValue('f') },
		func(value *OwnershipLedger) { value.Exceptions[0].State = "OPEN" },
		func(value *OwnershipLedger) { value.Exceptions[0].ExpiresAfterGeneration = 0 },
		func(value *OwnershipLedger) { value.PostChanges[0].OwnerActorID = value.Approvals[0].ActorID },
		func(value *OwnershipLedger) { value.PostChanges[0].State = "OPEN" },
	}
	for _, mutate := range mutations {
		value := ledger
		value.Approvals = append([]DecisionApproval(nil), ledger.Approvals...)
		value.Exceptions = append([]ExceptionRecord(nil), ledger.Exceptions...)
		value.PostChanges = append([]PostChangeRecord(nil), ledger.PostChanges...)
		mutate(&value)
		if ValidateOwnershipLedger(spec, value) == nil {
			t.Fatal("invalid ownership decision, exception, or post-change record accepted")
		}
	}
}

func TestMetricAllowlistIsClosed(t *testing.T) {
	valid := []MetricRule{{Maximum: 3, Minimum: 3, Name: "fleet_nodes_expected"}, {Maximum: 3, Minimum: 3, Name: "fleet_nodes_ready"}}
	if validateMetrics(valid) != nil {
		t.Fatal("valid metrics refused")
	}
	for _, values := range [][]MetricRule{
		{},
		{{Maximum: 1, Minimum: 2, Name: "bad"}},
		{{Maximum: 3, Minimum: 0, Name: "tenant/list"}},
		{{Maximum: 3, Minimum: 0, Name: "z"}, {Maximum: 3, Minimum: 0, Name: "a"}},
	} {
		if validateMetrics(values) == nil {
			t.Fatal("unbounded or non-canonical metric accepted")
		}
	}
}

func TestManagementSignalStrictProjection(t *testing.T) {
	valid := []byte(`{"name":"catalog-identifiers-v1","entries":4,"overlay":0,"tombstones":0,"version":"stamp","mode":"contains","dropped_keys":0,"keyless_entries":0}` + "\n")
	signal, err := ParseManagementSignal(valid)
	if err != nil || signal.Entries != 4 || signal.Version != "stamp" {
		t.Fatal("valid management response refused")
	}
	for _, raw := range [][]byte{
		[]byte(`{"name":"catalog-identifiers-v1","name":"other","entries":4,"overlay":0,"tombstones":0,"version":"stamp","mode":"contains","dropped_keys":0,"keyless_entries":0}`),
		[]byte(`{"name":"catalog-identifiers-v1","entries":4,"overlay":0,"tombstones":0,"version":"stamp","mode":"contains","dropped_keys":0,"keyless_entries":0,"tenant":"private"}`),
		[]byte(`{"name":"catalog-identifiers-v1","entries":4,"overlay":1,"tombstones":0,"version":"stamp","mode":"contains","dropped_keys":0,"keyless_entries":0}`),
		[]byte(`{"name":"catalog-identifiers-v1"`),
	} {
		if _, err := ParseManagementSignal(raw); err == nil {
			t.Fatal("malformed, duplicate, divergent, or unallowlisted management response accepted")
		}
	}
}

func TestEvidenceUsesDomainSeparatedFullIdentities(t *testing.T) {
	spec := evidenceSpec(t)
	first, err := deriveEvidence(spec)
	if err != nil {
		t.Fatal(err)
	}
	second, err := deriveEvidence(spec)
	if err != nil || first != second {
		t.Fatal("evidence is not deterministic")
	}
	for _, value := range []string{first.DeploymentGeneration, first.GoldenSetSHA256, first.NodeSetSHA256, first.ProcessSetSHA256, first.ServedStampSHA256} {
		if !validSHA(value) {
			t.Fatal("evidence is not a full SHA-256 identity")
		}
	}
	changed := spec
	changed.Nodes = append([]NodeSpec(nil), spec.Nodes...)
	changed.Nodes[0].ID = "node-0"
	if after, _ := deriveEvidence(changed); after.NodeSetSHA256 == first.NodeSetSHA256 || after.ServedStampSHA256 == first.ServedStampSHA256 {
		t.Fatal("node substitution did not change joined identities")
	}
	if first.NodeSetSHA256 == first.GoldenSetSHA256 || first.GoldenSetSHA256 == first.ProcessSetSHA256 {
		t.Fatal("domain-separated identity roles collapsed")
	}
}

func TestObservationDecisionsFailClosed(t *testing.T) {
	spec := evidenceSpec(t)
	evidence, _ := deriveEvidence(spec)
	plan := Plan{Evidence: evidence, MaxObservationGeneration: 3, Metrics: spec.Metrics, Nodes: spec.Nodes, RequiredObservationGeneration: 2}
	base := Observation{AuditPresent: true, FreshnessGeneration: 2, GoldenPresent: true, ManagementTopology: ManagementTopology,
		Metrics: []MetricValue{{"fleet_nodes_expected", 3}, {"fleet_nodes_ready", 3}}, PlanSHA256: hexValue('c'),
		GoldenNodes: 3, LiveProcessPairs: 3, ProcessPresent: true, ReadyNodes: 3, RequiredNodes: 3, Schema: ObservationSchema, ServedStampSHA256: evidence.ServedStampSHA256}
	tests := []struct {
		name string
		edit func(*Observation)
		want string
	}{
		{"healthy", func(*Observation) {}, "HEALTHY"},
		{"audit-loss", func(value *Observation) { value.AuditPresent = false }, "HOLD"},
		{"golden-loss", func(value *Observation) { value.GoldenPresent = false; value.GoldenNodes = 2 }, "HOLD"},
		{"stale", func(value *Observation) { value.FreshnessGeneration = 3 }, "HOLD"},
		{"missing-node", func(value *Observation) { value.ReadyNodes = 2 }, "RECOVERY-REQUIRED"},
		{"process-loss", func(value *Observation) { value.ProcessPresent = false; value.LiveProcessPairs = 2 }, "RECOVERY-REQUIRED"},
		{"divergence", func(value *Observation) { value.ServedStampSHA256 = hexValue('d') }, "DEGRADED"},
	}
	for _, test := range tests {
		value := base
		value.Metrics = append([]MetricValue(nil), base.Metrics...)
		test.edit(&value)
		got, err := EvaluateObservation(plan, value)
		if err != nil || got != test.want {
			t.Fatalf("%s decision = %q, %v", test.name, got, err)
		}
	}
	malformed := base
	malformed.Metrics[0].Name = "tenant_label"
	if _, err := EvaluateObservation(plan, malformed); err == nil {
		t.Fatal("unallowlisted metric accepted")
	}
}

func TestOrderedAbsentOnlyStageAndNoop(t *testing.T) {
	parent := privateTemp(t)
	root := filepath.Join(parent, "node")
	files := fixtureBundle()
	bundleSHA := bundleIdentityForTest(t, files)
	noop, err := stageNode(root, files, bundleSHA)
	if err != nil || noop {
		t.Fatal("first ordered stage failed")
	}
	if actual, err := verifyNodeRoot(root); err != nil || actual != bundleSHA {
		t.Fatal("staged node did not reconstruct")
	}
	noop, err = stageNode(root, files, bundleSHA)
	if err != nil || !noop {
		t.Fatal("identical stage was not a verified no-op")
	}
	entries, _ := os.ReadDir(root)
	names := make([]string, len(entries))
	for index, entry := range entries {
		names[index] = entry.Name()
	}
	if !reflect.DeepEqual(names, []string{"base.idx", "base.jsonl", "config.json"}) {
		t.Fatal("node inventory drifted")
	}
}

func TestStageRefusesTamperExtraModeAndLinks(t *testing.T) {
	tests := []struct {
		mode   string
		target string
	}{
		{"tamper", "config.json"}, {"tamper", "base.jsonl"}, {"tamper", "base.idx"}, {"extra", "journal"},
		{"mode", "config.json"}, {"mode", "base.jsonl"}, {"mode", "base.idx"},
		{"symlink", "config.json"}, {"symlink", "base.idx"},
		{"hardlink", "config.json"}, {"hardlink", "base.idx"}, {"special", "base.idx"},
	}
	for index, test := range tests {
		t.Run(fmt.Sprintf("%02d-%s-%s", index, test.mode, test.target), func(t *testing.T) {
			parent := privateTemp(t)
			root := filepath.Join(parent, "node")
			files := fixtureBundle()
			bundleSHA := bundleIdentityForTest(t, files)
			if _, err := stageNode(root, files, bundleSHA); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(root, test.target)
			switch test.mode {
			case "tamper":
				if err := os.WriteFile(target, []byte("tampered"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "extra":
				if err := os.WriteFile(target, nil, 0o600); err != nil {
					t.Fatal(err)
				}
			case "mode":
				if err := os.Chmod(target, 0o644); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Remove(target); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("base.jsonl", target); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				if runtime.GOOS == "windows" {
					t.Skip("hard links are platform-specific")
				}
				if err := os.Remove(target); err != nil {
					t.Fatal(err)
				}
				if err := os.Link(filepath.Join(root, "base.jsonl"), target); err != nil {
					t.Fatal(err)
				}
			case "special":
				if err := os.Remove(target); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(target, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := stageNode(root, files, bundleSHA); err == nil {
				t.Fatal("mutated existing node was accepted")
			}
		})
	}
}

func TestPrivateTransactionIsAbsentOnly(t *testing.T) {
	parent := privateTemp(t)
	out := filepath.Join(parent, "out")
	files := map[string][]byte{"one.json": []byte("{}\n"), "two.json": []byte("[]\n")}
	if privateTransaction(out, files) != nil {
		t.Fatal("first private transaction failed")
	}
	if privateTransaction(out, files) == nil {
		t.Fatal("overwrite transaction accepted")
	}
	for name, raw := range files {
		if !equalRaw(filepath.Join(out, name), raw) {
			t.Fatal("private transaction bytes or mode drifted")
		}
	}
}

func TestRecoverySpecBoundaries(t *testing.T) {
	valid := RecoverySpec{EvidenceReviewerActorID: "reviewer", ExpectedServedStampSHA256: hexValue('a'), ExpectedStateSHA256: hexValue('b'),
		Mode: "RESTORE", RecoveryGeneration: 2, RecoveryOperatorActorID: "recovery", Schema: RecoverySpecSchema,
		StepDeadlineMillis: 60_000, TargetArtifactID: hexValue('c')}
	if validateRecoverySpec(valid) != nil {
		t.Fatal("valid recovery spec refused")
	}
	mutations := []func(*RecoverySpec){
		func(value *RecoverySpec) { value.Schema = "wrong" },
		func(value *RecoverySpec) { value.ExpectedStateSHA256 = "short" },
		func(value *RecoverySpec) { value.Mode = "DELETE" },
		func(value *RecoverySpec) { value.TargetArtifactID = "short" },
		func(value *RecoverySpec) { value.RecoveryGeneration = 0 },
		func(value *RecoverySpec) { value.EvidenceReviewerActorID = value.RecoveryOperatorActorID },
		func(value *RecoverySpec) { value.StepDeadlineMillis = 60_001 },
	}
	for _, mutate := range mutations {
		value := valid
		mutate(&value)
		if validateRecoverySpec(value) == nil {
			t.Fatal("invalid recovery spec accepted")
		}
	}
}

func TestResultIsPathNeutral(t *testing.T) {
	result := Result{BackupSHA256: hexValue('a'), Decision: "RECOVERED", FleetID: "fleet-a", Generation: 2,
		PlanSHA256: hexValue('b'), RecoveredNodes: 3, Schema: ResultSchema, ServedStampSHA256: hexValue('c'), Status: "RECOVERED"}
	raw, err := MarshalResult(result)
	if err != nil {
		t.Fatal(err)
	}
	for _, prohibited := range []string{"/Users/", "127.0.0.1", "token", "tenant", "query", "record-data"} {
		if strings.Contains(string(raw), prohibited) {
			t.Fatalf("path-neutral result leaked %q", prohibited)
		}
	}
	var parsed Result
	if json.Unmarshal(raw, &parsed) != nil || parsed != result {
		t.Fatal("result did not round-trip")
	}
}

func privateTemp(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func nodeForTest(t *testing.T, parent, id string) NodeSpec {
	t.Helper()
	processRoot := filepath.Join(parent, id+"-process")
	dataRoot := filepath.Join(processRoot, "data")
	if err := os.MkdirAll(dataRoot, 0o700); err != nil || os.Chmod(processRoot, 0o700) != nil || os.Chmod(dataRoot, 0o700) != nil {
		t.Fatal("private node root unavailable")
	}
	credential := filepath.Join(processRoot, "backend.credential")
	if err := os.WriteFile(credential, []byte("fixture-backend"), 0o600); err != nil {
		t.Fatal(err)
	}
	return NodeSpec{BackendCredentialFile: credential, ID: id, Root: filepath.Join(dataRoot, PhysicalListName)}
}

func fixtureBundle() []stagedFile {
	values := []stagedFile{{name: "config.json", raw: []byte("{\"schema\":\"fixture\"}\n")}, {name: "base.jsonl", raw: []byte("{\"id\":\"neutral\"}\n")}, {name: "base.idx", raw: []byte("fixture-index")}}
	for index := range values {
		values[index].sha = digest(values[index].raw)
	}
	return values
}

func bundleIdentityForTest(t *testing.T, files []stagedFile) string {
	t.Helper()
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
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func evidenceSpec(t *testing.T) FleetSpec {
	t.Helper()
	parent := privateTemp(t)
	return FleetSpec{ArtifactID: hexValue('1'), Executables: ExecutableSet{
		Kurn:        Executable{Name: "kurn", Path: filepath.Join(parent, "kurn"), SHA256: KurnSHA256, SourceRevision: KurnSourceRevision, CompatibilityLabel: KurnCompatibility},
		Kurnd:       Executable{Name: "kurnd", Path: filepath.Join(parent, "kurnd"), SHA256: KurndSHA256, SourceRevision: KurnSourceRevision, CompatibilityLabel: KurnCompatibility},
		KurnSearchd: Executable{Name: "kurn-searchd", Path: filepath.Join(parent, "searchd"), SHA256: KurnSearchdSHA256, SourceRevision: KurnSourceRevision, CompatibilityLabel: KurnCompatibility},
	}, Bundle: BundleExpectation{BaseSHA256: hexValue('4'), ConfigSHA256: hexValue('5'), Entries: 4}, FirstGeneration: true, FleetID: "fleet-a", Generation: 1,
		Goldens: []GoldenSpec{{ExpectedSHA256: NeutralGoldenSHA256, ID: NeutralGoldenID}}, MaxObservationGeneration: 3,
		Metrics:             []MetricRule{{Maximum: 3, Minimum: 3, Name: "fleet_nodes_expected"}, {Maximum: 3, Minimum: 3, Name: "fleet_nodes_ready"}},
		Nodes:               []NodeSpec{nodeForTest(t, parent, "node-a"), nodeForTest(t, parent, "node-b"), nodeForTest(t, parent, "node-c")},
		ProbeDeadlineMillis: 30_000, RequiredObservationGeneration: 2, Roles: testRoles("fleet-a"), RouteDecisionSHA256: hexValue('3'), RouteID: "route-a", Schema: SpecSchema}
}

func testRoles(scope string) []RoleAssignment {
	roles := make([]RoleAssignment, len(requiredRoles))
	for index, role := range requiredRoles {
		roles[index] = RoleAssignment{ActorID: "actor-" + role, Role: role, ScopeID: scope}
	}
	sort.Slice(roles, func(i, j int) bool { return roles[i].Role < roles[j].Role })
	return roles
}

func ownershipLedgerForTest(spec FleetSpec) OwnershipLedger {
	actors := map[string]string{}
	for _, assignment := range spec.Roles {
		actors[assignment.Role] = assignment.ActorID
	}
	approvals := make([]DecisionApproval, len(requiredDecisions))
	for index, required := range requiredDecisions {
		approvals[index] = DecisionApproval{ActorID: actors[required.role], Decision: required.decision, Generation: spec.Generation,
			PredecessorPlanSHA256: spec.PredecessorPlanSHA256, Role: required.role, ScopeID: spec.FleetID, ValidThroughGeneration: spec.Generation}
	}
	return OwnershipLedger{Approvals: approvals,
		Exceptions: []ExceptionRecord{{ExceptionID: "exception-a", ExpiresAfterGeneration: spec.Generation, OpenedGeneration: spec.Generation,
			OwnerActorID: actors["data-security-owner"], ProofGapID: "gap-operator-ownership", ScopeID: spec.FleetID, State: "RESOLVED"}},
		PostChanges: []PostChangeRecord{{ActionID: "action-a", Generation: spec.Generation, IncidentID: "incident-a",
			OwnerActorID: actors["incident-commander"], ProofGapID: "gap-observability-recovery", ScopeID: spec.FleetID, State: "CLOSED"}},
		Schema: OwnershipSchema}
}

func hexValue(value byte) string { return strings.Repeat(string(value), 64) }
