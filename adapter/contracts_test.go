package adapter

import (
	"errors"
	"math"
	"strconv"
	"strings"
	"testing"
)

func TestProjectedEntryBoundsAndImmutability(t *testing.T) {
	keys := []string{"first", "second"}
	payload := []byte(`{"id":"entry"}`)
	entry, err := NewProjectedEntry(ProjectedEntrySpec{EntryID: "entry", Keys: keys, Payload: payload})
	if err != nil {
		t.Fatal(err)
	}
	keys[0] = "changed"
	payload[2] = 'X'
	gotKeys := entry.Keys()
	gotPayload := entry.Payload()
	if gotKeys[0] != "first" || string(gotPayload) != `{"id":"entry"}` || !entry.Valid() {
		t.Fatal("constructor input mutation changed entry")
	}
	gotKeys[0] = "changed"
	gotPayload[2] = 'X'
	if entry.Keys()[0] != "first" || string(entry.Payload()) != `{"id":"entry"}` {
		t.Fatal("accessor output mutation changed entry")
	}

	invalid := []ProjectedEntrySpec{
		{},
		{EntryID: "entry", Keys: []string{"key"}, Payload: []byte("null")},
		{EntryID: "entry", Keys: []string{"key", "key"}, Payload: []byte("{}")},
		{EntryID: strings.Repeat("x", MaxEntryIDBytes+1), Keys: []string{"key"}, Payload: []byte("{}")},
		{EntryID: "entry", Keys: []string{strings.Repeat("x", MaxPhysicalKeyRunes+1)}, Payload: []byte("{}")},
		{EntryID: "entry", Keys: []string{"key"}, Payload: []byte{0xff}},
		{EntryID: "entry", Keys: []string{"key"}, Payload: []byte("{}{}")},
	}
	for index, spec := range invalid {
		if _, err := NewProjectedEntry(spec); err == nil {
			t.Fatalf("invalid projected entry %d accepted", index)
		}
	}
}

func TestPlanEvidenceAndResolutionContracts(t *testing.T) {
	probe, err := NewProbe(ProbeSpec{
		Kind: "exact", LogicalTerm: "logical", PhysicalKey: "physical", ListRole: "primary",
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := NewCompiledPlan([]Probe{probe})
	if err != nil || !plan.Valid() || len(plan.Probes()) != 1 {
		t.Fatalf("plan: %v", err)
	}
	if _, err := NewCompiledPlan([]Probe{probe, probe}); err == nil {
		t.Fatal("duplicate probe accepted")
	}
	if _, err := NewProbe(ProbeSpec{Kind: "Exact", LogicalTerm: "x", PhysicalKey: "x", ListRole: "primary"}); err == nil {
		t.Fatal("non-ASCII-token evidence kind accepted")
	}

	payload := []byte(`{"id":"entry"}`)
	candidate, err := NewCandidate(CandidateSpec{
		EntryID: "entry", Score: 100, EchoedKey: "physical", Payload: payload,
	})
	if err != nil {
		t.Fatal(err)
	}
	payload[2] = 'X'
	if string(candidate.Payload()) != `{"id":"entry"}` {
		t.Fatal("candidate did not copy payload")
	}
	if _, err := NewCandidate(CandidateSpec{
		EntryID: "entry", Score: math.NaN(), EchoedKey: "physical", Payload: []byte("{}"),
	}); err == nil {
		t.Fatal("NaN candidate score accepted")
	}

	evidence, err := NewProbeEvidence(ProbeEvidenceSpec{
		Probe: probe, RequestedPool: 1, Candidates: []Candidate{candidate, candidate},
	})
	if err != nil {
		t.Fatalf("candidate count versus pool is adapter semantics, got %v", err)
	}
	candidates := evidence.Candidates()
	candidates[0] = Candidate{}
	if !evidence.Candidates()[0].Valid() || evidence.RequestedPool() != 1 {
		t.Fatal("evidence accessors are mutable")
	}

	hit, err := NewResolvedHit(ResolvedHitSpec{
		EntryID: "entry", Result: []byte(`{"id":"entry"}`), Evidence: []byte(`{"exact":true}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	resolution, err := NewResolution(ResolutionSpec{Complete: true, Hits: []ResolvedHit{hit}})
	if err != nil || !resolution.Complete() || len(resolution.Hits()) != 1 {
		t.Fatalf("resolution: %v", err)
	}
	_, err = NewResolution(ResolutionSpec{Complete: false, Hits: []ResolvedHit{hit}})
	var contractErr *Error
	if !errors.As(err, &contractErr) || contractErr.Category != CategoryIncomplete {
		t.Fatalf("incomplete resolution category: %v", err)
	}
	if _, err := NewResolution(ResolutionSpec{Complete: true, Hits: []ResolvedHit{hit, hit}}); err == nil {
		t.Fatal("duplicate result ID accepted")
	}
}

func TestContractErrorsDoNotEchoInput(t *testing.T) {
	secret := "private-query-secret"
	_, err := NewProbe(ProbeSpec{Kind: "bad kind", LogicalTerm: secret, PhysicalKey: secret, ListRole: "role"})
	if err == nil || strings.Contains(err.Error(), secret) || err.Error() != "adapter: invalid_contract" {
		t.Fatalf("unsafe error: %v", err)
	}
	_, err = NewResolution(ResolutionSpec{Complete: false})
	if err == nil || err.Error() != "adapter: incomplete" {
		t.Fatalf("incomplete error: %v", err)
	}
}

func TestContractExactCeilingsAndOneOver(t *testing.T) {
	keys := make([]string, MaxPhysicalKeys)
	for index := range keys {
		keys[index] = "key-" + strconv.Itoa(index)
	}
	keys[0] = strings.Repeat("k", MaxPhysicalKeyRunes)
	exactPayload := boundedJSONString(MaxOpaqueJSONBytes)
	if _, err := NewProjectedEntry(ProjectedEntrySpec{
		EntryID: strings.Repeat("i", MaxEntryIDBytes), Keys: keys, Payload: exactPayload,
	}); err != nil {
		t.Fatalf("projected-entry exact ceilings: %v", err)
	}
	oneOverKeys := append(append([]string(nil), keys...), "one-over")
	if _, err := NewProjectedEntry(ProjectedEntrySpec{EntryID: "entry", Keys: oneOverKeys, Payload: []byte("{}")}); err == nil {
		t.Fatal("one-over physical-key count accepted")
	}
	if _, err := NewProjectedEntry(ProjectedEntrySpec{
		EntryID: strings.Repeat("i", MaxEntryIDBytes+1), Keys: []string{"key"}, Payload: []byte("{}"),
	}); err == nil {
		t.Fatal("one-over entry ID accepted")
	}
	if _, err := NewProjectedEntry(ProjectedEntrySpec{
		EntryID: "entry", Keys: []string{strings.Repeat("k", MaxPhysicalKeyRunes+1)}, Payload: []byte("{}"),
	}); err == nil {
		t.Fatal("one-over physical key accepted")
	}
	if _, err := NewProjectedEntry(ProjectedEntrySpec{
		EntryID: "entry", Keys: []string{"key"}, Payload: boundedJSONString(MaxOpaqueJSONBytes + 1),
	}); err == nil {
		t.Fatal("one-over payload accepted")
	}

	probes := make([]Probe, MaxProbes)
	for index := range probes {
		probe, err := NewProbe(ProbeSpec{
			Kind: "exact", LogicalTerm: "term-" + strconv.Itoa(index),
			PhysicalKey: "key-" + strconv.Itoa(index), ListRole: "primary",
		})
		if err != nil {
			t.Fatal(err)
		}
		probes[index] = probe
	}
	longProbe, err := NewProbe(ProbeSpec{
		Kind: "exact", LogicalTerm: strings.Repeat("q", MaxLogicalTermRunes),
		PhysicalKey: strings.Repeat("k", MaxPhysicalKeyRunes), ListRole: "primary",
	})
	if err != nil {
		t.Fatalf("probe exact rune ceilings: %v", err)
	}
	if _, err := NewProbe(ProbeSpec{
		Kind: "exact", LogicalTerm: strings.Repeat("q", MaxLogicalTermRunes+1),
		PhysicalKey: "key", ListRole: "primary",
	}); err == nil {
		t.Fatal("one-over logical term accepted")
	}
	if _, err := NewCompiledPlan(probes); err != nil {
		t.Fatalf("plan exact probe ceiling: %v", err)
	}
	if _, err := NewCompiledPlan(append(probes, longProbe)); err == nil {
		t.Fatal("one-over probe count accepted")
	}

	candidates := make([]Candidate, MaxCandidatesPerProbe)
	for index := range candidates {
		candidate, err := NewCandidate(CandidateSpec{
			EntryID: "entry-" + strconv.Itoa(index), Score: float64(index),
			EchoedKey: strings.Repeat("e", MaxPhysicalKeyRunes), Payload: []byte("{}"),
		})
		if err != nil {
			t.Fatal(err)
		}
		candidates[index] = candidate
	}
	if _, err := NewProbeEvidence(ProbeEvidenceSpec{
		Probe: longProbe, RequestedPool: MaxPool, Candidates: candidates,
	}); err != nil {
		t.Fatalf("evidence exact ceilings: %v", err)
	}
	if _, err := NewProbeEvidence(ProbeEvidenceSpec{Probe: longProbe, RequestedPool: MaxPool + 1}); err == nil {
		t.Fatal("one-over pool accepted")
	}
	if _, err := NewProbeEvidence(ProbeEvidenceSpec{
		Probe: longProbe, RequestedPool: MaxPool, Candidates: append(candidates, candidates[0]),
	}); err == nil {
		t.Fatal("one-over candidates accepted")
	}

	hits := make([]ResolvedHit, MaxHits)
	for index := range hits {
		hit, err := NewResolvedHit(ResolvedHitSpec{
			EntryID: "entry-" + strconv.Itoa(index), Result: []byte("{}"),
		})
		if err != nil {
			t.Fatal(err)
		}
		hits[index] = hit
	}
	if _, err := NewResolution(ResolutionSpec{Complete: true, Hits: hits}); err != nil {
		t.Fatalf("resolution exact hit ceiling: %v", err)
	}
	extraHit, _ := NewResolvedHit(ResolvedHitSpec{EntryID: "one-over", Result: []byte("{}")})
	if _, err := NewResolution(ResolutionSpec{Complete: true, Hits: append(hits, extraHit)}); err == nil {
		t.Fatal("one-over hit count accepted")
	}
}

func boundedJSONString(size int) []byte {
	if size < 2 {
		return nil
	}
	return []byte(`"` + strings.Repeat("x", size-2) + `"`)
}
