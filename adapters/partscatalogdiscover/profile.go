package partscatalogdiscover

import (
	"github.com/kurn-dev/kurn-search/adapter"
	"github.com/kurn-dev/kurn-search/adapter/analyzer"
	"github.com/kurn-dev/kurn-search/profile"
)

const (
	analyzerFixtureCorpusSHA256       = "4ef6225e892460897fab706fcf295c1f73eac5ff9a1f148a22ac66051d256fef"
	correctedAnalyzerSourceSHA256     = "127d2b43363b2950b3b7a995020b3c01c8920922d64dfbf9987ef27c261869d0"
	analyzerOperationalIdentitySHA256 = "e729a51817550164438841fafc650086e41ec86f43d3af13d266319ee6e6f285"
	analyzerAuthorityArtifact         = "{\n  \"corrected_source_sha256\": \"127d2b43363b2950b3b7a995020b3c01c8920922d64dfbf9987ef27c261869d0\",\n  \"fixture_corpus_sha256\": \"4ef6225e892460897fab706fcf295c1f73eac5ff9a1f148a22ac66051d256fef\",\n  \"schema\": \"kurn-search/analyzer-authority/v1\",\n  \"unicode\": {\n    \"cases\": \"17.0.0\",\n    \"norm\": \"17.0.0\",\n    \"stdlib\": \"17.0.0\",\n    \"uax29\": \"github.com/clipperhouse/uax29/v2@v2.7.0\"\n  }\n}\n"
)

// OperationalBinding supplies deployment identity and bounded policy without
// changing the adapter's pure behavioral identity.
type OperationalBinding struct {
	PhysicalListName     string
	ConfigSHA256         string
	TestedEngines        []string
	TimeoutMillis        uint64
	MaxInflight          int
	WorstCaseChargeBytes uint64
}

// NewDescriptor binds the fixed parts-catalog-discover semantics to explicit
// operational deployment evidence.
func NewDescriptor(binding OperationalBinding) (profile.Descriptor, error) {
	if binding.PhysicalListName == "" || binding.ConfigSHA256 == "" || len(binding.TestedEngines) == 0 ||
		binding.TimeoutMillis == 0 || binding.MaxInflight < 1 || binding.WorstCaseChargeBytes == 0 {
		return profile.Descriptor{}, invalidInput()
	}
	artifacts, err := GenerateArtifacts()
	if err != nil {
		return profile.Descriptor{}, invalidInput()
	}
	component := func(label, schema string, value []byte) (adapter.Component, error) {
		return adapter.NewComponent(adapter.ComponentSpec{Label: label, Schema: schema, Artifact: value})
	}
	authority, err := component(profile.AnalyzerAuthorityLabel, profile.AnalyzerAuthoritySchema, []byte(analyzerAuthorityArtifact))
	if err != nil {
		return profile.Descriptor{}, invalidInput()
	}
	neutralOutput := analyzer.NeutralGolden()
	output, err := component(profile.AnalyzerOutputLabel, profile.AnalyzerOutputSchema, neutralOutput)
	if err != nil {
		return profile.Descriptor{}, invalidInput()
	}
	source, err := component("source-projection", "kurn-search/parts-catalog-discover-projection/v1", artifacts.SourceProjection)
	if err != nil {
		return profile.Descriptor{}, invalidInput()
	}
	query, err := component("query-codec", "kurn-search/parts-catalog-discover-query-codec/v1", artifacts.QueryCodec)
	if err != nil {
		return profile.Descriptor{}, invalidInput()
	}
	compiled, err := component("compiled-plan", "kurn-search/parts-catalog-discover-compiled-plan/v1", artifacts.CompiledPlan)
	if err != nil {
		return profile.Descriptor{}, invalidInput()
	}
	resolved, err := component("resolved-result", "kurn-search/parts-catalog-discover-resolved-result/v1", artifacts.ResolvedResult)
	if err != nil {
		return profile.Descriptor{}, invalidInput()
	}

	neutralOutputDigest := adapter.PlainSHA256(neutralOutput).Hex()
	spec := profile.Spec{
		Name: "parts-catalog-discover", Version: "v1",
		SourceSchema:  "kurn-search/parts-catalog-discover-source/v1",
		RequestSchema: "kurn-search/parts-catalog-discover-request/v1",
		ResultSchema:  "kurn-search/parts-catalog-discover-result/v1",
		EvidenceKinds: []adapter.EvidenceKind{kindExact, kindIdentifierPrefix, kindIdentifierWord, kindNamePrefix, kindNameWord},
		Analyzer: profile.AnalyzerAuthorities{
			SemanticAuthoritySHA256:   adapter.PlainSHA256([]byte(analyzerAuthorityArtifact)).Hex(),
			NeutralOutputSHA256:       neutralOutputDigest,
			FixtureCorpusSHA256:       analyzerFixtureCorpusSHA256,
			CorrectedSourceSHA256:     correctedAnalyzerSourceSHA256,
			OperationalIdentitySHA256: analyzerOperationalIdentitySHA256,
			StdlibUnicode:             "17.0.0", CasesUnicode: "17.0.0", NormUnicode: "17.0.0",
			UAX29Module: "github.com/clipperhouse/uax29/v2@v2.7.0",
		},
		GoToolchain:   profile.RequiredGoToolchain,
		Modules:       profile.FrozenModuleAuthorities(),
		TestedEngines: append([]string(nil), binding.TestedEngines...),
		Lists: []profile.PhysicalListSpec{{
			Role: listRole, Name: binding.PhysicalListName, ConfigSHA256: binding.ConfigSHA256,
		}},
		TrustedFilters: []string{"tenant_id"}, CallerFilters: []string{"category", "manufacturer", "partition_id", "status"},
		Bounds: profile.Bounds{
			MaxSourceBytes: maxSourceBytes, MaxRequestBytes: maxRequestBytes,
			MaxTrustedScopeBytes: maxTrustedScopeBytes, MaxProjectedEntries: 1,
			MaxKeysPerEntry: adapter.MaxPhysicalKeys, MaxPayloadBytes: adapter.MaxOpaqueJSONBytes,
			MaxQueryRunes: profile.HardMaxQueryRunes, MaxProbes: profile.HardMaxProbes,
			MaxPhysicalKeyRunes: profile.HardMaxPhysicalKeyRunes,
			PoolLadder:          []int{profile.DefaultPool1V1, profile.DefaultPool2V1, profile.DefaultPool3V1, profile.DefaultPool4V1},
			DefaultResultLimit:  profile.DefaultResultLimitV1, MaxResultLimit: profile.HardMaxResultLimit,
			MaxCandidatesPerProbe: profile.HardMaxCandidatesPerProbe,
			MaxEvidenceCandidates: profile.HardMaxEvidenceCandidates,
			MaxResultBytes:        profile.HardMaxResultBytes, MaxResponseBytes: profile.HardMaxResponseBytes,
			MaxIdentityArtifactBytes: profile.HardMaxIdentityArtifactBytes,
			VersionAttempts:          profile.VersionAttemptsV1, TimeoutMillis: binding.TimeoutMillis,
			MaxInflight: binding.MaxInflight, WorstCaseChargeBytes: binding.WorstCaseChargeBytes,
		},
	}
	return profile.NewDescriptor(spec, profile.BehaviorArtifacts{
		AnalyzerAuthority: authority, AnalyzerOutput: output,
		SourceProjection: source, QueryCodec: query,
		CompiledPlan: compiled, ResolvedResult: resolved,
	})
}
