package partscatalogfacets

import (
	"encoding/json"
	"slices"
	"testing"
)

// TestFrozenOracle executes every frozen oracle query through the delegating
// resolution and compares identifiers, order and the complete summary.
func TestFrozenOracle(t *testing.T) {
	fixture, err := loadOracle()
	if err != nil {
		t.Fatal(err)
	}
	entries, err := exampleEntries()
	if err != nil {
		t.Fatal(err)
	}
	semantics := New()
	for _, query := range fixture.Queries {
		ids, summary, _, err := resolveOracle(semantics, entries, query)
		if err != nil {
			t.Errorf("%s: %v", query.ID, err)
			continue
		}
		expectedSpec, err := query.ExpectedSummary.expected()
		if err != nil {
			t.Errorf("%s: %v", query.ID, err)
			continue
		}
		if !slices.Equal(ids, query.ExpectedIDs) {
			t.Errorf("%s: identifier sequence mismatch", query.ID)
		}
		if !sameSummary(summary, expectedSpec) {
			t.Errorf("%s: summary mismatch", query.ID)
		}
	}
}

// TestGenerateArtifacts proves the descriptor artifact generation reproduces
// and is deterministic.
func TestGenerateArtifacts(t *testing.T) {
	first, err := GenerateArtifacts()
	if err != nil {
		t.Fatal(err)
	}
	second, err := GenerateArtifacts()
	if err != nil {
		t.Fatal(err)
	}
	if len(first.ResolvedResult) == 0 {
		t.Fatal("empty resolved-result artifact")
	}
	if !slices.Equal(first.ResolvedResult, second.ResolvedResult) {
		t.Fatal("artifact generation is not deterministic")
	}
	var document map[string]any
	if err := json.Unmarshal(first.ResolvedResult, &document); err != nil {
		t.Fatal(err)
	}
	answers, ok := document["answers"].([]any)
	if !ok || len(answers) != fixtureQueryCount {
		t.Fatalf("artifact census mismatch: %v", document["answers"])
	}
}
