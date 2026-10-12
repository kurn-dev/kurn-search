package partscatalogfacets

import (
	"fmt"
	"slices"
	"testing"

	"github.com/kurn-dev/kurn-search/adapter"
	"github.com/kurn-dev/kurn-search/adapters/partscatalogdiscover"
	"github.com/kurn-dev/kurn-search/internal/jsjson"
)

func TestDelegationPlanIdentity(t *testing.T) {
	for _, raw := range oracleRequests(t) {
		facetPlan, err := New().Compile(raw, mustScope(t))
		if err != nil {
			t.Fatalf("facet compile refused: %v", err)
		}
		delegatePlan, err := partscatalogdiscover.New().Compile(raw, mustScope(t))
		if err != nil {
			t.Fatalf("delegate compile refused: %v", err)
		}
		if !slices.Equal(planKeys(facetPlan), planKeys(delegatePlan)) {
			t.Fatalf("plan divergence on %s", raw)
		}
	}
}

// TestSummaryAtCeilingSentinel proves the 1000-hit ambiguity refuses with the
// typed sentinel: two probes each complete below the pool (999 < 1000) whose
// disjoint entry union reaches the hits ceiling.
func TestSummaryAtCeilingSentinel(t *testing.T) {
	semantics := New()
	request := []byte(`{"query":"xa","limit":5}`)
	plan, err := semantics.Compile(request, mustScope(t))
	if err != nil {
		t.Fatal(err)
	}
	evidence := make([]adapter.ProbeEvidence, 0, len(plan.Probes()))
	family := 0
	for _, probe := range plan.Probes() {
		var candidates []adapter.Candidate
		if string(probe.Kind()) == "identifier-word" || string(probe.Kind()) == "name-word" {
			family++
			for index := 0; index < 999; index++ {
				candidates = append(candidates, synthesizeCandidate(t, probe, family, index))
			}
		}
		item, err := adapter.NewProbeEvidence(adapter.ProbeEvidenceSpec{
			Probe: probe, RequestedPool: 1000, Candidates: candidates,
		})
		if err != nil {
			t.Fatal(err)
		}
		evidence = append(evidence, item)
	}
	resolution, summary, err := semantics.ResolveWithSummary(request, mustScope(t), plan, evidence)
	if err == nil || !adapter.IsSummaryAtCeiling(err) {
		t.Fatalf("expected the ceiling sentinel, got resolution=%v summary=%v err=%v", resolution.Valid(), summary.Valid(), err)
	}
	if resolution.Valid() || summary.Valid() {
		t.Fatal("ceiling refusal leaked a partial resolution or summary")
	}
}

// synthesizeCandidate builds one valid canonical payload whose projection
// contains the given probe's physical key, distinct per family and index.
func synthesizeCandidate(t *testing.T, probe adapter.Probe, family, index int) adapter.Candidate {
	t.Helper()
	var identifier, name string
	switch string(probe.Kind()) {
	case "identifier-word":
		identifier, name = "XA", fmt.Sprintf("alpha %d", index)
	case "name-word":
		identifier, name = fmt.Sprintf("ZZ-%d", index), "xa thing"
	default:
		t.Fatalf("unexpected probe kind %s", probe.Kind())
	}
	payload, err := jsjson.Encode(jsjson.Object{
		"category": "gasket", "id": fmt.Sprintf("SYN-%d-%d", family, index),
		"identifier": identifier, "manufacturer": "Halden", "name": name,
		"status": "active", "tenant_id": "tenant-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	projected, err := partscatalogdiscover.New().Project(payload)
	if err != nil || len(projected) != 1 {
		t.Fatal(err)
	}
	if !slices.Contains(projected[0].Keys(), probe.PhysicalKey()) {
		t.Fatalf("synthesized payload lacks probe key %s", probe.PhysicalKey())
	}
	candidate, err := adapter.NewCandidate(adapter.CandidateSpec{
		EntryID: projected[0].EntryID(), Score: 100, EchoedKey: probe.PhysicalKey(), Payload: payload,
	})
	if err != nil {
		t.Fatal(err)
	}
	return candidate
}

// TestZeroMatchSummary proves the explicit zero-match shape: complete
// resolution, zero hits, total zero and three empty groups.
func TestZeroMatchSummary(t *testing.T) {
	semantics := New()
	request := []byte(`{"query":"sprocket","limit":5}`)
	plan, err := semantics.Compile(request, mustScope(t))
	if err != nil {
		t.Fatal(err)
	}
	evidence := make([]adapter.ProbeEvidence, 0, len(plan.Probes()))
	for _, probe := range plan.Probes() {
		item, err := adapter.NewProbeEvidence(adapter.ProbeEvidenceSpec{
			Probe: probe, RequestedPool: 128, Candidates: nil,
		})
		if err != nil {
			t.Fatal(err)
		}
		evidence = append(evidence, item)
	}
	resolution, summary, err := semantics.ResolveWithSummary(request, mustScope(t), plan, evidence)
	if err != nil {
		t.Fatal(err)
	}
	if len(resolution.Hits()) != 0 || summary.TotalMatches() != 0 {
		t.Fatal("zero-match census mismatch")
	}
	groups := summary.Groups()
	if len(groups) != 3 || groups[0].Name() != "category" || groups[1].Name() != "manufacturer" || groups[2].Name() != "status" {
		t.Fatalf("group order/names: %v", groups)
	}
	for _, group := range groups {
		if len(group.Buckets()) != 0 {
			t.Fatalf("zero-match group %s carries buckets", group.Name())
		}
	}
}

// TestHitsPrefixInvariant proves the response hits are the complete matched
// sequence's prefix of length min(limit, total).
func TestHitsPrefixInvariant(t *testing.T) {
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
		if query.ID != "D03" {
			continue
		}
		fullIDs, fullSummary, _, err := resolveOracle(semantics, entries, query)
		if err != nil {
			t.Fatal(err)
		}
		limited := query
		limited.Request.Limit = 2
		limitedIDs, limitedSummary, _, err := resolveOracle(semantics, entries, limited)
		if err != nil {
			t.Fatal(err)
		}
		if limitedSummary.TotalMatches() != fullSummary.TotalMatches() {
			t.Fatal("limit changed the total")
		}
		if !slices.Equal(limitedIDs, fullIDs[:2]) {
			t.Fatalf("hits are not the prefix: %v vs %v", limitedIDs, fullIDs[:2])
		}
	}
}
