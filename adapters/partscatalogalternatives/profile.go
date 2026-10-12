package partscatalogalternatives

import (
	"github.com/kurn-dev/kurn-search/adapter"
	"github.com/kurn-dev/kurn-search/adapter/analyzer"
	"github.com/kurn-dev/kurn-search/adapters/partscatalogfacets"
	"github.com/kurn-dev/kurn-search/profile"
)

// OperationalBinding retains the delivered physical-list and budget contract.
type OperationalBinding = partscatalogfacets.OperationalBinding

// This is the delivered discover analyzer-authority artifact, byte for byte.
const analyzerAuthorityArtifact = "{\n  \"corrected_source_sha256\": \"127d2b43363b2950b3b7a995020b3c01c8920922d64dfbf9987ef27c261869d0\",\n  \"fixture_corpus_sha256\": \"4ef6225e892460897fab706fcf295c1f73eac5ff9a1f148a22ac66051d256fef\",\n  \"schema\": \"kurn-search/analyzer-authority/v1\",\n  \"unicode\": {\n    \"cases\": \"17.0.0\",\n    \"norm\": \"17.0.0\",\n    \"stdlib\": \"17.0.0\",\n    \"uax29\": \"github.com/clipperhouse/uax29/v2@v2.7.0\"\n  }\n}\n"

func NewDescriptor(binding OperationalBinding) (profile.Descriptor, error) {
	base, err := partscatalogfacets.NewDescriptor(binding)
	if err != nil {
		return profile.Descriptor{}, err
	}
	artifacts, err := GenerateArtifacts()
	if err != nil {
		return profile.Descriptor{}, err
	}
	component := func(label, schema string, value []byte) (adapter.Component, error) {
		return adapter.NewComponent(adapter.ComponentSpec{Label: label, Schema: schema, Artifact: value})
	}
	authority, err := component(profile.AnalyzerAuthorityLabel, profile.AnalyzerAuthoritySchema, []byte(analyzerAuthorityArtifact))
	if err != nil {
		return profile.Descriptor{}, err
	}
	output, err := component(profile.AnalyzerOutputLabel, profile.AnalyzerOutputSchema, analyzer.NeutralGolden())
	if err != nil {
		return profile.Descriptor{}, err
	}
	source, err := component("source-projection", "kurn-search/parts-catalog-discover-projection/v1", artifacts.SourceProjection)
	if err != nil {
		return profile.Descriptor{}, err
	}
	query, err := component("query-codec", "kurn-search/parts-catalog-discover-query-codec/v1", artifacts.QueryCodec)
	if err != nil {
		return profile.Descriptor{}, err
	}
	compiled, err := component("compiled-plan", "kurn-search/parts-catalog-discover-compiled-plan/v1", artifacts.CompiledPlan)
	if err != nil {
		return profile.Descriptor{}, err
	}
	resolved, err := component("resolved-result", "kurn-search/parts-discover-alternatives-resolved-result/v1", artifacts.ResolvedResult)
	if err != nil {
		return profile.Descriptor{}, err
	}
	spec := base.Spec()
	spec.Name = "parts-catalog-discover-facet-alternatives"
	spec.ResultSchema = "kurn-search/parts-catalog-discover-facet-alternatives-result/v1"
	return profile.NewDescriptor(spec, profile.BehaviorArtifacts{
		AnalyzerAuthority: authority, AnalyzerOutput: output, SourceProjection: source,
		QueryCodec: query, CompiledPlan: compiled, ResolvedResult: resolved,
	})
}
