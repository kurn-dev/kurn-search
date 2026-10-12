package catalogid

import (
	"bytes"
	"testing"

	"github.com/kurn-dev/kurn-search/adapter"
)

func TestNeutralArtifactsAreByteDeterministic(t *testing.T) {
	t.Parallel()
	first, err := GenerateArtifacts()
	if err != nil {
		t.Fatal(err)
	}
	second, err := GenerateArtifacts()
	if err != nil {
		t.Fatal(err)
	}
	for name, pair := range map[string][2][]byte{
		"source-projection": {first.SourceProjection, second.SourceProjection},
		"query-codec":       {first.QueryCodec, second.QueryCodec},
		"compiled-plan":     {first.CompiledPlan, second.CompiledPlan},
		"resolved-result":   {first.ResolvedResult, second.ResolvedResult},
	} {
		if len(pair[0]) == 0 || !bytes.Equal(pair[0], pair[1]) {
			t.Fatalf("%s generation is not byte-identical", name)
		}
	}
}

func TestDescriptorBindsSemanticsButSeparatesOperations(t *testing.T) {
	t.Parallel()
	config := adapter.PlainSHA256([]byte("resolved neutral config\n")).Hex()
	binding := OperationalBinding{
		PhysicalListName: "catalog-identifiers-v1", ConfigSHA256: config,
		TestedEngines: []string{"v0.5.3"}, TimeoutMillis: 30_000,
		MaxInflight: 1, WorstCaseChargeBytes: 1 << 30,
	}
	first, err := NewDescriptor(binding)
	if err != nil {
		t.Fatal(err)
	}
	changed := binding
	changed.PhysicalListName = "catalog-identifiers-v2"
	second, err := NewDescriptor(changed)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Identities().Behavior.Equal(second.Identities().Behavior) {
		t.Fatal("operational-only change moved behavior identity")
	}
	if bytes.Equal(first.DiagnosticJSON(), second.DiagnosticJSON()) {
		t.Fatal("operational-only change did not move descriptor bytes")
	}
	if first.Spec().TrustedFilters[0] != "tenant_id" || first.Spec().CallerFilters[0] != "partition_id" {
		t.Fatal("descriptor filter vocabularies are not closed")
	}
	semantics := New()
	request := []byte(`{"query":"abc"}`)
	scope := []byte(`{"tenant_id":"tenant-1"}`)
	plan, err := semantics.Compile(request, scope)
	if err != nil || first.ValidatePlan(plan) != nil {
		t.Fatal("descriptor rejected its adapter plan")
	}
	fixture, _ := loadFixture()
	entries := make([]adapter.ProjectedEntry, 0, len(fixture.Records))
	for _, record := range fixture.Records {
		raw, _ := encodeFixtureRecord(record)
		projected, projectErr := semantics.Project(raw)
		if projectErr != nil {
			t.Fatal(projectErr)
		}
		entries = append(entries, projected[0])
	}
	fixtureQuery := fixtureQuery{Request: fixtureRequest{Query: "abc"}, Scope: fixtureScope{TenantID: "tenant-1"}}
	evidence, err := synthesizeEvidence(entries, plan, fixtureQuery, 128)
	if err != nil || first.ValidateEvidence(plan, evidence) != nil {
		t.Fatal("descriptor rejected valid adapter evidence")
	}
}
