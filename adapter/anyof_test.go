package adapter

import (
	"strings"
	"testing"
)

func TestAnyOfProbeContract(t *testing.T) {
	probe, err := NewProbe(ProbeSpec{Kind: "transposition", LogicalTerm: "XA-100", PhysicalKeys: []string{"AX-100", "XA-010"}, ListRole: "identifiers"})
	if err != nil {
		t.Fatal(err)
	}
	if probe.PhysicalKey() != "" || len(probe.PhysicalKeys()) != 2 || !probe.Valid() {
		t.Fatalf("probe: %+v", probe)
	}
	keys := probe.PhysicalKeys()
	keys[0] = "mutated"
	if probe.PhysicalKeys()[0] != "AX-100" {
		t.Fatal("PhysicalKeys returned the backing slice")
	}
	same, _ := NewProbe(ProbeSpec{Kind: "transposition", LogicalTerm: "XA-100", PhysicalKeys: []string{"AX-100", "XA-010"}, ListRole: "identifiers"})
	reordered, _ := NewProbe(ProbeSpec{Kind: "transposition", LogicalTerm: "XA-100", PhysicalKeys: []string{"XA-010", "AX-100"}, ListRole: "identifiers"})
	if !SameProbe(probe, same) || SameProbe(probe, reordered) {
		t.Fatal("SameProbe ignores key order or identity")
	}
	if _, err := NewCompiledPlan([]Probe{probe, reordered}); err != nil {
		t.Fatal("distinct any-of probes refused in one plan")
	}
	if _, err := NewCompiledPlan([]Probe{probe, same}); err == nil {
		t.Fatal("duplicate any-of probes accepted in one plan")
	}
	long := make([]string, MaxAnyOfKeys+1)
	for i := range long {
		long[i] = strings.Repeat("k", i+1)
	}
	for name, spec := range map[string]ProbeSpec{
		"both key forms": {Kind: "k", LogicalTerm: "q", PhysicalKey: "a", PhysicalKeys: []string{"b"}, ListRole: "r"},
		"empty key":      {Kind: "k", LogicalTerm: "q", PhysicalKeys: []string{"a", ""}, ListRole: "r"},
		"duplicate key":  {Kind: "k", LogicalTerm: "q", PhysicalKeys: []string{"a", "a"}, ListRole: "r"},
		"too many keys":  {Kind: "k", LogicalTerm: "q", PhysicalKeys: long, ListRole: "r"},
		"long key":       {Kind: "k", LogicalTerm: "q", PhysicalKeys: []string{strings.Repeat("k", MaxPhysicalKeyRunes+1)}, ListRole: "r"},
	} {
		if _, err := NewProbe(spec); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
