package partscatalogselect

import (
	"bytes"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/kurn-dev/kurn-search/adapter"
	"github.com/kurn-dev/kurn-search/adapters/partscatalog"
)

func TestResolveValidatesAllMembershipEvidenceBeforeLimit(t *testing.T) {
	request := []byte(`{"query":"AX-100","partition_id":"north","limit":1,"filters":{"manufacturer":["Northstar","Halden"],"category":["bearing","filter"],"status":"active"}}`)
	plan, err := New().Compile(request, testScope)
	if err != nil {
		t.Fatal(err)
	}
	base := []byte(`{"id":"part-1","identifier":"AX-100","manufacturer":"Northstar","category":"bearing","status":"active","tenant_id":"tenant-a","partition_id":"north"}`)
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
	alternate := []byte(` {"partition_id":"north","tenant_id":"tenant-a","status":"active","category":"bearing","manufacturer":"\u004eorthstar","identifier":"AX-100","id":"part-1"} `)
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

func TestSelectionPreservesQueryPlanAndLegacyRefusals(t *testing.T) {
	oldRequest := []byte(`{"query":"AX-100","filters":{"manufacturer":"Northstar"},"limit":25}`)
	newRequest := []byte(`{"query":"AX-100","filters":{"manufacturer":["Northstar","Northstar"]},"limit":25}`)
	old, err := partscatalog.New().Compile(oldRequest, testScope)
	if err != nil {
		t.Fatal(err)
	}
	current, err := New().Compile(newRequest, testScope)
	if err != nil {
		t.Fatal(err)
	}
	if !samePlan(old, current) {
		t.Fatal("filter changed physical plan")
	}
	if _, err := partscatalog.New().Compile(newRequest, testScope); err == nil {
		t.Fatal("legacy arrays accepted")
	}
	oldFilter, err := partscatalog.New().CompileFilter(oldRequest, testScope)
	if err != nil {
		t.Fatal(err)
	}
	filter, err := New().CompileMembershipFilter(newRequest, testScope)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(oldFilter.Trusted(), filter.Trusted()) || filter.Caller()[0].Values()[0] != oldFilter.Caller()[0].Value() {
		t.Fatal("scalar provenance changed")
	}
	if _, ok := any(New()).(adapter.FilterCompiler); ok {
		t.Fatal("new adapter acquired dummy legacy compiler")
	}
	binding := OperationalBinding{PhysicalListName: "parts-catalog-v1", ConfigSHA256: strings.Repeat("c", 64), TestedEngines: []string{"v0.6.0-1-gf7423cd"}, TimeoutMillis: 10000, MaxInflight: 1, WorstCaseChargeBytes: 1}
	d, err := NewDescriptor(binding)
	if err != nil {
		t.Fatal(err)
	}
	d2, err := NewDescriptor(binding)
	if err != nil {
		t.Fatal(err)
	}
	previous, err := partscatalog.NewDescriptor(partscatalog.OperationalBinding(binding))
	if err != nil {
		t.Fatal(err)
	}
	if d.Identities().Behavior.Hex() != d2.Identities().Behavior.Hex() || d.Identities().Behavior.Hex() == previous.Identities().Behavior.Hex() || d.Spec().Name != "parts-catalog-select" {
		t.Fatal("descriptor identity")
	}
	f, err := loadFixture()
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range f.Queries {
		raw, scope, err := encodeFixtureQuery(q)
		if err != nil {
			t.Fatal(err)
		}
		var request map[string]json.RawMessage
		if json.Unmarshal(raw, &request) != nil {
			t.Fatal("request")
		}
		delete(request, "filters")
		bare, _ := json.Marshal(request)
		a, e := New().Compile(raw, scope)
		if e != nil {
			t.Fatal(e)
		}
		b, e := partscatalog.New().Compile(bare, scope)
		if e != nil || !samePlan(a, b) {
			t.Fatal(q.ID, "per-value fanout")
		}
	}
}
