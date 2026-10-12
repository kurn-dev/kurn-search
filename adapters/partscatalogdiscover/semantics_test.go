package partscatalogdiscover

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/kurn-dev/kurn-search/adapter"
)

func TestResolveValidatesAllMembershipEvidenceBeforeLimit(t *testing.T) {
	request := []byte(`{"query":"AX-100","partition_id":"north","limit":1,"filters":{"manufacturer":["Northstar","Halden"],"category":["bearing","filter"],"status":"active"}}`)
	plan, err := New().Compile(request, testScope)
	if err != nil {
		t.Fatal(err)
	}
	base := []byte(`{"id":"part-1","identifier":"AX-100","name":"Deep groove bearing","manufacturer":"Northstar","category":"bearing","status":"active","tenant_id":"tenant-a","partition_id":"north"}`)
	makeEvidence := func(payloads ...[]byte) []adapter.ProbeEvidence {
		t.Helper()
		out := make([]adapter.ProbeEvidence, len(plan.Probes()))
		for i, probe := range plan.Probes() {
			candidates := []adapter.Candidate{}
			if i < len(payloads) {
				c, e := adapter.NewCandidate(adapter.CandidateSpec{EntryID: "part-1", Score: 100, EchoedKey: probe.PhysicalKey(), Payload: payloads[i]})
				if e != nil {
					t.Fatal(e)
				}
				candidates = append(candidates, c)
			}
			v, e := adapter.NewProbeEvidence(adapter.ProbeEvidenceSpec{Probe: probe, RequestedPool: 128, Candidates: candidates})
			if e != nil {
				t.Fatal(e)
			}
			out[i] = v
		}
		return out
	}
	alternate := []byte(` {"partition_id":"north","tenant_id":"tenant-a","status":"active","category":"bearing","manufacturer":"\u004eorthstar","name":"Deep groove bearing","identifier":"AX-100","id":"part-1"} `)
	result, err := New().Resolve(request, testScope, plan, makeEvidence(base, alternate))
	if err != nil || !result.Complete() || len(result.Hits()) != 1 {
		t.Fatal("equivalent payloads", err)
	}
	r, err := parseSource(base)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := canonicalSource(r)
	if err != nil || !bytes.Equal(result.Hits()[0].Result(), canonical) {
		t.Fatal("noncanonical resolved payload")
	}
	for _, change := range [][2]string{
		{"tenant-a", "tenant-b"}, {"north", "south"}, {"Northstar", "Vega"}, {"bearing", "gasket"},
		{"active", "discontinued"}, {"AX-100", "GX-200"}, {"part-1", "part-2"},
		{"Northstar", " Northstar"}, {"Northstar", "Halden"},
	} {
		t.Run(change[0]+"-"+change[1], func(t *testing.T) {
			bad := bytes.Replace(base, []byte(change[0]), []byte(change[1]), 1)
			_, err := New().Resolve(request, testScope, plan, makeEvidence(base, bad))
			var refusal *Error
			if !errors.As(err, &refusal) || refusal.Category != CategoryInvalidEvidence {
				t.Fatalf("late invalid candidate after result limit: %v", err)
			}
		})
	}
	t.Run("duplicate-id-in-probe", func(t *testing.T) {
		ev := makeEvidence(base)
		c := ev[0].Candidates()[0]
		ev[0], err = adapter.NewProbeEvidence(adapter.ProbeEvidenceSpec{Probe: plan.Probes()[0], RequestedPool: 128, Candidates: []adapter.Candidate{c, c}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := New().Resolve(request, testScope, plan, ev); err == nil {
			t.Fatal("duplicate admitted")
		}
	})
	t.Run("saturation-does-not-mask-later-invalid-payload", func(t *testing.T) {
		ev := makeEvidence(base, bytes.Replace(base, []byte("tenant-a"), []byte("tenant-b"), 1))
		for i := range ev {
			ev[i], err = adapter.NewProbeEvidence(adapter.ProbeEvidenceSpec{Probe: ev[i].Probe(), RequestedPool: 1, Candidates: ev[i].Candidates()})
			if err != nil {
				t.Fatal(err)
			}
		}
		_, err := New().Resolve(request, testScope, plan, ev)
		var refusal *Error
		if !errors.As(err, &refusal) || refusal.Category != CategoryInvalidEvidence {
			t.Fatal("saturation masked invalid evidence", err)
		}
	})
}

func TestDiscoverySourceAndPhysicalKeyBoundaries(t *testing.T) {
	base := `{"id":"boundary","identifier":"AX-100","name":"%s","manufacturer":"Northstar","category":"bearing","status":"active","tenant_id":"tenant-a"}`
	if _, err := New().Project([]byte(`{"id":"boundary","identifier":"AX-100","manufacturer":"Northstar","category":"bearing","status":"active","tenant_id":"tenant-a"}`)); err == nil {
		t.Fatal("missing name admitted")
	}
	for _, name := range []string{"", " name", "name ", "\ufeffname", "name\ufeff", strings.Repeat("x", 81)} {
		if _, err := New().Project([]byte(fmt.Sprintf(base, name))); err == nil {
			t.Fatalf("invalid name admitted: %q", name)
		}
	}
	if _, err := New().Project([]byte(fmt.Sprintf(base, "one two three four five six seven eight nine"))); err == nil {
		t.Fatal("nine-token name admitted")
	}

	word := strings.Repeat("𐐀", 80)
	key, err := physicalKey(kindNameWord, word)
	if err != nil || utf8.RuneCountInString(key) != 430 {
		t.Fatalf("four-byte word key boundary: runes=%d err=%v", utf8.RuneCountInString(key), err)
	}
	request := []byte(`{"query":"` + word + `","limit":25}`)
	plan, err := New().Compile(request, testScope)
	if err != nil || len(plan.Probes()) != 3 {
		t.Fatalf("80-scalar word plan: probes=%d err=%v", len(plan.Probes()), err)
	}
	projected, err := New().Project([]byte(fmt.Sprintf(base, word)))
	if err != nil || len(projected) != 1 || len(projected[0].Keys()) > 245 {
		t.Fatalf("conservative source-key bound: keys=%d err=%v", len(projected[0].Keys()), err)
	}
}

func TestDiscoveryOracleAndBounds(t *testing.T) {
	fixture, err := loadFixture()
	if err != nil {
		t.Fatal(err)
	}
	semantics := New()
	entries := make([]adapter.ProjectedEntry, 0, len(fixture.Records))
	for _, record := range fixture.Records {
		raw, err := encodeFixtureRecord(record)
		if err != nil {
			t.Fatal(err)
		}
		projected, err := semantics.Project(raw)
		if err != nil || len(projected) != 1 {
			t.Fatal(record.ID, err)
		}
		entries = append(entries, projected[0])
	}
	for _, query := range fixture.Queries {
		raw, scope, err := encodeFixtureQuery(query)
		if err != nil {
			t.Fatal(err)
		}
		plan, err := semantics.Compile(raw, scope)
		if err != nil {
			t.Fatal(query.ID, err)
		}
		evidence, err := synthesizeEvidence(entries, plan, query, 1000)
		if err != nil {
			t.Fatal(query.ID, err)
		}
		result, err := semantics.Resolve(raw, scope, plan, evidence)
		if err != nil {
			t.Fatal(query.ID, err)
		}
		ids := make([]string, 0, len(result.Hits()))
		for _, hit := range result.Hits() {
			ids = append(ids, hit.EntryID())
		}
		if !slices.Equal(ids, query.ExpectedIDs) {
			t.Fatalf("%s: got %v want %v", query.ID, ids, query.ExpectedIDs)
		}
	}

	for _, query := range []string{
		"", "   ", " ball", "ball ", "\ufeffball", "ball\ufeff", strings.Repeat("x", 81),
		"a b c d e f g h i", "---",
		"a-b-c-d e-f-g-h i-j-k-l m-n-o-p q-r-s-t u-v-w-x y-z-0-1 2-3-4-5",
	} {
		raw := []byte(`{"query":` + string(mustJSON(t, query)) + `,"limit":25}`)
		if _, err := semantics.Compile(raw, testScope); err == nil {
			t.Fatalf("invalid query admitted: %q", query)
		}
	}

	binding := OperationalBinding{PhysicalListName: "parts-catalog-discover-v1", ConfigSHA256: strings.Repeat("c", 64), TestedEngines: []string{"v0.6.0-1-gf7423cd"}, TimeoutMillis: 10000, MaxInflight: 1, WorstCaseChargeBytes: 1}
	d, err := NewDescriptor(binding)
	if err != nil {
		t.Fatal(err)
	}
	d2, err := NewDescriptor(binding)
	if err != nil || d.Identities().Behavior.Hex() != d2.Identities().Behavior.Hex() || d.Spec().Name != "parts-catalog-discover" {
		t.Fatal("descriptor identity", err)
	}
}

func mustJSON(t *testing.T, value string) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
