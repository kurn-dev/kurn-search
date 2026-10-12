package partscatalogsuggest

import (
	"bytes"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/kurn-dev/kurn-search/adapter"
)

func osa1(t *testing.T) Semantics {
	t.Helper()
	s, err := NewWithPolicy(PolicyOSA1)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func suggestRecord(id, identifier string) []byte {
	return []byte(`{"id":"` + id + `","identifier":"` + identifier + `","manufacturer":"Northstar","category":"bearing","status":"active","tenant_id":"tenant-a"}`)
}

func requireCategory(t *testing.T, err error, category Category) {
	t.Helper()
	var refusal *Error
	if !errors.As(err, &refusal) || refusal.Category != category {
		t.Fatalf("wanted %s, got %v", category, err)
	}
}

func TestPolicyVocabularyAndDescriptorIdentity(t *testing.T) {
	if _, err := NewWithPolicy("nope"); err == nil {
		t.Fatal("unknown policy accepted")
	}
	// The 0.60 variant: its own diagnostic identity (policy name, threshold
	// 600,000), the same acceptance behaviour and therefore the same
	// behaviour digest as osa1, and a score floor of 60.
	sixty, err := NewWithPolicy(PolicyOSA1G60)
	if err != nil || sixty.Policy() != PolicyOSA1G60 || policyThreshold(PolicyOSA1G60) != 600_000 || policyMinScore(PolicyOSA1G60) != 60 {
		t.Fatal("osa1-060 policy")
	}
	b60 := OperationalBinding{
		PhysicalListName: "parts-catalog-suggest-v1", ConfigSHA256: strings.Repeat("c", 64), Policy: PolicyOSA1G60,
		TestedEngines: []string{"v0.6.0-1-gf7423cd"}, TimeoutMillis: 10000, MaxInflight: 1, WorstCaseChargeBytes: 1,
	}
	d60, err := NewDescriptor(b60)
	if err != nil || *d60.Spec().Lists[0].ThresholdMillionths != 600_000 || !bytes.Contains(d60.DiagnosticJSON(), []byte(`"policy": "osa1-060"`)) {
		t.Fatalf("osa1-060 descriptor: %v", err)
	}
	b45 := b60
	b45.Policy = PolicyOSA1
	d45, _ := NewDescriptor(b45)
	if bytes.Equal(d45.DiagnosticJSON(), d60.DiagnosticJSON()) || !d45.Identities().Behavior.Equal(d60.Identities().Behavior) {
		t.Fatal("osa1-060 must differ from osa1 in diagnostic and share its behaviour digest")
	}
	low := []byte(`{"query":"AX-100","limit":25}`)
	plan60 := mustCompile(t, sixty, low)
	_, err = sixty.Resolve(low, testScope, plan60, testEvidence(t, plan60, 128, testCandidate(t, "PC-006", 59, "AX-101", suggestRecord("PC-006", "AX-101"))))
	requireInvalidEvidence(t, err)
	if r, err := sixty.Resolve(low, testScope, plan60, testEvidence(t, plan60, 128, testCandidate(t, "PC-006", 60, "AX-101", suggestRecord("PC-006", "AX-101")))); err != nil || len(r.Hits()) != 1 {
		t.Fatalf("osa1-060 accepts a score-60 one-edit candidate: %v", err)
	}
	rejected60 := testEvidence(t, plan60, 128, testCandidate(t, "PC-020", 100, "AX-001", suggestRecord("PC-020", "AX-001")))
	if _, taken, err := sixty.CompileFallback(low, testScope, plan60, rejected60); err != nil || !taken {
		t.Fatalf("osa1-060 fallback on rejected pool: taken %v err %v", taken, err)
	}
	c60, err := GenerateArtifactsFor(PolicyOSA1G60)
	if err != nil || !bytes.Contains(c60.ResolvedResult, []byte("osa1-fallback-on-nonempty-pool")) {
		t.Fatalf("osa1-060 artifacts: %v", err)
	}
	if s, err := NewWithPolicy(""); err != nil || s.Policy() != "" || New().Policy() != "" {
		t.Fatal("default policy")
	}
	binding := OperationalBinding{
		PhysicalListName: "parts-catalog-suggest-v1", ConfigSHA256: strings.Repeat("c", 64),
		TestedEngines: []string{"v0.6.0-1-gf7423cd"}, TimeoutMillis: 10000, MaxInflight: 1, WorstCaseChargeBytes: 1,
	}
	def, err := NewDescriptor(binding)
	if err != nil {
		t.Fatal(err)
	}
	binding.Policy = PolicyOSA1
	opt, err := NewDescriptor(binding)
	if err != nil {
		t.Fatal(err)
	}
	binding.Policy = "nope"
	if _, err := NewDescriptor(binding); err == nil {
		t.Fatal("unknown policy descriptor accepted")
	}
	if def.Spec().Policy != "" || opt.Spec().Policy != PolicyOSA1 ||
		*def.Spec().Lists[0].ThresholdMillionths != 650_000 || *opt.Spec().Lists[0].ThresholdMillionths != 450_000 {
		t.Fatalf("spec: default %+v opt-in %+v", def.Spec().Policy, opt.Spec().Policy)
	}
	if def.Identities().Behavior.Equal(opt.Identities().Behavior) || def.Identities().Search.Equal(opt.Identities().Search) {
		t.Fatal("the opt-in policy shares the default behaviour identity")
	}
	if bytes.Equal(def.DiagnosticJSON(), opt.DiagnosticJSON()) || !bytes.Contains(opt.DiagnosticJSON(), []byte(`"policy": "osa1"`)) || bytes.Contains(def.DiagnosticJSON(), []byte("policy")) {
		t.Fatalf("diagnostics: %s", opt.DiagnosticJSON())
	}
	// The default artifacts are unchanged by the policy-only fixture queries.
	a, err := GenerateArtifacts()
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(a.ResolvedResult, []byte("osa1-")) {
		t.Fatal("default artifacts evaluate the policy-only queries")
	}
	b, err := GenerateArtifactsFor(PolicyOSA1)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b.ResolvedResult, []byte("osa1-fallback-on-nonempty-pool")) || !bytes.Contains(b.CompiledPlan, []byte("osa1-rejects-distance-two")) {
		t.Fatal("policy artifacts miss the policy queries")
	}
}

func TestOSA1FallbackOnNonEmptyPoolAndSuppression(t *testing.T) {
	s := osa1(t)
	request := []byte(`{"query":"XA-100","limit":25}`)
	plan, err := s.Compile(request, testScope)
	if err != nil {
		t.Fatal(err)
	}
	if !samePlan(plan, mustCompile(t, New(), request)) {
		t.Fatal("the policy changed the first-pass plan")
	}
	rejected := testEvidence(t, plan, 128, testCandidate(t, "PC-012", 66, "AX-103", suggestRecord("PC-012", "AX-103")))
	fallback, taken, err := s.CompileFallback(request, testScope, plan, rejected)
	if err != nil || !taken || !slices.Equal(fallback.Probes()[0].PhysicalKeys(), []string{"AX-100", "XA-010"}) {
		t.Fatalf("non-empty pool, all rejected: taken %v err %v", taken, err)
	}
	// The default policy does not fall back on a non-empty pool.
	if _, taken, err := New().CompileFallback(request, testScope, plan, rejected); err != nil || taken {
		t.Fatalf("default policy fell back on a non-empty pool: %v", err)
	}
	// One accepted candidate (within one edit) suppresses the fallback.
	accepted := testEvidence(t, plan, 128,
		testCandidate(t, "PC-012", 66, "AX-103", suggestRecord("PC-012", "AX-103")),
		testCandidate(t, "PC-001", 100, "AX-100", suggestRecord("PC-001", "AX-100")))
	if _, taken, err := s.CompileFallback(request, testScope, plan, accepted); err != nil || taken {
		t.Fatalf("accepted candidate did not suppress the fallback: %v", err)
	}
	// Resolving the two-step plan: the original evidence is retained, the
	// transposition hits are accepted at score 100.
	combined, err := adapter.NewCompiledPlan(append(plan.Probes(), fallback.Probes()...))
	if err != nil {
		t.Fatal(err)
	}
	second, err := adapter.NewProbeEvidence(adapter.ProbeEvidenceSpec{Probe: fallback.Probes()[0], RequestedPool: 128,
		Candidates: []adapter.Candidate{testCandidate(t, "PC-001", 100, "AX-100", suggestRecord("PC-001", "AX-100"))}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.Resolve(request, testScope, combined, append(rejected, second))
	if err != nil || len(result.Hits()) != 1 || result.Hits()[0].EntryID() != "PC-001" || !bytes.Contains(result.Hits()[0].Evidence(), []byte(`"transposition": "yes"`)) {
		t.Fatalf("two-step resolve: %v %+v", err, result)
	}
	// A two-step plan whose first evidence holds an accepted candidate is
	// refused; a one-step plan with nothing accepted and variants available
	// is an executor that skipped the fallback.
	if _, err := s.Resolve(request, testScope, combined, append(accepted, second)); err == nil {
		t.Fatal("two-step plan beside an accepted candidate")
	}
	_, err = s.Resolve(request, testScope, plan, rejected)
	requireCategory(t, err, CategoryInvalidPlan)
	// Without variants the one-step plan resolves to nothing.
	zRequest := []byte(`{"query":"Z","limit":25}`)
	zPlan := mustCompile(t, s, zRequest)
	zEvidence := testEvidence(t, zPlan, 128, testCandidate(t, "PC-012", 66, "AX-103", suggestRecord("PC-012", "AX-103")))
	if _, taken, err := s.CompileFallback(zRequest, testScope, zPlan, zEvidence); err != nil || taken {
		t.Fatalf("no variants: taken %v err %v", taken, err)
	}
	if result, err := s.Resolve(zRequest, testScope, zPlan, zEvidence); err != nil || len(result.Hits()) != 0 {
		t.Fatalf("no variants, nothing accepted: %v %+v", err, result)
	}
}

func mustCompile(t *testing.T, s Semantics, request []byte) adapter.CompiledPlan {
	t.Helper()
	plan, err := s.Compile(request, testScope)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestOSA1AcceptanceCases(t *testing.T) {
	s := osa1(t)
	request := []byte(`{"query":"AX-100","limit":25}`)
	plan := mustCompile(t, s, request)
	// Score-100 inexact key beyond one edit is rejected; within one edit
	// accepted; scores down to 45 are valid under the policy and 44 is not.
	ev := testEvidence(t, plan, 128,
		testCandidate(t, "PC-020", 100, "AX-001", suggestRecord("PC-020", "AX-001")),    // two edits, score 100
		testCandidate(t, "PC-006", 45, "AX-101", suggestRecord("PC-006", "AX-101")),     // one substitution
		testCandidate(t, "PC-021", 70, "AX-1000", suggestRecord("PC-021", "AX-1000")),   // one insertion
		testCandidate(t, "PC-022", 100, "AX-100", suggestRecord("PC-022", "AX-100")),    // exact, at 100 as the contract requires
		testCandidate(t, "PC-023", 60, "ÁX-100", suggestRecord("PC-023", "ÁX-100")),     // one rune substitution
		testCandidate(t, "PC-024", 60, "AX-100ÉÉ", suggestRecord("PC-024", "AX-100ÉÉ")), // two insertions
	)
	result, err := s.Resolve(request, testScope, plan, ev)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, hit := range result.Hits() {
		ids = append(ids, hit.EntryID())
	}
	if !slices.Equal(ids, []string{"PC-022", "PC-021", "PC-023", "PC-006"}) {
		t.Fatalf("accepted: %v", ids)
	}
	low := testEvidence(t, plan, 128, testCandidate(t, "PC-006", 44, "AX-101", suggestRecord("PC-006", "AX-101")))
	_, err = s.Resolve(request, testScope, plan, low)
	requireInvalidEvidence(t, err)
	// The default policy still refuses scores under 65.
	_, err = New().Resolve(request, testScope, mustCompile(t, New(), request), testEvidence(t, mustCompile(t, New(), request), 128,
		testCandidate(t, "PC-006", 60, "AX-101", suggestRecord("PC-006", "AX-101"))))
	requireInvalidEvidence(t, err)
	// Malformed and foreign evidence are errors in CompileFallback, never a
	// fallback miss.
	foreignTenant := testEvidence(t, plan, 128, testCandidate(t, "PC-005", 70, "AX-101",
		bytes.Replace(suggestRecord("PC-005", "AX-101"), []byte("tenant-a"), []byte("tenant-b"), 1)))
	if _, _, err := s.CompileFallback(request, testScope, plan, foreignTenant); err == nil {
		t.Fatal("foreign tenant became a fallback decision")
	}
	wrongKey := testEvidence(t, plan, 128, testCandidate(t, "PC-006", 70, "AX-10X", suggestRecord("PC-006", "AX-101")))
	if _, _, err := s.CompileFallback(request, testScope, plan, wrongKey); err == nil {
		t.Fatal("wrong echoed key became a fallback decision")
	}
	// A saturated first pass with nothing accepted escalates the pool.
	var many []adapter.Candidate
	for i := 0; i < 128; i++ {
		id := "PC-9" + strings.Repeat("0", 3-len(itoa(i))) + itoa(i)
		many = append(many, testCandidate(t, id, 70, "ZZ-"+itoa(i), suggestRecord(id, "ZZ-"+itoa(i))))
	}
	saturated := testEvidence(t, plan, 128, many...)
	fallback, taken, err := s.CompileFallback(request, testScope, plan, saturated)
	if err != nil || !taken {
		t.Fatalf("saturated, nothing accepted: taken %v err %v", taken, err)
	}
	combined, _ := adapter.NewCompiledPlan(append(plan.Probes(), fallback.Probes()...))
	second, _ := adapter.NewProbeEvidence(adapter.ProbeEvidenceSpec{Probe: fallback.Probes()[0], RequestedPool: 128})
	_, err = s.Resolve(request, testScope, combined, append(saturated, second))
	if !s.IsIncomplete(err) {
		t.Fatalf("saturated first pass under the policy: %v", err)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

// Filters apply before acceptance under the policy exactly as under the
// default: a candidate outside the request's filters is invalid evidence,
// never a verifier rejection, and the fallback decision is unaffected.
func TestOSA1FiltersPrecedeAcceptance(t *testing.T) {
	s := osa1(t)
	request := []byte(`{"query":"AX-100","limit":25,"filters":{"status":"discontinued"}}`)
	plan := mustCompile(t, s, request)
	active := testEvidence(t, plan, 128, testCandidate(t, "PC-006", 70, "AX-101", suggestRecord("PC-006", "AX-101")))
	if _, _, err := s.CompileFallback(request, testScope, plan, active); err == nil {
		t.Fatal("candidate outside the status filter reached the fallback decision")
	}
	_, err := s.Resolve(request, testScope, plan, active)
	requireInvalidEvidence(t, err)
	discontinued := []byte(`{"id":"PC-007","identifier":"AX-102","manufacturer":"Vega","category":"gasket","status":"discontinued","tenant_id":"tenant-a"}`)
	ok := testEvidence(t, plan, 128, testCandidate(t, "PC-007", 70, "AX-102", discontinued))
	if _, taken, err := s.CompileFallback(request, testScope, plan, ok); err != nil || taken {
		t.Fatalf("accepted filtered candidate: taken %v err %v", taken, err)
	}
	if result, err := s.Resolve(request, testScope, plan, ok); err != nil || len(result.Hits()) != 1 {
		t.Fatalf("filtered resolve: %v", err)
	}
}

// TestUnionPolicy covers osa1-060-union: its own identity at the 0.60
// generation point, the transposition step taken beside accepted
// candidates, the combination of both branches (one entry once, the
// transposition evidence winning), deterministic ranking and limit,
// no-variant and saturated cases, the score floor, and the triggered
// policy's behaviour left unchanged on the same evidence.
func TestUnionPolicy(t *testing.T) {
	union, err := NewWithPolicy(PolicyOSA1G60Union)
	if err != nil || union.Policy() != PolicyOSA1G60Union || policyThreshold(PolicyOSA1G60Union) != 600_000 || policyMinScore(PolicyOSA1G60Union) != 60 {
		t.Fatal("osa1-060-union policy")
	}
	sixty, _ := NewWithPolicy(PolicyOSA1G60)
	binding := OperationalBinding{
		PhysicalListName: "parts-catalog-suggest-v1", ConfigSHA256: strings.Repeat("c", 64), Policy: PolicyOSA1G60Union,
		TestedEngines: []string{"v0.6.0-1-gf7423cd"}, TimeoutMillis: 10000, MaxInflight: 1, WorstCaseChargeBytes: 1,
	}
	dUnion, err := NewDescriptor(binding)
	if err != nil || *dUnion.Spec().Lists[0].ThresholdMillionths != 600_000 || !bytes.Contains(dUnion.DiagnosticJSON(), []byte(`"policy": "osa1-060-union"`)) {
		t.Fatalf("union descriptor: %v", err)
	}
	binding.Policy = PolicyOSA1G60
	d60, _ := NewDescriptor(binding)
	if bytes.Equal(d60.DiagnosticJSON(), dUnion.DiagnosticJSON()) || d60.Identities().Behavior.Equal(dUnion.Identities().Behavior) {
		t.Fatal("union must have its own diagnostic and behaviour identity")
	}

	request := []byte(`{"query":"XA-100","limit":25}`)
	plan := mustCompile(t, union, request)
	if !samePlan(plan, mustCompile(t, New(), request)) {
		t.Fatal("the policy changed the first-pass plan")
	}
	// PC-001 (AX-100) is a transposition neighbour accepted by the
	// predicate at score 100; PC-090 (XA-101) an accepted substitution
	// neighbour; PC-012 (AX-103) is rejected (two edits).
	first := testEvidence(t, plan, 128,
		testCandidate(t, "PC-001", 100, "AX-100", suggestRecord("PC-001", "AX-100")),
		testCandidate(t, "PC-090", 80, "XA-101", suggestRecord("PC-090", "XA-101")),
		testCandidate(t, "PC-012", 66, "AX-103", suggestRecord("PC-012", "AX-103")))
	fallback, taken, err := union.CompileFallback(request, testScope, plan, first)
	if err != nil || !taken || !slices.Equal(fallback.Probes()[0].PhysicalKeys(), []string{"AX-100", "XA-010"}) {
		t.Fatalf("union: step not taken beside accepted candidates: taken %v err %v", taken, err)
	}
	if _, taken, err := sixty.CompileFallback(request, testScope, plan, first); err != nil || taken {
		t.Fatalf("triggered policy changed: taken %v err %v", taken, err)
	}
	if _, taken, err := New().CompileFallback(request, testScope, plan, first); err != nil || taken {
		t.Fatalf("default policy changed: taken %v err %v", taken, err)
	}
	combined, err := adapter.NewCompiledPlan(append(plan.Probes(), fallback.Probes()...))
	if err != nil {
		t.Fatal(err)
	}
	second := func(candidates ...adapter.Candidate) adapter.ProbeEvidence {
		evidence, err := adapter.NewProbeEvidence(adapter.ProbeEvidenceSpec{Probe: fallback.Probes()[0], RequestedPool: 128, Candidates: candidates})
		if err != nil {
			t.Fatal(err)
		}
		return evidence
	}
	// Both branches: PC-001 appears in both and is served once with the
	// transposition evidence; PC-002 only by the step; PC-090 only by
	// similarity. Ranked: score 100 twice (identifier AX-100, id order),
	// then 80.
	result, err := union.Resolve(request, testScope, combined, append(first, second(
		testCandidate(t, "PC-002", 100, "AX-100", suggestRecord("PC-002", "AX-100")),
		testCandidate(t, "PC-001", 100, "AX-100", suggestRecord("PC-001", "AX-100")))))
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, 3)
	for _, hit := range result.Hits() {
		ids = append(ids, hit.EntryID())
	}
	if !slices.Equal(ids, []string{"PC-001", "PC-002", "PC-090"}) {
		t.Fatalf("union order: %v", ids)
	}
	if !bytes.Contains(result.Hits()[0].Evidence(), []byte(`"transposition": "yes"`)) || !bytes.Contains(result.Hits()[1].Evidence(), []byte(`"transposition": "yes"`)) ||
		bytes.Contains(result.Hits()[2].Evidence(), []byte(`"transposition": "yes"`)) || !bytes.Contains(result.Hits()[2].Result(), []byte(`"score": 80`)) {
		t.Fatalf("union evidence: %s | %s", result.Hits()[0].Evidence(), result.Hits()[2].Evidence())
	}
	// Transposition recovery beside an accepted neighbour (the served
	// IRe.204 case): the step's hit outranks the accepted substitution.
	result, err = union.Resolve(request, testScope, combined, append(testEvidence(t, plan, 128,
		testCandidate(t, "PC-090", 61, "XA-101", suggestRecord("PC-090", "XA-101"))),
		second(testCandidate(t, "PC-003", 100, "AX-100", suggestRecord("PC-003", "AX-100")))))
	if err != nil || len(result.Hits()) != 2 || result.Hits()[0].EntryID() != "PC-003" || result.Hits()[1].EntryID() != "PC-090" {
		t.Fatalf("recovery beside accepted neighbour: %v %+v", err, result)
	}
	// The response limit cuts the combined, ranked list.
	limited := []byte(`{"query":"XA-100","limit":1}`)
	lplan := mustCompile(t, union, limited)
	lcombined, _ := adapter.NewCompiledPlan(append(lplan.Probes(), fallback.Probes()...))
	result, err = union.Resolve(limited, testScope, lcombined, append(testEvidence(t, lplan, 128,
		testCandidate(t, "PC-090", 80, "XA-101", suggestRecord("PC-090", "XA-101"))),
		second(testCandidate(t, "PC-002", 100, "AX-100", suggestRecord("PC-002", "AX-100")))))
	if err != nil || len(result.Hits()) != 1 || result.Hits()[0].EntryID() != "PC-002" {
		t.Fatalf("limit after union: %v %+v", err, result)
	}
	// An empty step beside accepted candidates serves the accepted ones.
	result, err = union.Resolve(request, testScope, combined, append(first, second()))
	if err != nil || len(result.Hits()) != 2 || result.Hits()[0].EntryID() != "PC-001" || result.Hits()[1].EntryID() != "PC-090" {
		t.Fatalf("empty step: %v %+v", err, result)
	}
	// A one-step plan for a query with variants skipped the due step,
	// even with accepted candidates; the triggered policy accepts it.
	_, err = union.Resolve(request, testScope, plan, first)
	requireCategory(t, err, CategoryInvalidPlan)
	if r, err := sixty.Resolve(request, testScope, plan, first); err != nil || len(r.Hits()) != 2 {
		t.Fatalf("triggered one-step resolve changed: %v", err)
	}
	// No variants: no step, one-step plan resolves.
	zRequest := []byte(`{"query":"Z","limit":25}`)
	zPlan := mustCompile(t, union, zRequest)
	zEvidence := testEvidence(t, zPlan, 128, testCandidate(t, "PC-091", 70, "Z1", suggestRecord("PC-091", "Z1")))
	if _, taken, err := union.CompileFallback(zRequest, testScope, zPlan, zEvidence); err != nil || taken {
		t.Fatalf("no variants: taken %v err %v", taken, err)
	}
	if result, err := union.Resolve(zRequest, testScope, zPlan, zEvidence); err != nil || len(result.Hits()) != 1 {
		t.Fatalf("no variants: %v %+v", err, result)
	}
	// A saturated first pool is incomplete even with the step's hits.
	full := make([]adapter.Candidate, 0, 128)
	for i := range 128 {
		id := "PC-5" + itoa(100+i)
		full = append(full, testCandidate(t, id, 70, "XA-1"+itoa(10+i), suggestRecord(id, "XA-1"+itoa(10+i))))
	}
	_, err = union.Resolve(request, testScope, combined, append(testEvidence(t, plan, 128, full...), second()))
	if !union.IsIncomplete(err) {
		t.Fatalf("saturated first pool: %v", err)
	}
	if _, _, err := union.CompileFallback(request, testScope, plan, testEvidence(t, plan, 128, full...)); err != nil {
		t.Fatalf("saturated pool still compiles the step: %v", err)
	}
	// Floors and malformed evidence precede any combination.
	_, err = union.Resolve(request, testScope, combined, append(testEvidence(t, plan, 128,
		testCandidate(t, "PC-090", 59, "XA-101", suggestRecord("PC-090", "XA-101"))), second()))
	requireInvalidEvidence(t, err)
	_, err = union.Resolve(request, testScope, combined, append(first, second(testCandidate(t, "PC-002", 99, "AX-100", suggestRecord("PC-002", "AX-100")))))
	requireInvalidEvidence(t, err)
	_, _, err = union.CompileFallback(request, testScope, plan, testEvidence(t, plan, 128,
		testCandidate(t, "PC-090", 59, "XA-101", suggestRecord("PC-090", "XA-101"))))
	requireInvalidEvidence(t, err)
	artifacts, err := GenerateArtifactsFor(PolicyOSA1G60Union)
	if err != nil || !bytes.Contains(artifacts.ResolvedResult, []byte("union-duplicate-across-branches")) {
		t.Fatalf("union artifacts: %v", err)
	}
	// Two adjacent letters differing only in case: the variant that
	// equals the query under the analyzer is the similarity step's own
	// exact match and is left out of the union's step (the served
	// "yeE.001" case); the triggered policies keep their variant list.
	caseRequest := []byte(`{"query":"yeE.001","limit":25}`)
	casePlan := mustCompile(t, union, caseRequest)
	caseFirst := testEvidence(t, casePlan, 128, testCandidate(t, "PC-095", 100, "YEE.001", suggestRecord("PC-095", "YEE.001")))
	caseFallback, taken, err := union.CompileFallback(caseRequest, testScope, casePlan, caseFirst)
	if err != nil || !taken || !slices.Equal(caseFallback.Probes()[0].PhysicalKeys(), []string{"eyE.001", "yeE.010"}) {
		t.Fatalf("union variants for a case-adjacent query: taken %v err %v %v", taken, err, caseFallback.Probes())
	}
	if probe, ok, err := fallbackProbe(searchRequest{Query: "yeE.001", Limit: 25}, PolicyOSA1G60); err != nil || !ok || !slices.Equal(probe.PhysicalKeys(), []string{"eyE.001", "yEe.001", "yeE.010"}) {
		t.Fatalf("triggered variants changed: %v %v", err, probe.PhysicalKeys())
	}
	caseCombined, _ := adapter.NewCompiledPlan(append(casePlan.Probes(), caseFallback.Probes()...))
	caseSecond, _ := adapter.NewProbeEvidence(adapter.ProbeEvidenceSpec{Probe: caseFallback.Probes()[0], RequestedPool: 128})
	if result, err := union.Resolve(caseRequest, testScope, caseCombined, append(caseFirst, caseSecond)); err != nil || len(result.Hits()) != 1 || result.Hits()[0].EntryID() != "PC-095" {
		t.Fatalf("case-adjacent query under the union: %v %+v", err, result)
	}
	// Every variant collapses onto the query: no step at all.
	aaRequest := []byte(`{"query":"aA","limit":25}`)
	aaPlan := mustCompile(t, union, aaRequest)
	aaFirst := testEvidence(t, aaPlan, 128, testCandidate(t, "PC-096", 100, "AA", suggestRecord("PC-096", "AA")))
	if _, taken, err := union.CompileFallback(aaRequest, testScope, aaPlan, aaFirst); err != nil || taken {
		t.Fatalf("collapsed variants: taken %v err %v", taken, err)
	}
	if result, err := union.Resolve(aaRequest, testScope, aaPlan, aaFirst); err != nil || len(result.Hits()) != 1 {
		t.Fatalf("collapsed variants resolve: %v %+v", err, result)
	}
}
