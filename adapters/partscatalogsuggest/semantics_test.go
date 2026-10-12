package partscatalogsuggest

import (
	"bytes"
	"errors"
	"fmt"
	"testing"

	"github.com/kurn-dev/kurn-search/adapter"
)

func testCandidate(t *testing.T, entryID string, score float64, echoedKey string, payload []byte) adapter.Candidate {
	t.Helper()
	candidate, err := adapter.NewCandidate(adapter.CandidateSpec{
		EntryID: entryID, Score: score, EchoedKey: echoedKey, Payload: payload,
	})
	if err != nil {
		t.Fatal(err)
	}
	return candidate
}

func testEvidence(t *testing.T, plan adapter.CompiledPlan, pool int, candidates ...adapter.Candidate) []adapter.ProbeEvidence {
	t.Helper()
	evidence, err := adapter.NewProbeEvidence(adapter.ProbeEvidenceSpec{
		Probe: plan.Probes()[0], RequestedPool: pool, Candidates: candidates,
	})
	if err != nil {
		t.Fatal(err)
	}
	return []adapter.ProbeEvidence{evidence}
}

func requireInvalidEvidence(t *testing.T, err error) {
	t.Helper()
	var refusal *Error
	if !errors.As(err, &refusal) || refusal.Category != CategoryInvalidEvidence {
		t.Fatalf("wanted invalid evidence, got %v", err)
	}
}

func TestResolveValidatesEverySimilarityCandidateBeforeLimit(t *testing.T) {
	request := []byte(`{"query":"AX-100","partition_id":"north","limit":1,"filters":{"manufacturer":["Northstar","Halden"],"category":["bearing","filter"],"status":"active"}}`)
	plan, err := New().Compile(request, testScope)
	if err != nil {
		t.Fatal(err)
	}
	first := []byte(`{"id":"part-1","identifier":"AX-100","manufacturer":"Northstar","category":"bearing","status":"active","tenant_id":"tenant-a","partition_id":"north"}`)
	second := []byte(`{"id":"part-2","identifier":"AX-101","manufacturer":"Halden","category":"filter","status":"active","tenant_id":"tenant-a","partition_id":"north"}`)
	valid := func(secondPayload []byte) []adapter.ProbeEvidence {
		return testEvidence(t, plan, 128,
			testCandidate(t, "part-1", 100, "AX-100", first),
			testCandidate(t, "part-2", 78, "AX-101", secondPayload),
		)
	}
	result, err := New().Resolve(request, testScope, plan, valid(second))
	if err != nil || !result.Complete() || len(result.Hits()) != 1 {
		t.Fatal("valid evidence", err)
	}
	record, err := parseSource(first)
	if err != nil {
		t.Fatal(err)
	}
	want, err := canonicalResult(record, 100)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(result.Hits()[0].Result(), want) {
		t.Fatalf("result = %s", result.Hits()[0].Result())
	}

	for _, change := range [][2]string{
		{"tenant-a", "tenant-b"}, {"north", "south"}, {"Halden", "Vega"},
		{"filter", "gasket"}, {"active", "discontinued"}, {"part-2", "part-3"},
		{"Halden", " Halden"},
	} {
		t.Run(change[0]+"-"+change[1], func(t *testing.T) {
			bad := bytes.Replace(second, []byte(change[0]), []byte(change[1]), 1)
			_, err := New().Resolve(request, testScope, plan, valid(bad))
			requireInvalidEvidence(t, err)
		})
	}

	t.Run("key-mismatch", func(t *testing.T) {
		_, err := New().Resolve(request, testScope, plan, testEvidence(t, plan, 128,
			testCandidate(t, "part-1", 100, "AX-999", first)))
		requireInvalidEvidence(t, err)
	})
	for _, score := range []float64{59, 100.5, 101} {
		t.Run(fmt.Sprintf("score-%g", score), func(t *testing.T) {
			_, err := New().Resolve(request, testScope, plan, testEvidence(t, plan, 128,
				testCandidate(t, "part-2", score, "AX-101", second)))
			requireInvalidEvidence(t, err)
		})
	}
	t.Run("exact-requires-100", func(t *testing.T) {
		_, err := New().Resolve(request, testScope, plan, testEvidence(t, plan, 128,
			testCandidate(t, "part-1", 99, "AX-100", first)))
		requireInvalidEvidence(t, err)
	})
	t.Run("duplicate-id", func(t *testing.T) {
		candidate := testCandidate(t, "part-1", 100, "AX-100", first)
		_, err := New().Resolve(request, testScope, plan, testEvidence(t, plan, 128, candidate, candidate))
		requireInvalidEvidence(t, err)
	})
}

func TestResolveRanksScoreExactIdentifierThenEntryID(t *testing.T) {
	request := []byte(`{"query":" ax-100 ","limit":10}`)
	plan, err := New().Compile(request, testScope)
	if err != nil {
		t.Fatal(err)
	}
	payload := func(id, identifier string) []byte {
		return []byte(fmt.Sprintf(`{"id":%q,"identifier":%q,"manufacturer":"Northstar","category":"bearing","status":"active","tenant_id":"tenant-a"}`, id, identifier))
	}
	candidates := []adapter.Candidate{
		testCandidate(t, "id-c", 100, "BX-100", payload("id-c", "BX-100")),
		testCandidate(t, "id-b", 100, "ax-100", payload("id-b", "ax-100")),
		testCandidate(t, "id-a", 100, "AX-100", payload("id-a", "AX-100")),
		testCandidate(t, "id-z", 99, "AA-100", payload("id-z", "AA-100")),
	}
	result, err := New().Resolve(request, testScope, plan, testEvidence(t, plan, 128, candidates...))
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(result.Hits()))
	for _, hit := range result.Hits() {
		got = append(got, hit.EntryID())
	}
	want := []string{"id-a", "id-b", "id-c", "id-z"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("rank = %v", got)
	}
}

func TestSaturationDoesNotMaskInvalidCandidate(t *testing.T) {
	request := []byte(`{"query":"AX","limit":1}`)
	plan, err := New().Compile(request, testScope)
	if err != nil {
		t.Fatal(err)
	}
	candidates := make([]adapter.Candidate, 128)
	for i := range candidates {
		id := fmt.Sprintf("part-%03d", i)
		identifier := fmt.Sprintf("AX-%03d", i)
		payload := []byte(fmt.Sprintf(`{"id":%q,"identifier":%q,"manufacturer":"Northstar","category":"bearing","status":"active","tenant_id":"tenant-a"}`, id, identifier))
		candidates[i] = testCandidate(t, id, 100, identifier, payload)
	}
	_, err = New().Resolve(request, testScope, plan, testEvidence(t, plan, 128, candidates...))
	var refusal *Error
	if !errors.As(err, &refusal) || refusal.Category != CategoryIncomplete {
		t.Fatalf("wanted incomplete, got %v", err)
	}
	last := candidates[len(candidates)-1]
	bad := bytes.Replace(last.Payload(), []byte("tenant-a"), []byte("tenant-b"), 1)
	candidates[len(candidates)-1] = testCandidate(t, last.EntryID(), last.Score(), last.EchoedKey(), bad)
	_, err = New().Resolve(request, testScope, plan, testEvidence(t, plan, 128, candidates...))
	requireInvalidEvidence(t, err)
}

func TestFinalPoolRungRefusesOneThousandCandidates(t *testing.T) {
	request := []byte(`{"query":"AX","limit":1}`)
	plan, err := New().Compile(request, testScope)
	if err != nil {
		t.Fatal(err)
	}
	candidates := make([]adapter.Candidate, 1000)
	for i := range candidates {
		id := fmt.Sprintf("part-%04d", i)
		identifier := fmt.Sprintf("AX-%04d", i)
		payload := []byte(fmt.Sprintf(`{"id":%q,"identifier":%q,"manufacturer":"Northstar","category":"bearing","status":"active","tenant_id":"tenant-a"}`, id, identifier))
		candidates[i] = testCandidate(t, id, 100, identifier, payload)
	}
	_, err = New().Resolve(request, testScope, plan, testEvidence(t, plan, 1000, candidates...))
	var refusal *Error
	if !errors.As(err, &refusal) || refusal.Category != CategoryIncomplete {
		t.Fatalf("wanted final-rung incomplete refusal, got %v", err)
	}
}

func TestSimilarityPlanAndDescriptor(t *testing.T) {
	plain := []byte(`{"query":"AX-100","limit":25}`)
	filtered := []byte(`{"query":"AX-100","filters":{"manufacturer":["Northstar","Halden"]},"limit":25}`)
	a, err := New().Compile(plain, testScope)
	if err != nil {
		t.Fatal(err)
	}
	b, err := New().Compile(filtered, testScope)
	if err != nil {
		t.Fatal(err)
	}
	if !samePlan(a, b) || len(a.Probes()) != 1 {
		t.Fatal("membership changed similarity plan")
	}
	probe := a.Probes()[0]
	if probe.Kind() != kindSimilarity || probe.ListRole() != listRole ||
		probe.LogicalTerm() != "AX-100" || probe.PhysicalKey() != "AX-100" {
		t.Fatal("similarity probe")
	}
	if _, ok := any(New()).(adapter.FilterCompiler); ok {
		t.Fatal("suggest adapter acquired legacy exact filter compiler")
	}

	binding := OperationalBinding{
		PhysicalListName: "parts-catalog-suggest-v1", ConfigSHA256: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		TestedEngines: []string{"v0.6.0-1-gf7423cd"}, TimeoutMillis: 10000,
		MaxInflight: 1, WorstCaseChargeBytes: 1,
	}
	descriptor, err := NewDescriptor(binding)
	if err != nil {
		t.Fatal(err)
	}
	copyDescriptor, err := NewDescriptor(binding)
	if err != nil {
		t.Fatal(err)
	}
	spec := descriptor.Spec()
	if spec.Name != "parts-catalog-suggest" || len(spec.Lists) != 1 ||
		spec.Lists[0].ThresholdMillionths == nil || *spec.Lists[0].ThresholdMillionths != 650_000 ||
		descriptor.Identities().Behavior.Hex() != copyDescriptor.Identities().Behavior.Hex() {
		t.Fatal("descriptor")
	}
}
