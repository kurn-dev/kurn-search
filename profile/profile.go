// Package profile defines immutable closed adapter descriptors. It validates
// identity, list, attestation, filter-vocabulary, and runtime-policy inputs but
// performs no registry, authentication, admission, retry, or backend work.
package profile

import (
	"bytes"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/kurn-dev/kurn-search/adapter"
)

// Common hard ceilings. Every accepted descriptor carries explicit nonzero
// values no greater than these constants.
const (
	HardMaxSourceBytes           = 16 << 20
	HardMaxRequestBytes          = 1 << 20
	HardMaxTrustedScopeBytes     = 64 << 10
	HardMaxProjectedEntries      = 10_000
	HardMaxKeysPerEntry          = adapter.MaxPhysicalKeys
	HardMaxPayloadBytes          = adapter.MaxOpaqueJSONBytes
	HardMaxQueryRunes            = 256
	HardMaxProbes                = 64
	HardMaxPhysicalKeyRunes      = adapter.MaxPhysicalKeyRunes
	HardMaxPool                  = adapter.MaxPool
	HardMaxPoolLadderSteps       = 4
	HardMaxResultLimit           = adapter.MaxHits
	HardMaxCandidatesPerProbe    = adapter.MaxCandidatesPerProbe
	HardMaxEvidenceCandidates    = adapter.MaxEvidenceCandidates
	HardMaxResultBytes           = 16 << 20
	HardMaxResponseBytes         = 64 << 20
	HardMaxIdentityArtifactBytes = adapter.MaxIdentityArtifactBytes
	HardMaxTimeoutMillis         = 600_000
	HardMaxInflight              = 1024
	HardMaxWorstCaseChargeBytes  = 1 << 40
	HardMaxFilterFields          = 8
	HardMaxTestedEngines         = 16
	HardMaxModules               = 5
	VersionAttemptsV1            = 2
	DefaultResultLimitV1         = 25
	DefaultPool1V1               = 128
	DefaultPool2V1               = 256
	DefaultPool3V1               = 512
	DefaultPool4V1               = 1000
	RequiredGoToolchain          = "go1.27.0"
)

const (
	AnalyzerAuthorityLabel  = "analyzer-authority"
	AnalyzerAuthoritySchema = "kurn-search/analyzer-authority/v1"
	AnalyzerOutputLabel     = "analyzer-output"
	AnalyzerOutputSchema    = "kurn-search-analyzer-neutral/v1"
)

var (
	nameRE       = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)
	versionRE    = regexp.MustCompile(`^v[1-9][0-9]{0,9}$`)
	schemaRE     = regexp.MustCompile(`^[a-z0-9][a-z0-9./-]{0,63}$`)
	listNameRE   = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	versionedRE  = regexp.MustCompile(`-v[1-9][0-9]*$`)
	filterNameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]{0,127}$`)
	engineRE     = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?$`)
	toolchainRE  = regexp.MustCompile(`^go[0-9]+\.[0-9]+\.[0-9]+$`)
	moduleRE     = regexp.MustCompile(`^[A-Za-z0-9._~/-]{1,256}$`)
)

var frozenModules = []ModuleAuthority{
	{Path: "github.com/clipperhouse/uax29/v2", Version: "v2.7.0", Sum: "h1:+gs4oBZ2gPfVrKPthwbMzWZDaAFPGYK72F0NJv2v7Vk="},
	{Path: "golang.org/x/mod", Version: "v0.36.0", Sum: "h1:JJjpVx6myfUsUdAzZuOSTTmRE0PfZeNWzzvKrP7amb4="},
	{Path: "golang.org/x/sync", Version: "v0.21.0", Sum: "h1:HLII4xRRTtCRkxYp4HNFF0Js/Og6q2i++KXbg0gHCwM="},
	{Path: "golang.org/x/text", Version: "v0.38.0", Sum: "h1:sXmwo9DwP3OK9EZ7PqAdaooSGozfl/3a6/xJcbzPRhE="},
	{Path: "golang.org/x/tools", Version: "v0.45.0", Sum: "h1:18qN3FAooORvApf5XjCXgsuayZOEtXf6JK18I3+ONa8="},
}

// FrozenModuleAuthorities returns the exact five-module operational graph
// admitted by Slice 2. The caller receives a defensive copy.
func FrozenModuleAuthorities() []ModuleAuthority {
	return append([]ModuleAuthority(nil), frozenModules...)
}

type BehaviorArtifacts struct {
	AnalyzerAuthority adapter.Component
	AnalyzerOutput    adapter.Component
	SourceProjection  adapter.Component
	QueryCodec        adapter.Component
	CompiledPlan      adapter.Component
	ResolvedResult    adapter.Component
}

type AnalyzerAuthorities struct {
	SemanticAuthoritySHA256   string `json:"semantic_authority_sha256"`
	NeutralOutputSHA256       string `json:"neutral_output_sha256"`
	FixtureCorpusSHA256       string `json:"fixture_corpus_sha256"`
	CorrectedSourceSHA256     string `json:"corrected_source_sha256"`
	OperationalIdentitySHA256 string `json:"operational_identity_sha256"`
	StdlibUnicode             string `json:"stdlib_unicode"`
	CasesUnicode              string `json:"cases_unicode"`
	NormUnicode               string `json:"norm_unicode"`
	UAX29Module               string `json:"uax29_module"`
}

type ModuleAuthority struct {
	Path    string `json:"path"`
	Version string `json:"version"`
	Sum     string `json:"sum"`
}

type PhysicalListSpec struct {
	Role                adapter.ListRole
	Name                string
	ConfigSHA256        string
	ThresholdMillionths *uint32
}

type Bounds struct {
	MaxSourceBytes           int
	MaxRequestBytes          int
	MaxTrustedScopeBytes     int
	MaxProjectedEntries      int
	MaxKeysPerEntry          int
	MaxPayloadBytes          int
	MaxQueryRunes            int
	MaxProbes                int
	MaxPhysicalKeyRunes      int
	PoolLadder               []int
	DefaultResultLimit       int
	MaxResultLimit           int
	MaxCandidatesPerProbe    int
	MaxEvidenceCandidates    int
	MaxResultBytes           int
	MaxResponseBytes         int
	MaxIdentityArtifactBytes int
	VersionAttempts          int
	TimeoutMillis            uint64
	MaxInflight              int
	WorstCaseChargeBytes     uint64
}

type Spec struct {
	Name    string
	Version string
	// Policy names an opt-in behaviour policy of the adapter ("" is the
	// default behaviour). It is part of the diagnostic identity, so a
	// descriptor built for a policy never matches a default diagnostic.
	Policy         string
	SourceSchema   string
	RequestSchema  string
	ResultSchema   string
	EvidenceKinds  []adapter.EvidenceKind
	Analyzer       AnalyzerAuthorities
	GoToolchain    string
	Modules        []ModuleAuthority
	TestedEngines  []string
	Lists          []PhysicalListSpec
	TrustedFilters []string
	CallerFilters  []string
	Bounds         Bounds
}

type Identities struct {
	Codec    adapter.Digest
	Search   adapter.Digest
	Behavior adapter.Digest
}

// Descriptor is a validated immutable closed profile.
type Descriptor struct {
	spec       Spec
	identities Identities
	diagnostic []byte
	valid      bool
}

func NewDescriptor(input Spec, artifacts BehaviorArtifacts) (Descriptor, error) {
	spec := cloneSpec(input)
	if spec.Bounds.MaxInflight == 0 {
		spec.Bounds.MaxInflight = 1
	}
	if !validateSpec(spec) || !validateArtifacts(spec, artifacts) {
		return Descriptor{}, invalidProfile()
	}
	codec, err := adapter.CodecDigest(artifacts.SourceProjection, artifacts.QueryCodec)
	if err != nil {
		return Descriptor{}, identityMismatch()
	}
	search, err := adapter.SearchDigest(artifacts.CompiledPlan, artifacts.ResolvedResult)
	if err != nil {
		return Descriptor{}, identityMismatch()
	}
	behavior, err := adapter.BehaviorDigest(
		artifacts.AnalyzerAuthority, artifacts.AnalyzerOutput, codec, search,
	)
	if err != nil {
		return Descriptor{}, identityMismatch()
	}

	sortSpec(&spec)
	identities := Identities{Codec: codec, Search: search, Behavior: behavior}
	diagnostic, err := encodeDiagnostic(spec, identities)
	if err != nil {
		return Descriptor{}, invalidProfile()
	}
	return Descriptor{spec: spec, identities: identities, diagnostic: diagnostic, valid: true}, nil
}

func (d Descriptor) Valid() bool            { return d.valid }
func (d Descriptor) Spec() Spec             { return cloneSpec(d.spec) }
func (d Descriptor) Identities() Identities { return d.identities }
func (d Descriptor) DiagnosticJSON() []byte { return bytes.Clone(d.diagnostic) }

// ValidatePlan applies the descriptor's closed evidence-kind and physical-
// list-role vocabularies to an otherwise valid pure plan.
func (d Descriptor) ValidatePlan(plan adapter.CompiledPlan) error {
	if !d.valid || !plan.Valid() || len(plan.Probes()) > d.spec.Bounds.MaxProbes {
		return invalidProfile()
	}
	kinds := make(map[adapter.EvidenceKind]struct{}, len(d.spec.EvidenceKinds))
	for _, kind := range d.spec.EvidenceKinds {
		kinds[kind] = struct{}{}
	}
	roles := make(map[adapter.ListRole]struct{}, len(d.spec.Lists))
	for _, list := range d.spec.Lists {
		roles[list.Role] = struct{}{}
	}
	for _, probe := range plan.Probes() {
		if _, ok := kinds[probe.Kind()]; !ok {
			return invalidProfile()
		}
		if _, ok := roles[probe.ListRole()]; !ok {
			return invalidProfile()
		}
		if utf8.RuneCountInString(probe.LogicalTerm()) > d.spec.Bounds.MaxQueryRunes ||
			utf8.RuneCountInString(probe.PhysicalKey()) > d.spec.Bounds.MaxPhysicalKeyRunes {
			return invalidProfile()
		}
		for _, key := range probe.PhysicalKeys() {
			if utf8.RuneCountInString(key) > d.spec.Bounds.MaxPhysicalKeyRunes {
				return invalidProfile()
			}
		}
	}
	return nil
}

// ValidateEvidence proves that one attempt contains exactly one ordered
// evidence item per planned probe and uses one declared pool rung throughout.
func (d Descriptor) ValidateEvidence(plan adapter.CompiledPlan, evidence []adapter.ProbeEvidence) error {
	if d.ValidatePlan(plan) != nil || len(evidence) != len(plan.Probes()) ||
		len(evidence) > d.spec.Bounds.MaxProbes {
		return invalidProfile()
	}
	probes := plan.Probes()
	pool := 0
	totalCandidates := 0
	for index, item := range evidence {
		if !item.Valid() || !sameProbe(probes[index], item.Probe()) ||
			!containsInt(d.spec.Bounds.PoolLadder, item.RequestedPool()) {
			return invalidProfile()
		}
		if pool == 0 {
			pool = item.RequestedPool()
		} else if item.RequestedPool() != pool {
			return invalidProfile()
		}
		if len(item.Candidates()) > d.spec.Bounds.MaxCandidatesPerProbe {
			return invalidProfile()
		}
		totalCandidates += len(item.Candidates())
		if totalCandidates > d.spec.Bounds.MaxEvidenceCandidates {
			return invalidProfile()
		}
	}
	return nil
}

func sameProbe(left, right adapter.Probe) bool { return adapter.SameProbe(left, right) }

func containsInt(values []int, candidate int) bool {
	for _, value := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

func validateArtifacts(spec Spec, artifacts BehaviorArtifacts) bool {
	components := []adapter.Component{
		artifacts.AnalyzerAuthority, artifacts.AnalyzerOutput,
		artifacts.SourceProjection, artifacts.QueryCodec,
		artifacts.CompiledPlan, artifacts.ResolvedResult,
	}
	for _, component := range components {
		if !component.Valid() || len(component.Artifact()) > spec.Bounds.MaxIdentityArtifactBytes {
			return false
		}
	}
	if artifacts.AnalyzerAuthority.Label() != AnalyzerAuthorityLabel ||
		artifacts.AnalyzerAuthority.Schema() != AnalyzerAuthoritySchema ||
		artifacts.AnalyzerOutput.Label() != AnalyzerOutputLabel ||
		artifacts.AnalyzerOutput.Schema() != AnalyzerOutputSchema {
		return false
	}
	return adapter.PlainSHA256(artifacts.AnalyzerAuthority.Artifact()).Hex() == spec.Analyzer.SemanticAuthoritySHA256 &&
		adapter.PlainSHA256(artifacts.AnalyzerOutput.Artifact()).Hex() == spec.Analyzer.NeutralOutputSHA256
}

func validateSpec(spec Spec) bool {
	if !nameRE.MatchString(spec.Name) || !versionRE.MatchString(spec.Version) ||
		(spec.Policy != "" && !nameRE.MatchString(spec.Policy)) ||
		!schemaRE.MatchString(spec.SourceSchema) || !schemaRE.MatchString(spec.RequestSchema) ||
		!schemaRE.MatchString(spec.ResultSchema) || len(spec.EvidenceKinds) < 1 ||
		len(spec.EvidenceKinds) > adapter.MaxEvidenceKinds || !validateAnalyzer(spec.Analyzer) ||
		spec.GoToolchain != RequiredGoToolchain || !toolchainRE.MatchString(spec.GoToolchain) || !validateModules(spec.Modules) ||
		!validateEngines(spec.TestedEngines) || !validateLists(spec.Lists) ||
		!validateFilters(spec.TrustedFilters, spec.CallerFilters) || !validateBounds(spec.Bounds) {
		return false
	}
	seenKinds := make(map[adapter.EvidenceKind]struct{}, len(spec.EvidenceKinds))
	for _, kind := range spec.EvidenceKinds {
		if !nameRE.MatchString(string(kind)) {
			return false
		}
		if _, exists := seenKinds[kind]; exists {
			return false
		}
		seenKinds[kind] = struct{}{}
	}
	return true
}

func validateAnalyzer(authority AnalyzerAuthorities) bool {
	for _, digest := range []string{
		authority.SemanticAuthoritySHA256, authority.NeutralOutputSHA256,
		authority.FixtureCorpusSHA256, authority.CorrectedSourceSHA256,
		authority.OperationalIdentitySHA256,
	} {
		if parsed, err := adapter.ParseDigestHex(digest); err != nil || !parsed.Valid() {
			return false
		}
	}
	return authority.StdlibUnicode == "17.0.0" && authority.CasesUnicode == "17.0.0" &&
		authority.NormUnicode == "17.0.0" && authority.UAX29Module == "github.com/clipperhouse/uax29/v2@v2.7.0"
}

func validateModules(modules []ModuleAuthority) bool {
	if len(modules) != len(frozenModules) || len(modules) > HardMaxModules {
		return false
	}
	want := make(map[string]ModuleAuthority, len(frozenModules))
	for _, module := range frozenModules {
		want[module.Path+"\x00"+module.Version] = module
	}
	seen := make(map[string]struct{}, len(modules))
	for _, module := range modules {
		if !moduleRE.MatchString(module.Path) || module.Version == "" || len(module.Version) > 64 ||
			!strings.HasPrefix(module.Sum, "h1:") || len(module.Sum) > 128 {
			return false
		}
		key := module.Path + "\x00" + module.Version
		if _, exists := seen[key]; exists {
			return false
		}
		frozen, exists := want[key]
		if !exists || frozen.Sum != module.Sum {
			return false
		}
		seen[key] = struct{}{}
	}
	return true
}

func validateEngines(versions []string) bool {
	if len(versions) < 1 || len(versions) > HardMaxTestedEngines {
		return false
	}
	seen := make(map[string]struct{}, len(versions))
	for _, version := range versions {
		if len(version) > 64 || !engineRE.MatchString(version) {
			return false
		}
		if _, exists := seen[version]; exists {
			return false
		}
		seen[version] = struct{}{}
	}
	return true
}

func validateLists(lists []PhysicalListSpec) bool {
	if len(lists) < 1 || len(lists) > adapter.MaxPhysicalListRoles {
		return false
	}
	roles := make(map[adapter.ListRole]struct{}, len(lists))
	names := make(map[string]struct{}, len(lists))
	for _, list := range lists {
		if !nameRE.MatchString(string(list.Role)) || !listNameRE.MatchString(list.Name) ||
			!versionedRE.MatchString(list.Name) {
			return false
		}
		if parsed, err := adapter.ParseDigestHex(list.ConfigSHA256); err != nil || !parsed.Valid() {
			return false
		}
		if list.ThresholdMillionths != nil && *list.ThresholdMillionths > 1_000_000 {
			return false
		}
		if _, exists := roles[list.Role]; exists {
			return false
		}
		if _, exists := names[list.Name]; exists {
			return false
		}
		roles[list.Role] = struct{}{}
		names[list.Name] = struct{}{}
	}
	return true
}

func validateFilters(trusted, caller []string) bool {
	if len(trusted)+len(caller) < 1 || len(trusted)+len(caller) > HardMaxFilterFields {
		return false
	}
	seen := make(map[string]struct{}, len(trusted)+len(caller))
	for _, vocabulary := range [][]string{trusted, caller} {
		for _, name := range vocabulary {
			if !utf8.ValidString(name) || !filterNameRE.MatchString(name) {
				return false
			}
			if _, exists := seen[name]; exists {
				return false
			}
			seen[name] = struct{}{}
		}
	}
	return true
}

func validateBounds(bounds Bounds) bool {
	if !positiveAtMost(bounds.MaxSourceBytes, HardMaxSourceBytes) ||
		!positiveAtMost(bounds.MaxRequestBytes, HardMaxRequestBytes) ||
		!positiveAtMost(bounds.MaxTrustedScopeBytes, HardMaxTrustedScopeBytes) ||
		!positiveAtMost(bounds.MaxProjectedEntries, HardMaxProjectedEntries) ||
		!positiveAtMost(bounds.MaxKeysPerEntry, HardMaxKeysPerEntry) ||
		!positiveAtMost(bounds.MaxPayloadBytes, HardMaxPayloadBytes) ||
		bounds.MaxQueryRunes != HardMaxQueryRunes || bounds.MaxProbes != HardMaxProbes ||
		bounds.MaxPhysicalKeyRunes != HardMaxPhysicalKeyRunes ||
		!positiveAtMost(bounds.DefaultResultLimit, HardMaxResultLimit) ||
		!positiveAtMost(bounds.MaxResultLimit, HardMaxResultLimit) ||
		bounds.DefaultResultLimit != DefaultResultLimitV1 || bounds.MaxResultLimit != HardMaxResultLimit ||
		!positiveAtMost(bounds.MaxCandidatesPerProbe, HardMaxCandidatesPerProbe) ||
		!positiveAtMost(bounds.MaxEvidenceCandidates, HardMaxEvidenceCandidates) ||
		bounds.MaxEvidenceCandidates < bounds.MaxCandidatesPerProbe ||
		!positiveAtMost(bounds.MaxResultBytes, HardMaxResultBytes) ||
		!positiveAtMost(bounds.MaxResponseBytes, HardMaxResponseBytes) ||
		!positiveAtMost(bounds.MaxIdentityArtifactBytes, HardMaxIdentityArtifactBytes) ||
		bounds.VersionAttempts != VersionAttemptsV1 || bounds.TimeoutMillis < 1 ||
		bounds.TimeoutMillis > HardMaxTimeoutMillis || bounds.MaxInflight < 1 ||
		bounds.MaxInflight > HardMaxInflight || bounds.WorstCaseChargeBytes < 1 ||
		bounds.WorstCaseChargeBytes > HardMaxWorstCaseChargeBytes ||
		len(bounds.PoolLadder) != 4 || len(bounds.PoolLadder) > HardMaxPoolLadderSteps {
		return false
	}
	expectedPools := [...]int{DefaultPool1V1, DefaultPool2V1, DefaultPool3V1, DefaultPool4V1}
	previous := 0
	for index, pool := range bounds.PoolLadder {
		if pool <= previous || pool < 1 || pool > HardMaxPool || pool > bounds.MaxCandidatesPerProbe {
			return false
		}
		if pool != expectedPools[index] {
			return false
		}
		previous = pool
	}
	return true
}

func positiveAtMost(value, maximum int) bool { return value > 0 && value <= maximum }

func cloneSpec(spec Spec) Spec {
	clone := spec
	clone.EvidenceKinds = append([]adapter.EvidenceKind(nil), spec.EvidenceKinds...)
	clone.Modules = append([]ModuleAuthority(nil), spec.Modules...)
	clone.TestedEngines = append([]string(nil), spec.TestedEngines...)
	clone.Lists = append([]PhysicalListSpec(nil), spec.Lists...)
	for index := range clone.Lists {
		if spec.Lists[index].ThresholdMillionths != nil {
			value := *spec.Lists[index].ThresholdMillionths
			clone.Lists[index].ThresholdMillionths = &value
		}
	}
	clone.TrustedFilters = append([]string(nil), spec.TrustedFilters...)
	clone.CallerFilters = append([]string(nil), spec.CallerFilters...)
	clone.Bounds.PoolLadder = append([]int(nil), spec.Bounds.PoolLadder...)
	return clone
}

func sortSpec(spec *Spec) {
	sort.Slice(spec.Modules, func(i, j int) bool {
		if spec.Modules[i].Path != spec.Modules[j].Path {
			return spec.Modules[i].Path < spec.Modules[j].Path
		}
		return spec.Modules[i].Version < spec.Modules[j].Version
	})
	sort.Strings(spec.TestedEngines)
	sort.Slice(spec.Lists, func(i, j int) bool { return spec.Lists[i].Role < spec.Lists[j].Role })
	sort.Strings(spec.TrustedFilters)
	sort.Strings(spec.CallerFilters)
}
