package profile

import (
	"bytes"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/kurn-dev/kurn-search/adapter"
)

const (
	fixtureCorpusSHA = "4ef6225e892460897fab706fcf295c1f73eac5ff9a1f148a22ac66051d256fef"
	correctedSource  = "127d2b43363b2950b3b7a995020b3c01c8920922d64dfbf9987ef27c261869d0"
	operationalSHA   = "e729a51817550164438841fafc650086e41ec86f43d3af13d266319ee6e6f285"
)

func TestDescriptorDeterminismImmutabilityAndOperationalSplit(t *testing.T) {
	spec, artifacts := neutralInputs(t)
	descriptor, err := NewDescriptor(spec, artifacts)
	if err != nil {
		t.Fatal(err)
	}
	if !descriptor.Valid() || descriptor.Spec().Bounds.MaxInflight != 1 {
		t.Fatal("valid descriptor did not apply the explicit v1 admission default")
	}

	permuted := cloneSpec(spec)
	reverseModules(permuted.Modules)
	reverseStrings(permuted.TestedEngines)
	reverseLists(permuted.Lists)
	reverseStrings(permuted.TrustedFilters)
	reverseStrings(permuted.CallerFilters)
	equivalent, err := NewDescriptor(permuted, artifacts)
	if err != nil || !bytes.Equal(descriptor.DiagnosticJSON(), equivalent.DiagnosticJSON()) {
		t.Fatalf("equivalent descriptor input was not deterministic: %v", err)
	}

	callerCopy := descriptor.Spec()
	callerCopy.Modules[0].Path = "changed"
	callerCopy.Bounds.PoolLadder[0] = 999
	diagnostic := descriptor.DiagnosticJSON()
	diagnostic[0] = 'X'
	if descriptor.Spec().Modules[0].Path == "changed" || descriptor.Spec().Bounds.PoolLadder[0] != DefaultPool1V1 ||
		descriptor.DiagnosticJSON()[0] != '{' {
		t.Fatal("descriptor exposed mutable state")
	}

	operational := cloneSpec(spec)
	operational.TestedEngines = append(operational.TestedEngines, "v0.5.4")
	operational.Lists[0].Name = "neutral-primary-v2"
	changedOperational, err := NewDescriptor(operational, artifacts)
	if err != nil {
		t.Fatal(err)
	}
	if !descriptor.Identities().Behavior.Equal(changedOperational.Identities().Behavior) ||
		bytes.Equal(descriptor.DiagnosticJSON(), changedOperational.DiagnosticJSON()) {
		t.Fatal("operational movement did not preserve behavior and move descriptor bytes")
	}

	semanticArtifacts := artifacts
	semanticArtifacts.QueryCodec = mustComponent(t, "query-codec", "neutral/query/v1", []byte("changed query codec\n"))
	changedSemantic, err := NewDescriptor(spec, semanticArtifacts)
	if err != nil {
		t.Fatal(err)
	}
	if descriptor.Identities().Codec.Equal(changedSemantic.Identities().Codec) ||
		descriptor.Identities().Behavior.Equal(changedSemantic.Identities().Behavior) {
		t.Fatal("semantic movement did not move codec and overall behavior identities")
	}
}

func TestDiagnosticStrictRoundTripAndRefusals(t *testing.T) {
	spec, artifacts := neutralInputs(t)
	descriptor, err := NewDescriptor(spec, artifacts)
	if err != nil {
		t.Fatal(err)
	}
	raw := descriptor.DiagnosticJSON()
	parsed, err := ParseDiagnostic(raw, artifacts)
	if err != nil || !bytes.Equal(parsed.DiagnosticJSON(), raw) {
		t.Fatalf("strict diagnostic round trip: %v", err)
	}

	mutatedDigest := bytes.Replace(raw, []byte(descriptor.Identities().Behavior.Hex()), []byte(strings.Repeat("0", 64)), 1)
	requireProfileCategory(t, ParseDiagnosticError(mutatedDigest, artifacts), CategoryIdentityMismatch)

	unknown := append(bytes.Clone(raw[:len(raw)-2]), []byte(",\n  \"unknown\": true\n}\n")...)
	requireProfileCategory(t, ParseDiagnosticError(unknown, artifacts), CategoryInvalidProfile)
	duplicate := append(bytes.Clone(raw[:len(raw)-2]), []byte(",\n  \"name\": \"duplicate\"\n}\n")...)
	requireProfileCategory(t, ParseDiagnosticError(duplicate, artifacts), CategoryInvalidProfile)
	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err != nil {
		t.Fatal(err)
	}
	requireProfileCategory(t, ParseDiagnosticError(compact.Bytes(), artifacts), CategoryInvalidProfile)
	requireProfileCategory(t, ParseDiagnosticError([]byte{0xff}, artifacts), CategoryInvalidProfile)
}

func TestDescriptorPlanAndEvidenceClosure(t *testing.T) {
	spec, artifacts := neutralInputs(t)
	descriptor, err := NewDescriptor(spec, artifacts)
	if err != nil {
		t.Fatal(err)
	}
	exact := mustProbe(t, "exact", "lot", "lot", "primary")
	word := mustProbe(t, "word", "lot", "lot", "word")
	plan, err := adapter.NewCompiledPlan([]adapter.Probe{exact, word})
	if err != nil || descriptor.ValidatePlan(plan) != nil {
		t.Fatalf("declared plan: %v", err)
	}
	unknownKind := mustProbe(t, "suffix", "lot", "tol", "primary")
	unknownPlan, _ := adapter.NewCompiledPlan([]adapter.Probe{unknownKind})
	if descriptor.ValidatePlan(unknownPlan) == nil {
		t.Fatal("undeclared evidence kind accepted")
	}
	unknownRole := mustProbe(t, "exact", "lot", "lot", "suffix")
	unknownRolePlan, _ := adapter.NewCompiledPlan([]adapter.Probe{unknownRole})
	if descriptor.ValidatePlan(unknownRolePlan) == nil {
		t.Fatal("undeclared list role accepted")
	}

	exactEvidence := mustEvidence(t, exact, DefaultPool1V1)
	wordEvidence := mustEvidence(t, word, DefaultPool1V1)
	if err := descriptor.ValidateEvidence(plan, []adapter.ProbeEvidence{exactEvidence, wordEvidence}); err != nil {
		t.Fatalf("declared attempt evidence: %v", err)
	}
	wrongPool := mustEvidence(t, word, DefaultPool2V1)
	if descriptor.ValidateEvidence(plan, []adapter.ProbeEvidence{exactEvidence, wrongPool}) == nil {
		t.Fatal("mixed pool attempt accepted")
	}
	if descriptor.ValidateEvidence(plan, []adapter.ProbeEvidence{wordEvidence, exactEvidence}) == nil {
		t.Fatal("evidence reordered away from requested probes")
	}
	undeclaredPool := mustEvidence(t, word, 129)
	if descriptor.ValidateEvidence(plan, []adapter.ProbeEvidence{exactEvidence, undeclaredPool}) == nil {
		t.Fatal("undeclared pool rung accepted")
	}
}

func TestProfileClosureHostileInputs(t *testing.T) {
	base, artifacts := neutralInputs(t)
	tests := []struct {
		name   string
		mutate func(*Spec)
	}{
		{"trusted-caller-overlap", func(spec *Spec) { spec.CallerFilters = []string{"factoryId"} }},
		{"trusted-duplicate", func(spec *Spec) { spec.TrustedFilters = []string{"factoryId", "factoryId"} }},
		{"unversioned-list", func(spec *Spec) { spec.Lists[0].Name = "neutral-primary" }},
		{"uppercase-config", func(spec *Spec) { spec.Lists[0].ConfigSHA256 = strings.ToUpper(spec.Lists[0].ConfigSHA256) }},
		{"duplicate-list-role", func(spec *Spec) { spec.Lists[1].Role = spec.Lists[0].Role }},
		{"unknown-toolchain", func(spec *Spec) { spec.GoToolchain = "go1.27.1" }},
		{"module-sum-drift", func(spec *Spec) { spec.Modules[0].Sum = "h1:changed" }},
		{"duplicate-engine", func(spec *Spec) { spec.TestedEngines = append(spec.TestedEngines, spec.TestedEngines[0]) }},
		{"unknown-evidence-kind", func(spec *Spec) { spec.EvidenceKinds[0] = "Exact" }},
		{"query-bound-drift", func(spec *Spec) { spec.Bounds.MaxQueryRunes-- }},
		{"probe-bound-drift", func(spec *Spec) { spec.Bounds.MaxProbes-- }},
		{"pool-ladder-drift", func(spec *Spec) { spec.Bounds.PoolLadder[0]++ }},
		{"result-default-drift", func(spec *Spec) { spec.Bounds.DefaultResultLimit++ }},
		{"version-attempts-drift", func(spec *Spec) { spec.Bounds.VersionAttempts++ }},
		{"omitted-bound", func(spec *Spec) { spec.Bounds.MaxSourceBytes = 0 }},
		{"artifact-bound-enforced", func(spec *Spec) { spec.Bounds.MaxIdentityArtifactBytes = 1 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			spec := cloneSpec(base)
			test.mutate(&spec)
			_, err := NewDescriptor(spec, artifacts)
			requireProfileCategory(t, err, CategoryInvalidProfile)
		})
	}
}

func TestProfileErrorsDoNotEchoInputsOrDigests(t *testing.T) {
	secret := "PRIVATE-profile-secret"
	spec, artifacts := neutralInputs(t)
	spec.Name = secret
	err := descriptorError(spec, artifacts)
	if err == nil || strings.Contains(err.Error(), secret) || err.Error() != "profile: invalid_profile" {
		t.Fatalf("unsafe profile error: %v", err)
	}

	spec, artifacts = neutralInputs(t)
	descriptor, err := NewDescriptor(spec, artifacts)
	if err != nil {
		t.Fatal(err)
	}
	digest := descriptor.Identities().Behavior.Hex()
	mutated := bytes.Replace(descriptor.DiagnosticJSON(), []byte(digest), []byte(strings.Repeat("0", 64)), 1)
	err = ParseDiagnosticError(mutated, artifacts)
	if err == nil || strings.Contains(err.Error(), digest) || err.Error() != "profile: identity_mismatch" {
		t.Fatalf("unsafe identity error: %v", err)
	}
}

func TestNamedHardCeilingsAtEdgeAndOneOver(t *testing.T) {
	base, artifacts := neutralInputs(t)
	tests := []struct {
		name string
		edge func(*Spec)
		over func(*Spec)
	}{
		{"source-bytes", func(s *Spec) { s.Bounds.MaxSourceBytes = HardMaxSourceBytes }, func(s *Spec) { s.Bounds.MaxSourceBytes = HardMaxSourceBytes + 1 }},
		{"request-bytes", func(s *Spec) { s.Bounds.MaxRequestBytes = HardMaxRequestBytes }, func(s *Spec) { s.Bounds.MaxRequestBytes = HardMaxRequestBytes + 1 }},
		{"trusted-scope-bytes", func(s *Spec) { s.Bounds.MaxTrustedScopeBytes = HardMaxTrustedScopeBytes }, func(s *Spec) { s.Bounds.MaxTrustedScopeBytes = HardMaxTrustedScopeBytes + 1 }},
		{"projected-entries", func(s *Spec) { s.Bounds.MaxProjectedEntries = HardMaxProjectedEntries }, func(s *Spec) { s.Bounds.MaxProjectedEntries = HardMaxProjectedEntries + 1 }},
		{"keys-per-entry", func(s *Spec) { s.Bounds.MaxKeysPerEntry = HardMaxKeysPerEntry }, func(s *Spec) { s.Bounds.MaxKeysPerEntry = HardMaxKeysPerEntry + 1 }},
		{"payload-bytes", func(s *Spec) { s.Bounds.MaxPayloadBytes = HardMaxPayloadBytes }, func(s *Spec) { s.Bounds.MaxPayloadBytes = HardMaxPayloadBytes + 1 }},
		{"result-bytes", func(s *Spec) { s.Bounds.MaxResultBytes = HardMaxResultBytes }, func(s *Spec) { s.Bounds.MaxResultBytes = HardMaxResultBytes + 1 }},
		{"response-bytes", func(s *Spec) { s.Bounds.MaxResponseBytes = HardMaxResponseBytes }, func(s *Spec) { s.Bounds.MaxResponseBytes = HardMaxResponseBytes + 1 }},
		{"identity-artifact-bytes", func(s *Spec) { s.Bounds.MaxIdentityArtifactBytes = HardMaxIdentityArtifactBytes }, func(s *Spec) { s.Bounds.MaxIdentityArtifactBytes = HardMaxIdentityArtifactBytes + 1 }},
		{"timeout", func(s *Spec) { s.Bounds.TimeoutMillis = HardMaxTimeoutMillis }, func(s *Spec) { s.Bounds.TimeoutMillis = HardMaxTimeoutMillis + 1 }},
		{"inflight", func(s *Spec) { s.Bounds.MaxInflight = HardMaxInflight }, func(s *Spec) { s.Bounds.MaxInflight = HardMaxInflight + 1 }},
		{"worst-case-charge", func(s *Spec) { s.Bounds.WorstCaseChargeBytes = HardMaxWorstCaseChargeBytes }, func(s *Spec) { s.Bounds.WorstCaseChargeBytes = HardMaxWorstCaseChargeBytes + 1 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			edge := cloneSpec(base)
			test.edge(&edge)
			if _, err := NewDescriptor(edge, artifacts); err != nil {
				t.Fatalf("named ceiling edge refused: %v", err)
			}
			over := cloneSpec(base)
			test.over(&over)
			requireProfileCategory(t, descriptorError(over, artifacts), CategoryInvalidProfile)
		})
	}
}

func TestFrozenVocabularyAndPolicyEdges(t *testing.T) {
	base, artifacts := neutralInputs(t)

	edge := cloneSpec(base)
	edge.EvidenceKinds = make([]adapter.EvidenceKind, adapter.MaxEvidenceKinds)
	edge.Lists = make([]PhysicalListSpec, adapter.MaxPhysicalListRoles)
	for index := range edge.EvidenceKinds {
		edge.EvidenceKinds[index] = adapter.EvidenceKind("kind-" + strconv.Itoa(index))
		edge.Lists[index] = PhysicalListSpec{
			Role: adapter.ListRole("role-" + strconv.Itoa(index)), Name: "list-" + strconv.Itoa(index) + "-v1",
			ConfigSHA256: adapter.PlainSHA256([]byte("config-" + strconv.Itoa(index))).Hex(),
		}
	}
	edge.TrustedFilters = []string{"t0", "t1", "t2", "t3"}
	edge.CallerFilters = []string{"c0", "c1", "c2", "c3"}
	edge.TestedEngines = make([]string, HardMaxTestedEngines)
	for index := range edge.TestedEngines {
		edge.TestedEngines[index] = "v0.5." + strconv.Itoa(index)
	}
	if _, err := NewDescriptor(edge, artifacts); err != nil {
		t.Fatalf("closed-vocabulary edges: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*Spec)
	}{
		{"evidence-kinds", func(s *Spec) { s.EvidenceKinds = append(s.EvidenceKinds, "one-over") }},
		{"physical-list-roles", func(s *Spec) {
			s.Lists = append(s.Lists, PhysicalListSpec{Role: "one-over", Name: "one-over-v1", ConfigSHA256: strings.Repeat("0", 64)})
		}},
		{"filter-fields", func(s *Spec) { s.CallerFilters = append(s.CallerFilters, "c4") }},
		{"tested-engines", func(s *Spec) { s.TestedEngines = append(s.TestedEngines, "v0.5.16") }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			over := cloneSpec(edge)
			test.mutate(&over)
			requireProfileCategory(t, descriptorError(over, artifacts), CategoryInvalidProfile)
		})
	}

	policyOver := []struct {
		name   string
		mutate func(*Spec)
	}{
		{"query-runes", func(s *Spec) { s.Bounds.MaxQueryRunes = HardMaxQueryRunes + 1 }},
		{"probes", func(s *Spec) { s.Bounds.MaxProbes = HardMaxProbes + 1 }},
		{"physical-key-runes", func(s *Spec) { s.Bounds.MaxPhysicalKeyRunes = HardMaxPhysicalKeyRunes + 1 }},
		{"pool", func(s *Spec) { s.Bounds.PoolLadder[3] = HardMaxPool + 1 }},
		{"result-limit", func(s *Spec) { s.Bounds.MaxResultLimit = HardMaxResultLimit + 1 }},
		{"candidates-per-probe", func(s *Spec) { s.Bounds.MaxCandidatesPerProbe = HardMaxCandidatesPerProbe + 1 }},
		{"evidence-candidates", func(s *Spec) { s.Bounds.MaxEvidenceCandidates = HardMaxEvidenceCandidates + 1 }},
	}
	for _, test := range policyOver {
		t.Run(test.name, func(t *testing.T) {
			over := cloneSpec(base)
			test.mutate(&over)
			requireProfileCategory(t, descriptorError(over, artifacts), CategoryInvalidProfile)
		})
	}

	long := mustProbe(t, "exact", strings.Repeat("q", HardMaxQueryRunes+1), "key", "primary")
	plan, _ := adapter.NewCompiledPlan([]adapter.Probe{long})
	descriptor, _ := NewDescriptor(base, artifacts)
	if descriptor.ValidatePlan(plan) == nil {
		t.Fatal("plan above the descriptor query-rune bound accepted")
	}
}

func neutralInputs(t *testing.T) (Spec, BehaviorArtifacts) {
	t.Helper()
	authorityBytes := []byte("neutral semantic analyzer authority\n")
	outputBytes := []byte("neutral analyzer output\n")
	artifacts := BehaviorArtifacts{
		AnalyzerAuthority: mustComponent(t, AnalyzerAuthorityLabel, AnalyzerAuthoritySchema, authorityBytes),
		AnalyzerOutput:    mustComponent(t, AnalyzerOutputLabel, AnalyzerOutputSchema, outputBytes),
		SourceProjection:  mustComponent(t, "source-projection", "neutral/source/v1", []byte("source projection\n")),
		QueryCodec:        mustComponent(t, "query-codec", "neutral/query/v1", []byte("query codec\n")),
		CompiledPlan:      mustComponent(t, "compiled-plan", "neutral/compiled/v1", []byte("compiled plan\n")),
		ResolvedResult:    mustComponent(t, "resolved-result", "neutral/resolved/v1", []byte("resolved result\n")),
	}
	configA := adapter.PlainSHA256([]byte("primary config\n")).Hex()
	configB := adapter.PlainSHA256([]byte("word config\n")).Hex()
	spec := Spec{
		Name: "neutral-search", Version: "v1", SourceSchema: "neutral/source/v1",
		RequestSchema: "neutral/request/v1", ResultSchema: "neutral/result/v1",
		EvidenceKinds: []adapter.EvidenceKind{"exact", "word"},
		Analyzer: AnalyzerAuthorities{
			SemanticAuthoritySHA256: adapter.PlainSHA256(authorityBytes).Hex(),
			NeutralOutputSHA256:     adapter.PlainSHA256(outputBytes).Hex(),
			FixtureCorpusSHA256:     fixtureCorpusSHA, CorrectedSourceSHA256: correctedSource,
			OperationalIdentitySHA256: operationalSHA, StdlibUnicode: "17.0.0",
			CasesUnicode: "17.0.0", NormUnicode: "17.0.0",
			UAX29Module: "github.com/clipperhouse/uax29/v2@v2.7.0",
		},
		GoToolchain: RequiredGoToolchain, Modules: FrozenModuleAuthorities(),
		TestedEngines: []string{"v0.5.3", "v0.5.2"},
		Lists: []PhysicalListSpec{
			{Role: "primary", Name: "neutral-primary-v1", ConfigSHA256: configA},
			{Role: "word", Name: "neutral-word-v1", ConfigSHA256: configB},
		},
		TrustedFilters: []string{"factoryId"}, CallerFilters: []string{"variantId"},
		Bounds: Bounds{
			MaxSourceBytes: HardMaxSourceBytes, MaxRequestBytes: HardMaxRequestBytes,
			MaxTrustedScopeBytes: HardMaxTrustedScopeBytes, MaxProjectedEntries: HardMaxProjectedEntries,
			MaxKeysPerEntry: HardMaxKeysPerEntry, MaxPayloadBytes: HardMaxPayloadBytes,
			MaxQueryRunes: HardMaxQueryRunes, MaxProbes: HardMaxProbes,
			MaxPhysicalKeyRunes: HardMaxPhysicalKeyRunes,
			PoolLadder:          []int{DefaultPool1V1, DefaultPool2V1, DefaultPool3V1, DefaultPool4V1},
			DefaultResultLimit:  DefaultResultLimitV1, MaxResultLimit: HardMaxResultLimit,
			MaxCandidatesPerProbe: HardMaxCandidatesPerProbe,
			MaxEvidenceCandidates: HardMaxEvidenceCandidates,
			MaxResultBytes:        HardMaxResultBytes, MaxResponseBytes: HardMaxResponseBytes,
			MaxIdentityArtifactBytes: HardMaxIdentityArtifactBytes,
			VersionAttempts:          VersionAttemptsV1, TimeoutMillis: 30_000,
			MaxInflight: 0, WorstCaseChargeBytes: HardMaxWorstCaseChargeBytes,
		},
	}
	return spec, artifacts
}

func mustComponent(t *testing.T, label, schema string, artifact []byte) adapter.Component {
	t.Helper()
	component, err := adapter.NewComponent(adapter.ComponentSpec{Label: label, Schema: schema, Artifact: artifact})
	if err != nil {
		t.Fatal(err)
	}
	return component
}

func mustProbe(t *testing.T, kind, term, key, role string) adapter.Probe {
	t.Helper()
	probe, err := adapter.NewProbe(adapter.ProbeSpec{
		Kind: adapter.EvidenceKind(kind), LogicalTerm: term, PhysicalKey: key, ListRole: adapter.ListRole(role),
	})
	if err != nil {
		t.Fatal(err)
	}
	return probe
}

func mustEvidence(t *testing.T, probe adapter.Probe, pool int) adapter.ProbeEvidence {
	t.Helper()
	evidence, err := adapter.NewProbeEvidence(adapter.ProbeEvidenceSpec{Probe: probe, RequestedPool: pool})
	if err != nil {
		t.Fatal(err)
	}
	return evidence
}

func ParseDiagnosticError(raw []byte, artifacts BehaviorArtifacts) error {
	_, err := ParseDiagnostic(raw, artifacts)
	return err
}

func descriptorError(spec Spec, artifacts BehaviorArtifacts) error {
	_, err := NewDescriptor(spec, artifacts)
	return err
}

func requireProfileCategory(t *testing.T, err error, category Category) {
	t.Helper()
	var profileErr *Error
	if !errors.As(err, &profileErr) || profileErr.Category != category {
		t.Fatalf("error category got %v want %s", err, category)
	}
}

func reverseModules(values []ModuleAuthority) {
	for left, right := 0, len(values)-1; left < right; left, right = left+1, right-1 {
		values[left], values[right] = values[right], values[left]
	}
}

func reverseStrings(values []string) {
	for left, right := 0, len(values)-1; left < right; left, right = left+1, right-1 {
		values[left], values[right] = values[right], values[left]
	}
}

func reverseLists(values []PhysicalListSpec) {
	for left, right := 0, len(values)-1; left < right; left, right = left+1, right-1 {
		values[left], values[right] = values[right], values[left]
	}
}
