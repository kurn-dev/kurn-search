package catalogid

import (
	"bytes"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/kurn-dev/kurn-search/adapter"
	"github.com/kurn-dev/kurn-search/internal/jsjson"
	"github.com/kurn-dev/kurn-search/internal/jsorder"
)

func TestProjectStrictCanonicalAndOrdered(t *testing.T) {
	t.Parallel()
	semantics := New()
	source := []byte(`{"tenant_id":"tenant-1","identifier":"ÅBC-123","id":"one","partition_id":"partition-1"}`)
	entries, err := semantics.Project(source)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].EntryID() != "one" {
		t.Fatalf("unexpected entries: %#v", entries)
	}
	keys := entries[0].Keys()
	wantExact, _ := physicalKey(kindExact, "abc-123")
	if !slices.Contains(keys, wantExact) || !slices.IsSortedFunc(keys, jsorder.Compare) {
		t.Fatalf("projected keys are missing exact evidence or not UTF-16 ordered: %#v", keys)
	}
	wantPayload := []byte("{\n  \"id\": \"one\",\n  \"identifier\": \"ÅBC-123\",\n  \"partition_id\": \"partition-1\",\n  \"tenant_id\": \"tenant-1\"\n}\n")
	if !bytes.Equal(entries[0].Payload(), wantPayload) {
		t.Fatalf("payload mismatch\n got: %s\nwant: %s", entries[0].Payload(), wantPayload)
	}
}

func TestStrictDomainJSONRejectsHostileShapes(t *testing.T) {
	t.Parallel()
	semantics := New()
	validScope := []byte(`{"tenant_id":"tenant-1"}`)
	invalidSources := [][]byte{
		[]byte(`{"id":"one","identifier":"abc","tenant_id":"tenant-1","extra":"x"}`),
		[]byte(`{"id":"one","id":"two","identifier":"abc","tenant_id":"tenant-1"}`),
		[]byte(`{"id":"one","identifier":"abc","tenant_id":null}`),
		[]byte(`{"id":"one","identifier":"abc","tenant_id":"tenant-1"} trailing`),
		[]byte(`{"id":"","identifier":"abc","tenant_id":"tenant-1"}`),
	}
	for _, source := range invalidSources {
		if _, err := semantics.Project(source); err == nil {
			t.Fatalf("Project(%q) unexpectedly succeeded", source)
		}
	}
	invalidRequests := [][]byte{
		[]byte(`{"query":"abc","tenant_id":"tenant-1"}`),
		[]byte(`{"query":"abc","limit":1.5}`),
		[]byte(`{"query":"abc","limit":null}`),
		[]byte(`{"query":"abc","partition_id":""}`),
	}
	for _, request := range invalidRequests {
		if _, err := semantics.Compile(request, validScope); err == nil {
			t.Fatalf("Compile(%q) unexpectedly succeeded", request)
		}
	}
	for _, scope := range [][]byte{
		[]byte(`{"tenant_id":null}`), []byte(`{"tenant_id":""}`),
		[]byte(`{"tenant_id":"tenant-1","extra":"x"}`),
	} {
		if _, err := semantics.Compile([]byte(`{"query":"abc"}`), scope); err == nil {
			t.Fatalf("Compile() unexpectedly accepted scope %q", scope)
		}
	}
}

func TestCompileFrozenBoundariesAndWhitespace(t *testing.T) {
	t.Parallel()
	semantics := New()
	scope := []byte(`{"tenant_id":"tenant-1"}`)
	plan, err := semantics.Compile([]byte(`{"query":"AB"}`), scope)
	if err != nil {
		t.Fatal(err)
	}
	kinds := make([]adapter.EvidenceKind, 0, len(plan.Probes()))
	for _, probe := range plan.Probes() {
		kinds = append(kinds, probe.Kind())
		if probe.ListRole() != listRole {
			t.Fatalf("unexpected list role %q", probe.ListRole())
		}
	}
	if !slices.Equal(kinds, []adapter.EvidenceKind{kindExact, kindWord}) {
		t.Fatalf("short query kinds = %#v", kinds)
	}

	for _, query := range []string{"", "   ", "\u00A0", "\uFEFF", " \uFEFF\u2028 "} {
		raw, _ := jsjson.Encode(jsjson.Object{"query": query})
		if _, err := semantics.Compile(raw, scope); err == nil {
			t.Fatalf("whitespace query %q unexpectedly succeeded", query)
		}
	}

	repeated, _ := jsjson.Encode(jsjson.Object{"query": "LOT LOT LOT"})
	plan, err = semantics.Compile(repeated, scope)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Probes()) != 4 {
		t.Fatalf("repeated query emitted %d probes, want 4", len(plan.Probes()))
	}

	overProbe, _ := jsjson.Encode(jsjson.Object{
		"query": strings.Join(func() []string {
			values := make([]string, 22)
			for index := range values {
				values[index] = "token" + string(rune('a'+index))
			}
			return values
		}(), " "),
	})
	if _, err := semantics.Compile(overProbe, scope); err == nil {
		t.Fatal("over-64 probe query unexpectedly succeeded")
	}
	overKey, _ := jsjson.Encode(jsjson.Object{"query": strings.Repeat("😀", 256)})
	if _, err := semantics.Compile(overKey, scope); err == nil {
		t.Fatal("over-512 physical key unexpectedly succeeded")
	}
}

func TestResolveFrozenRankingScopeAndEvidence(t *testing.T) {
	t.Parallel()
	fixture, err := loadFixture()
	if err != nil {
		t.Fatal(err)
	}
	semantics := New()
	entries := make([]adapter.ProjectedEntry, 0, len(fixture.Records))
	for _, record := range fixture.Records {
		raw, _ := encodeFixtureRecord(record)
		projected, err := semantics.Project(raw)
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, projected[0])
	}
	query := fixture.Queries[0]
	request, scope, _ := encodeFixtureQuery(query)
	plan, err := semantics.Compile(request, scope)
	if err != nil {
		t.Fatal(err)
	}
	observed, err := synthesizeEvidence(entries, plan, query, 1000)
	if err != nil {
		t.Fatal(err)
	}
	resolution, err := semantics.Resolve(request, scope, plan, observed)
	if err != nil {
		t.Fatal(err)
	}
	hits := resolution.Hits()
	if len(hits) == 0 || hits[0].EntryID() != "a-exact" {
		t.Fatalf("unexpected ranked hits: %#v", hitIDs(hits))
	}
	for _, hit := range hits {
		if len(hit.Evidence()) == 0 || !bytes.Contains(hit.Evidence(), []byte(`"exact"`)) ||
			!bytes.Contains(hit.Evidence(), []byte(`"starts"`)) || !bytes.Contains(hit.Evidence(), []byte(`"ends"`)) ||
			!bytes.Contains(hit.Evidence(), []byte(`"word"`)) {
			t.Fatalf("hit %q lacks mandatory complete evidence", hit.EntryID())
		}
	}
	if slices.Contains(hitIDs(hits), "j-other-partition") || slices.Contains(hitIDs(hits), "k-other-tenant") {
		t.Fatalf("trusted/caller scope leaked: %#v", hitIDs(hits))
	}
}

func TestResolveSaturationAndHostileEvidenceFailClosed(t *testing.T) {
	t.Parallel()
	semantics := New()
	request := []byte(`{"query":"abc","partition_id":"partition-1","limit":25}`)
	scope := []byte(`{"tenant_id":"tenant-1"}`)
	plan, err := semantics.Compile(request, scope)
	if err != nil {
		t.Fatal(err)
	}
	probe := plan.Probes()[0]
	payload := []byte(`{"id":"one","identifier":"abc","partition_id":"partition-1","tenant_id":"tenant-1"}`)
	candidate, _ := adapter.NewCandidate(adapter.CandidateSpec{EntryID: "one", Score: 100, EchoedKey: probe.PhysicalKey(), Payload: payload})
	observed := make([]adapter.ProbeEvidence, len(plan.Probes()))
	for index, current := range plan.Probes() {
		candidates := []adapter.Candidate{}
		if index == 0 {
			candidates = []adapter.Candidate{candidate}
		}
		observed[index], _ = adapter.NewProbeEvidence(adapter.ProbeEvidenceSpec{
			Probe: current, RequestedPool: 1, Candidates: candidates,
		})
	}
	_, err = semantics.Resolve(request, scope, plan, observed)
	var typed *Error
	if !errors.As(err, &typed) || typed.Category != CategoryIncomplete {
		t.Fatalf("saturation error = %v, want incomplete", err)
	}

	wrong, _ := adapter.NewCandidate(adapter.CandidateSpec{EntryID: "one", Score: 99, EchoedKey: probe.PhysicalKey(), Payload: payload})
	observed[0], _ = adapter.NewProbeEvidence(adapter.ProbeEvidenceSpec{Probe: probe, RequestedPool: 2, Candidates: []adapter.Candidate{wrong}})
	for index := 1; index < len(observed); index++ {
		observed[index], _ = adapter.NewProbeEvidence(adapter.ProbeEvidenceSpec{Probe: plan.Probes()[index], RequestedPool: 2})
	}
	_, err = semantics.Resolve(request, scope, plan, observed)
	if !errors.As(err, &typed) || typed.Category != CategoryInvalidEvidence {
		t.Fatalf("hostile evidence error = %v, want invalid_evidence", err)
	}
}

func TestResolveRejectsCardinalityPoolProbeAndPayloadAttacks(t *testing.T) {
	t.Parallel()
	semantics := New()
	request := []byte(`{"query":"abc","partition_id":"partition-1","limit":25}`)
	scope := []byte(`{"tenant_id":"tenant-1"}`)
	plan, err := semantics.Compile(request, scope)
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"id":"one","identifier":"abc","partition_id":"partition-1","tenant_id":"tenant-1"}`)
	baseline := evidenceForPlan(t, plan, 128, payload)

	assertCategory := func(t *testing.T, evidence []adapter.ProbeEvidence, category Category) {
		t.Helper()
		_, err := semantics.Resolve(request, scope, plan, evidence)
		var typed *Error
		if !errors.As(err, &typed) || typed.Category != category {
			t.Fatalf("Resolve() error = %v, want %s", err, category)
		}
	}
	t.Run("missing-evidence", func(t *testing.T) { assertCategory(t, baseline[:len(baseline)-1], CategoryInvalidPlan) })

	mixedPool := append([]adapter.ProbeEvidence(nil), baseline...)
	mixedPool[1], _ = adapter.NewProbeEvidence(adapter.ProbeEvidenceSpec{Probe: plan.Probes()[1], RequestedPool: 256})
	t.Run("mixed-pool", func(t *testing.T) { assertCategory(t, mixedPool, CategoryInvalidEvidence) })

	wrongProbe := append([]adapter.ProbeEvidence(nil), baseline...)
	wrongProbe[0], _ = adapter.NewProbeEvidence(adapter.ProbeEvidenceSpec{Probe: plan.Probes()[1], RequestedPool: 128})
	t.Run("wrong-probe", func(t *testing.T) { assertCategory(t, wrongProbe, CategoryInvalidEvidence) })

	duplicate := append([]adapter.ProbeEvidence(nil), baseline...)
	candidate, _ := adapter.NewCandidate(adapter.CandidateSpec{EntryID: "one", Score: 100, EchoedKey: plan.Probes()[0].PhysicalKey(), Payload: payload})
	duplicate[0], _ = adapter.NewProbeEvidence(adapter.ProbeEvidenceSpec{Probe: plan.Probes()[0], RequestedPool: 128, Candidates: []adapter.Candidate{candidate, candidate}})
	t.Run("duplicate-entry", func(t *testing.T) { assertCategory(t, duplicate, CategoryInvalidEvidence) })

	wrongScope := append([]adapter.ProbeEvidence(nil), baseline...)
	scopedPayload := []byte(`{"id":"one","identifier":"abc","partition_id":"partition-1","tenant_id":"tenant-2"}`)
	scoped, _ := adapter.NewCandidate(adapter.CandidateSpec{EntryID: "one", Score: 100, EchoedKey: plan.Probes()[0].PhysicalKey(), Payload: scopedPayload})
	wrongScope[0], _ = adapter.NewProbeEvidence(adapter.ProbeEvidenceSpec{Probe: plan.Probes()[0], RequestedPool: 128, Candidates: []adapter.Candidate{scoped}})
	t.Run("wrong-tenant", func(t *testing.T) { assertCategory(t, wrongScope, CategoryInvalidEvidence) })

	changedPayload := append([]adapter.ProbeEvidence(nil), baseline...)
	other := []byte(`{"id":"one","identifier":"different","partition_id":"partition-1","tenant_id":"tenant-1"}`)
	second, _ := adapter.NewCandidate(adapter.CandidateSpec{EntryID: "one", Score: 100, EchoedKey: plan.Probes()[1].PhysicalKey(), Payload: other})
	changedPayload[1], _ = adapter.NewProbeEvidence(adapter.ProbeEvidenceSpec{Probe: plan.Probes()[1], RequestedPool: 128, Candidates: []adapter.Candidate{second}})
	t.Run("cross-probe-payload-change", func(t *testing.T) { assertCategory(t, changedPayload, CategoryInvalidEvidence) })
}

func TestResolveEntryIDTieUsesJavaScriptUTF16Order(t *testing.T) {
	t.Parallel()
	semantics := New()
	records := []fixtureRecord{
		{ID: "😀", Identifier: "abc", TenantID: "tenant-1"},
		{ID: "\uE000", Identifier: "abc", TenantID: "tenant-1"},
	}
	entries := make([]adapter.ProjectedEntry, 0, len(records))
	for _, record := range records {
		raw, _ := encodeFixtureRecord(record)
		projected, err := semantics.Project(raw)
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, projected[0])
	}
	query := fixtureQuery{ID: "tie", Request: fixtureRequest{Query: "abc", Limit: 25}, Scope: fixtureScope{TenantID: "tenant-1"}}
	request, scope, _ := encodeFixtureQuery(query)
	plan, _ := semantics.Compile(request, scope)
	evidence, _ := synthesizeEvidence(entries, plan, query, 128)
	resolution, err := semantics.Resolve(request, scope, plan, evidence)
	if err != nil {
		t.Fatal(err)
	}
	if got := hitIDs(resolution.Hits()); !slices.Equal(got, []string{"😀", "\uE000"}) {
		t.Fatalf("UTF-16 tie order = %#v", got)
	}
}

func evidenceForPlan(t *testing.T, plan adapter.CompiledPlan, pool int, payload []byte) []adapter.ProbeEvidence {
	t.Helper()
	result := make([]adapter.ProbeEvidence, len(plan.Probes()))
	for index, probe := range plan.Probes() {
		candidate, err := adapter.NewCandidate(adapter.CandidateSpec{EntryID: "one", Score: 100, EchoedKey: probe.PhysicalKey(), Payload: payload})
		if err != nil {
			t.Fatal(err)
		}
		result[index], err = adapter.NewProbeEvidence(adapter.ProbeEvidenceSpec{Probe: probe, RequestedPool: pool, Candidates: []adapter.Candidate{candidate}})
		if err != nil {
			t.Fatal(err)
		}
	}
	return result
}

func hitIDs(hits []adapter.ResolvedHit) []string {
	result := make([]string, len(hits))
	for index, hit := range hits {
		result[index] = hit.EntryID()
	}
	return result
}
