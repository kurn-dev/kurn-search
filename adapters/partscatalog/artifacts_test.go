package partscatalog

import (
	"bytes"
	"slices"
	"testing"

	"github.com/kurn-dev/kurn-search/adapter"
	"github.com/kurn-dev/kurn-search/adapters/catalogid"
)

func TestArtifactsDeterministicAndIndependent(t *testing.T) {
	first, err := GenerateArtifacts()
	if err != nil {
		t.Fatal(err)
	}
	second, err := GenerateArtifacts()
	if err != nil {
		t.Fatal(err)
	}
	old, err := catalogid.GenerateArtifacts()
	if err != nil {
		t.Fatal(err)
	}
	for name, values := range map[string][3][]byte{
		"projection": {first.SourceProjection, second.SourceProjection, old.SourceProjection},
		"codec":      {first.QueryCodec, second.QueryCodec, old.QueryCodec},
		"plan":       {first.CompiledPlan, second.CompiledPlan, old.CompiledPlan},
		"result":     {first.ResolvedResult, second.ResolvedResult, old.ResolvedResult},
	} {
		if len(values[0]) == 0 || !bytes.Equal(values[0], values[1]) || bytes.Equal(values[0], values[2]) {
			t.Fatalf("%s identity", name)
		}
	}
	if !bytes.Contains(first.QueryCodec, []byte(`"filters"`)) || !bytes.Contains(first.CompiledPlan, []byte(`"trusted"`)) {
		t.Fatal("missing metadata semantics")
	}
	copy := NeutralFixture()
	copy[0] = '!'
	if NeutralFixture()[0] == '!' {
		t.Fatal("fixture alias")
	}
}

func TestDescriptorPreservesBoundsAndSeparatesCatalog(t *testing.T) {
	config := adapter.PlainSHA256([]byte("fixture config")).Hex()
	b := OperationalBinding{PhysicalListName: "parts-catalog-v1", ConfigSHA256: config, TestedEngines: []string{"v0.6.0-1-gf7423cd"}, TimeoutMillis: 30000, MaxInflight: 1, WorstCaseChargeBytes: 1 << 30}
	d, err := NewDescriptor(b)
	if err != nil {
		t.Fatal(err)
	}
	old, err := catalogid.NewDescriptor(catalogid.OperationalBinding{PhysicalListName: b.PhysicalListName, ConfigSHA256: config, TestedEngines: b.TestedEngines, TimeoutMillis: b.TimeoutMillis, MaxInflight: b.MaxInflight, WorstCaseChargeBytes: b.WorstCaseChargeBytes})
	if err != nil {
		t.Fatal(err)
	}
	if d.Identities().Behavior.Equal(old.Identities().Behavior) {
		t.Fatal("shared recipe behavior")
	}
	s := d.Spec()
	if s.Name != "parts-catalog" || !slices.Equal(s.CallerFilters, []string{"category", "manufacturer", "partition_id", "status"}) || !slices.Equal(s.TrustedFilters, []string{"tenant_id"}) ||
		!slices.Equal(s.Bounds.PoolLadder, []int{128, 256, 512, 1000}) || s.Bounds.VersionAttempts != 2 {
		t.Fatal("descriptor vocabulary/bounds")
	}
	b.PhysicalListName = "parts-catalog-v2"
	changed, err := NewDescriptor(b)
	if err != nil || !d.Identities().Behavior.Equal(changed.Identities().Behavior) || bytes.Equal(d.DiagnosticJSON(), changed.DiagnosticJSON()) {
		t.Fatalf("operation/behavior separation: error=%v sameBehavior=%v sameDiagnostic=%v", err, d.Identities().Behavior.Equal(changed.Identities().Behavior), bytes.Equal(d.DiagnosticJSON(), changed.DiagnosticJSON()))
	}
}
