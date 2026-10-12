package partscatalogsuggest

import (
	"bytes"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/kurn-dev/kurn-search/adapter"
)

func TestTranspositions(t *testing.T) {
	for query, want := range map[string][]string{
		"AX-100": {"XA-100", "AX-010"},
		"Z":      nil,
		"aa":     nil,
		"a b":    nil,
		"ab":     {"ba"},
		"1234":   {"2134", "1324", "1243"},
		"GX-021": {"XG-021", "GX-201", "GX-012"},
		"Ab-Cd1": {"bA-Cd1", "Ab-dC1", "Ab-C1d"},
		"  xy  ": {"  yx  "},
		"ß1":     {"1ß"},
		"aé":     {"éa"},
	} {
		if got := transpositions(query); !slices.Equal(got, want) {
			t.Errorf("%q: %q, want %q", query, got, want)
		}
	}
	long := "abcdefghijklmnopqrstuvwxyz0123456789"
	if got := transpositions(long); len(got) != maxTranspositions || got[0] != "bacdefghijklmnopqrstuvwxyz0123456789" {
		t.Fatalf("cap: %d variants, first %q", len(got), got[0])
	}
}

func fallbackPlan(t *testing.T, request []byte, first []adapter.Candidate) (adapter.CompiledPlan, adapter.CompiledPlan, []adapter.ProbeEvidence) {
	t.Helper()
	plan, err := New().Compile(request, testScope)
	if err != nil {
		t.Fatal(err)
	}
	evidence := testEvidence(t, plan, 128, first...)
	fallback, taken, err := New().CompileFallback(request, testScope, plan, evidence)
	if err != nil {
		t.Fatal(err)
	}
	if !taken {
		return plan, adapter.CompiledPlan{}, evidence
	}
	return plan, fallback, evidence
}

func TestCompileFallback(t *testing.T) {
	request := []byte(`{"query":"XA-100","limit":25}`)
	plan, fallback, _ := fallbackPlan(t, request, nil)
	probes := fallback.Probes()
	if !fallback.Valid() || len(probes) != 1 || probes[0].Kind() != kindTransposition || probes[0].LogicalTerm() != "XA-100" ||
		probes[0].ListRole() != listRole || probes[0].PhysicalKey() != "" || !slices.Equal(probes[0].PhysicalKeys(), []string{"AX-100", "XA-010"}) {
		t.Fatalf("fallback plan: %+v", probes)
	}
	again, _, _ := fallbackPlan(t, request, nil)
	if !samePlan(plan, again) {
		t.Fatal("compile is not deterministic")
	}

	// Not taken: the similarity step had a candidate, or the query has no
	// variants.
	hit := testCandidate(t, "PC-006", 78, "AX-101", []byte(`{"id":"PC-006","identifier":"AX-101","manufacturer":"Northstar","category":"bearing","status":"active","tenant_id":"tenant-a"}`))
	if _, fb, _ := fallbackPlan(t, request, []adapter.Candidate{hit}); fb.Valid() {
		t.Fatal("fallback taken beside a similarity candidate")
	}
	if _, fb, _ := fallbackPlan(t, []byte(`{"query":"Z","limit":25}`), nil); fb.Valid() {
		t.Fatal("fallback taken for a query without variants")
	}

	// A plan or evidence that is not this adapter's is refused.
	other, err := New().Compile([]byte(`{"query":"AX-100","limit":25}`), testScope)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := New().CompileFallback(request, testScope, other, testEvidence(t, other, 128)); err == nil {
		t.Fatal("foreign plan accepted")
	}
	if _, _, err := New().CompileFallback(request, testScope, plan, nil); err == nil {
		t.Fatal("missing evidence accepted")
	}
}

func TestResolveTranspositionStep(t *testing.T) {
	request := []byte(`{"query":"XA-100","limit":25}`)
	plan, fallback, evidence := fallbackPlan(t, request, nil)
	combined, err := adapter.NewCompiledPlan(append(plan.Probes(), fallback.Probes()...))
	if err != nil {
		t.Fatal(err)
	}
	record := func(id, identifier string) []byte {
		return []byte(`{"id":"` + id + `","identifier":"` + identifier + `","manufacturer":"Northstar","category":"bearing","status":"active","tenant_id":"tenant-a"}`)
	}
	second := func(candidates ...adapter.Candidate) []adapter.ProbeEvidence {
		item, err := adapter.NewProbeEvidence(adapter.ProbeEvidenceSpec{Probe: fallback.Probes()[0], RequestedPool: 128, Candidates: candidates})
		if err != nil {
			t.Fatal(err)
		}
		return append(append([]adapter.ProbeEvidence(nil), evidence...), item)
	}
	hits := second(
		testCandidate(t, "PC-002", 100, "AX-100", record("PC-002", "AX-100")),
		testCandidate(t, "PC-001", 100, "AX-100", record("PC-001", "AX-100")),
		testCandidate(t, "PC-020", 100, "xa-010", record("PC-020", "xa-010")),
	)
	result, err := New().Resolve(request, testScope, combined, hits)
	if err != nil || !result.Complete() || len(result.Hits()) != 3 {
		t.Fatalf("resolve: %v %+v", err, result)
	}
	// Identifier order, then ID: AX-100 (PC-001, PC-002), then xa-010.
	var ids []string
	for _, hit := range result.Hits() {
		ids = append(ids, hit.EntryID())
		if !bytes.Contains(hit.Evidence(), []byte(`"transposition": "yes"`)) || !bytes.Contains(hit.Evidence(), []byte(`"normalized_exact": "no"`)) ||
			!bytes.Contains(hit.Result(), []byte(`"score": 100`)) {
			t.Fatalf("evidence %s result %s", hit.Evidence(), hit.Result())
		}
	}
	if !slices.Equal(ids, []string{"PC-001", "PC-002", "PC-020"}) {
		t.Fatalf("order: %v", ids)
	}
	// An empty fallback step resolves to no hits.
	if result, err := New().Resolve(request, testScope, combined, second()); err != nil || len(result.Hits()) != 0 {
		t.Fatalf("empty fallback: %v %+v", err, result)
	}
	// Similarity hits never carry the member.
	similarity, err := New().Compile([]byte(`{"query":"AX-100","limit":25}`), testScope)
	if err != nil {
		t.Fatal(err)
	}
	result, err = New().Resolve([]byte(`{"query":"AX-100","limit":25}`), testScope, similarity,
		testEvidence(t, similarity, 128, testCandidate(t, "PC-001", 100, "AX-100", record("PC-001", "AX-100"))))
	if err != nil || len(result.Hits()) != 1 || bytes.Contains(result.Hits()[0].Evidence(), []byte("transposition")) {
		t.Fatalf("similarity evidence: %v %s", err, result.Hits()[0].Evidence())
	}

	for name, observed := range map[string][]adapter.ProbeEvidence{
		"not a variant":          second(testCandidate(t, "PC-006", 100, "AX-101", record("PC-006", "AX-101"))),
		"score below 100":        second(testCandidate(t, "PC-001", 93, "AX-100", record("PC-001", "AX-100"))),
		"identifier is query":    second(testCandidate(t, "PC-030", 100, "XA-100", record("PC-030", "XA-100"))),
		"other tenant":           second(testCandidate(t, "PC-005", 100, "AX-100", bytes.Replace(record("PC-005", "AX-100"), []byte("tenant-a"), []byte("tenant-b"), 1))),
		"first step not empty":   append([]adapter.ProbeEvidence{testEvidence(t, plan, 128, testCandidate(t, "PC-006", 78, "AX-101", record("PC-006", "AX-101")))[0]}, second()[1]),
		"first step other pool":  append([]adapter.ProbeEvidence{testEvidence(t, plan, 256)[0]}, second()[1]),
		"fallback evidence only": second()[1:],
	} {
		t.Run(name, func(t *testing.T) {
			_, err := New().Resolve(request, testScope, combined, observed)
			var refusal *Error
			if !errors.As(err, &refusal) || (refusal.Category != CategoryInvalidEvidence && refusal.Category != CategoryInvalidPlan) {
				t.Fatalf("got %v", err)
			}
		})
	}
	// A two-probe plan whose second probe is not the recomputed fallback.
	foreign, _ := adapter.NewProbe(adapter.ProbeSpec{Kind: kindTransposition, LogicalTerm: "XA-100", PhysicalKeys: []string{"AX-100"}, ListRole: listRole})
	wrong, _ := adapter.NewCompiledPlan([]adapter.Probe{plan.Probes()[0], foreign})
	item, _ := adapter.NewProbeEvidence(adapter.ProbeEvidenceSpec{Probe: foreign, RequestedPool: 128})
	if _, err := New().Resolve(request, testScope, wrong, append(append([]adapter.ProbeEvidence(nil), evidence...), item)); err == nil || !strings.Contains(err.Error(), "invalid_plan") {
		t.Fatalf("foreign fallback plan: %v", err)
	}
}
