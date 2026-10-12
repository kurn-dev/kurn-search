package preparedsetup

import (
	"testing"

	"github.com/kurn-dev/kurn-search/internal/localproof"
)

func TestParseMaxInflight(t *testing.T) {
	for raw, want := range map[string]int{"1": 1, "8": 8, "12": 12, "64": 64} {
		if got, ok := parseMaxInflight(raw); !ok || got != want {
			t.Fatalf("parseMaxInflight(%q) = %d, %v", raw, got, ok)
		}
	}
	for _, raw := range []string{"", "0", "65", "99", "100", "08", "-1", "+1", "1.0", " 1", "1 ", "x", "1e1"} {
		if got, ok := parseMaxInflight(raw); ok {
			t.Fatalf("parseMaxInflight(%q) accepted %d", raw, got)
		}
	}
}

func TestParseMaxQueue(t *testing.T) {
	for raw, want := range map[string]int{"1": 1, "16": 16, "400": 400, "4096": 4096} {
		if got, ok := parseMaxQueue(raw); !ok || got != want {
			t.Fatalf("parseMaxQueue(%q) = %d, %v", raw, got, ok)
		}
	}
	for _, raw := range []string{"", "0", "4097", "10000", "016", "-1", "+1", "1.0", " 1", "x"} {
		if got, ok := parseMaxQueue(raw); ok {
			t.Fatalf("parseMaxQueue(%q) accepted %d", raw, got)
		}
	}
	m := testValidManifest(t)
	for _, n := range []int{0, 1, 16, 4096} {
		m.Operational.MaxQueue = n
		if err := validateManifest(m); err != nil {
			t.Fatalf("max_queue %d refused: %v", n, err)
		}
	}
	for _, n := range []int{-1, 4097} {
		m.Operational.MaxQueue = n
		if validateManifest(m) == nil {
			t.Fatalf("max_queue %d accepted", n)
		}
	}
}

// Engine pairs: a new preparation binds the current label; a manifest from a
// version bound to the previously admitted label still validates against a
// descriptor rebuilt with that label; an unknown label is refused by the
// descriptor builder and by manifest validation.
func TestManifestEngineLabelsAdmittedPairsOnly(t *testing.T) {
	previous := localproof.EngineAuthorities[1].Label
	m := testValidManifest(t)
	if m.Operational.TestedEngine != localproof.KurnCompatibilityLabel {
		t.Fatalf("test manifest binds %q, not the current label", m.Operational.TestedEngine)
	}
	old, err := descriptorFor(m.Operational.MaxInflight, m.Profile.Policy, previous)
	if err != nil {
		t.Fatal(err)
	}
	existing := m
	existing.Operational.TestedEngine = previous
	existing.Profile.DiagnosticSHA256 = bytesSHA(old.DiagnosticJSON())
	if err := validateManifest(existing); err != nil {
		t.Fatalf("existing version with the previous engine label refused: %v", err)
	}
	mixed := m
	mixed.Operational.TestedEngine = previous
	if validateManifest(mixed) == nil {
		t.Fatal("previous label accepted with the current-label diagnostic")
	}
	if _, err := descriptorFor(1, "", "v0.0.0-unknown"); err == nil {
		t.Fatal("unknown engine label built a descriptor")
	}
	unknown := m
	unknown.Operational.TestedEngine = "v0.0.0-unknown"
	if validateManifest(unknown) == nil {
		t.Fatal("unknown engine label accepted")
	}
}

// A manifest declaring a non-default admission limit validates only with the
// matching profile diagnostic; the default-limit diagnostic is refused for it.
func TestManifestMaxInflightBoundToDiagnostic(t *testing.T) {
	m := testValidManifest(t)
	d, err := descriptorFor(8, "", localproof.KurnCompatibilityLabel)
	if err != nil {
		t.Fatal(err)
	}
	m.Operational.MaxInflight = 8
	if validateManifest(m) == nil {
		t.Fatal("max_inflight 8 accepted with the max_inflight 1 diagnostic")
	}
	m.Profile.DiagnosticSHA256 = bytesSHA(d.DiagnosticJSON())
	if err := validateManifest(m); err != nil {
		t.Fatalf("consistent max_inflight 8 refused: %v", err)
	}
	for _, n := range []int{0, 65} {
		bad := m
		bad.Operational.MaxInflight = n
		if validateManifest(bad) == nil {
			t.Fatalf("max_inflight %d accepted", n)
		}
	}
}
