package partscatalogalternatives

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/kurn-dev/kurn-search/adapter"
	"github.com/kurn-dev/kurn-search/adapters/partscatalogdiscover"
	"github.com/kurn-dev/kurn-search/internal/jsorder"
)

type expectedGroup struct {
	basis   int
	buckets []adapter.SummaryBucketSpec
}

type alternativeCase struct {
	id, query, tenant, partition string
	filters                      map[string]any
	limit, total                 int
	ids                          []string
	groups                       [3]expectedGroup
}

func group(basis int, values ...any) expectedGroup {
	result := expectedGroup{basis: basis}
	for index := 0; index < len(values); index += 2 {
		result.buckets = append(result.buckets, adapter.SummaryBucketSpec{Count: values[index].(int), Value: values[index+1].(string)})
	}
	return result
}

// The 12 expected rows are frozen independently of GenerateArtifacts. A06's
// 25-ID prefix is the accepted fixed-source W7-S1 order, not regenerated from
// the implementation being tested.
func acceptedCases() []alternativeCase {
	return []alternativeCase{
		{"A01", "bearing", "tenant-a", "", nil, 25, 7,
			[]string{"PC-001", "PC-002", "PC-003", "PC-010", "PC-011", "PC-006", "PC-012"},
			[3]expectedGroup{group(7, 6, "bearing", 1, "Bearing"), group(7, 5, "Northstar", 1, "Halden", 1, "northstar"), group(7, 6, "active", 1, "discontinued")}},
		{"A02", "bearing", "tenant-a", "", map[string]any{"manufacturer": "Northstar"}, 25, 5,
			[]string{"PC-001", "PC-003", "PC-010", "PC-006", "PC-012"},
			[3]expectedGroup{group(5, 4, "bearing", 1, "Bearing"), group(7, 5, "Northstar", 1, "Halden", 1, "northstar"), group(5, 4, "active", 1, "discontinued")}},
		{"A03", "bearing", "tenant-a", "", map[string]any{"category": "Bearing"}, 25, 1,
			[]string{"PC-012"}, [3]expectedGroup{group(7, 6, "bearing", 1, "Bearing"), group(1, 1, "Northstar"), group(1, 1, "active")}},
		{"A04", "gasket", "tenant-a", "", map[string]any{"status": "discontinued"}, 25, 2,
			[]string{"PC-007", "ZB-9139"}, [3]expectedGroup{group(2, 2, "gasket"), group(2, 1, "Halden", 1, "Vega"), group(141, 139, "active", 2, "discontinued")}},
		{"A05", "bearing", "tenant-a", "", map[string]any{"manufacturer": "Vega"}, 25, 0,
			[]string{}, [3]expectedGroup{group(0), group(7, 5, "Northstar", 1, "Halden", 1, "northstar"), group(0)}},
		{"A06", "gasket", "tenant-a", "", map[string]any{"manufacturer": []string{"Halden", "Vega"}}, 25, 141,
			[]string{"PC-007", "PC-009", "ZB-9001", "ZB-9002", "ZB-9003", "ZB-9004", "ZB-9005", "ZB-9006", "ZB-9007", "ZB-9008", "ZB-9009", "ZB-9010", "ZB-9011", "ZB-9012", "ZB-9013", "ZB-9014", "ZB-9015", "ZB-9016", "ZB-9017", "ZB-9018", "ZB-9019", "ZB-9020", "ZB-9021", "ZB-9022", "ZB-9023"},
			[3]expectedGroup{group(141, 141, "gasket"), group(141, 139, "Halden", 2, "Vega"), group(141, 139, "active", 2, "discontinued")}},
		{"A07", "AX-100", "tenant-a", "", map[string]any{"category": "bearing", "status": "active"}, 25, 4,
			[]string{"PC-001", "PC-002", "PC-010", "PC-011"}, [3]expectedGroup{group(6, 4, "bearing", 2, "filter"), group(4, 2, "Northstar", 1, "Halden", 1, "northstar"), group(5, 4, "active", 1, "discontinued")}},
		{"A08", "bearing", "tenant-a", "", map[string]any{"category": "Bearing", "manufacturer": "Northstar"}, 25, 1,
			[]string{"PC-012"}, [3]expectedGroup{group(5, 4, "bearing", 1, "Bearing"), group(1, 1, "Northstar"), group(1, 1, "active")}},
		{"A09", "ball", "tenant-b", "", map[string]any{"manufacturer": "Halden"}, 25, 0,
			[]string{}, [3]expectedGroup{group(0), group(1, 1, "Northstar"), group(0)}},
		{"A10", "filter", "tenant-a", "south", map[string]any{"status": "discontinued"}, 25, 0,
			[]string{}, [3]expectedGroup{group(0), group(0), group(1, 1, "active")}},
		{"A11", "qzxqzx", "tenant-a", "", nil, 25, 0,
			[]string{}, [3]expectedGroup{group(0), group(0), group(0)}},
		{"A12", "bearing", "tenant-a", "", map[string]any{"manufacturer": "Northstar"}, 1, 5,
			[]string{"PC-001"}, [3]expectedGroup{group(5, 4, "bearing", 1, "Bearing"), group(7, 5, "Northstar", 1, "Halden", 1, "northstar"), group(5, 4, "active", 1, "discontinued")}},
	}
}

func exampleEntries(t *testing.T) []adapter.ProjectedEntry {
	t.Helper()
	source, err := partscatalogdiscover.ExampleSource()
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSuffix(source, []byte("\n")), []byte("\n"))
	if len(lines) != 152 {
		t.Fatalf("source census %d", len(lines))
	}
	entries := make([]adapter.ProjectedEntry, 0, len(lines))
	for _, line := range lines {
		var record map[string]json.RawMessage
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatal(err)
		}
		delete(record, "keys")
		payload, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		projected, err := New().Project(payload)
		if err != nil || len(projected) != 1 {
			t.Fatalf("projection: %v", err)
		}
		entries = append(entries, projected[0])
	}
	return entries
}

func caseBytes(t *testing.T, row alternativeCase) ([]byte, []byte) {
	t.Helper()
	request := map[string]any{"query": row.query, "limit": row.limit}
	if row.partition != "" {
		request["partition_id"] = row.partition
	}
	if row.filters != nil {
		request["filters"] = row.filters
	}
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := json.Marshal(map[string]string{"tenant_id": row.tenant})
	if err != nil {
		t.Fatal(err)
	}
	return raw, scope
}

func completeEvidence(t *testing.T, entries []adapter.ProjectedEntry, plan adapter.CompiledPlan, row alternativeCase) []adapter.ProbeEvidence {
	t.Helper()
	items := make([]adapter.ProbeEvidence, 0, len(plan.Probes()))
	for _, probe := range plan.Probes() {
		candidates := make([]adapter.Candidate, 0)
		for _, entry := range entries {
			var record map[string]string
			if err := json.Unmarshal(entry.Payload(), &record); err != nil {
				t.Fatal(err)
			}
			if record["tenant_id"] != row.tenant || row.partition != "" && record["partition_id"] != row.partition ||
				!slices.Contains(entry.Keys(), probe.PhysicalKey()) {
				continue
			}
			candidate, err := adapter.NewCandidate(adapter.CandidateSpec{EntryID: entry.EntryID(), Score: 100,
				EchoedKey: probe.PhysicalKey(), Payload: entry.Payload()})
			if err != nil {
				t.Fatal(err)
			}
			candidates = append(candidates, candidate)
		}
		sort.Slice(candidates, func(i, j int) bool { return jsorder.Less(candidates[i].EntryID(), candidates[j].EntryID()) })
		item, err := adapter.NewProbeEvidence(adapter.ProbeEvidenceSpec{Probe: probe, RequestedPool: 1000, Candidates: candidates})
		if err != nil {
			t.Fatal(err)
		}
		items = append(items, item)
	}
	return items
}

func TestAcceptedOracleAndSupersetIdentity(t *testing.T) {
	entries := exampleEntries(t)
	semantics := New()
	for _, row := range acceptedCases() {
		t.Run(row.id, func(t *testing.T) {
			request, scope := caseBytes(t, row)
			plan, err := semantics.Compile(request, scope)
			if err != nil {
				t.Fatal(err)
			}
			view, err := parseRequestView(request)
			if err != nil {
				t.Fatal(err)
			}
			var original, superset map[string]any
			if err := json.Unmarshal(request, &original); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(view.superset, &superset); err != nil {
				t.Fatal(err)
			}
			delete(original, "filters")
			original["limit"] = float64(1000)
			originalJSON, _ := json.Marshal(original)
			supersetJSON, _ := json.Marshal(superset)
			if !bytes.Equal(originalJSON, supersetJSON) {
				t.Fatal("superset changed unrelated member")
			}
			supersetPlan, err := semantics.Compile(view.superset, scope)
			if err != nil || !samePlan(plan, supersetPlan) {
				t.Fatal("superset plan drift")
			}
			filter, err := semantics.CompileMembershipFilter(request, scope)
			if err != nil {
				t.Fatal(err)
			}
			wantFilter, err := partscatalogdiscover.New().CompileMembershipFilter(view.superset, scope)
			if err != nil || !bytes.Equal(filter.CanonicalJSON(), wantFilter.CanonicalJSON()) {
				t.Fatal("backend filter drift")
			}
			if len(filter.Caller()) > 1 {
				t.Fatal("facet terms leaked to backend")
			}
			evidence := completeEvidence(t, entries, plan, row)
			resolution, summary, err := semantics.ResolveWithAlternativeSummary(request, scope, plan, evidence)
			if err != nil {
				t.Fatal(err)
			}
			ids := make([]string, 0, len(resolution.Hits()))
			for _, hit := range resolution.Hits() {
				ids = append(ids, hit.EntryID())
			}
			if !slices.Equal(ids, row.ids) || summary.TotalMatches() != row.total {
				t.Fatalf("hits/total: %v %d", ids, summary.TotalMatches())
			}
			groups := summary.Groups()
			if len(groups) != 3 {
				t.Fatal("group census")
			}
			for index, name := range []string{"category", "manufacturer", "status"} {
				group, expected := groups[index], row.groups[index]
				if group.Name() != name || group.BasisTotal() != expected.basis || len(group.Buckets()) != len(expected.buckets) {
					t.Fatalf("%s basis/buckets: %d %v", name, group.BasisTotal(), group.Buckets())
				}
				sum := 0
				for position, bucket := range group.Buckets() {
					want := expected.buckets[position]
					if bucket.Count() != want.Count || bucket.Value() != want.Value {
						t.Fatalf("%s bucket %d: %d/%q", name, position, bucket.Count(), bucket.Value())
					}
					sum += bucket.Count()
				}
				if sum != expected.basis {
					t.Fatalf("%s sum %d", name, sum)
				}
			}
		})
	}
}

func TestRejectedOriginalGrammar(t *testing.T) {
	semantics := New()
	scope := []byte(`{"tenant_id":"tenant-a"}`)
	for _, raw := range []string{
		`{"query":"bearing","limit":1,"filters":{"category":null}}`,
		`{"query":"bearing","limit":1,"filters":{"category":[]}}`,
		`{"query":"bearing","limit":1,"filters":{"status":"unknown"}}`,
		`{"query":"bearing","limit":1001}`,
		`{"query":"bearing","limit":1,"unknown":1}`,
		`{"query":"bearing","limit":1,"filters":{"category":"a","category":"b"}}`,
		`{"query":"\ud800","limit":1}`,
	} {
		t.Run(strings.ReplaceAll(raw, "/", "_"), func(t *testing.T) {
			if _, err := semantics.Compile([]byte(raw), scope); err == nil {
				t.Fatal("invalid request compiled")
			}
			if _, err := semantics.CompileMembershipFilter([]byte(raw), scope); err == nil {
				t.Fatal("invalid request reached backend filter")
			}
		})
	}
}

func TestFrozenAlternativeArtifacts(t *testing.T) {
	first, err := GenerateArtifacts()
	if err != nil {
		t.Fatal(err)
	}
	second, err := GenerateArtifacts()
	if err != nil {
		t.Fatal(err)
	}
	if len(first.ResolvedResult) == 0 || !bytes.Equal(first.ResolvedResult, second.ResolvedResult) {
		t.Fatal("non-deterministic alternative result artifact")
	}
	shared, err := partscatalogdiscover.GenerateArtifacts()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.SourceProjection, shared.SourceProjection) ||
		!bytes.Equal(first.QueryCodec, shared.QueryCodec) || !bytes.Equal(first.CompiledPlan, shared.CompiledPlan) {
		t.Fatal("shared semantic component drift")
	}
}

func TestAlternativeDescriptorIdentity(t *testing.T) {
	descriptor, err := NewDescriptor(OperationalBinding{
		PhysicalListName: "parts-catalog-discover-v1",
		ConfigSHA256:     "49d8551b347da49135c49b13eed03d84a322a6496d34bf53428ab47ca3ee221d",
		TestedEngines:    []string{"v0.6.0-1-gf7423cd"}, TimeoutMillis: 30000,
		MaxInflight: 1, WorstCaseChargeBytes: 1 << 30,
	})
	if err != nil || !descriptor.Valid() {
		t.Fatalf("descriptor: %v", err)
	}
	if descriptor.Spec().Name != "parts-catalog-discover-facet-alternatives" {
		t.Fatal("route name")
	}
}

func syntheticCandidate(t *testing.T, probe adapter.Probe, family, index int) adapter.Candidate {
	t.Helper()
	identifier, name := "XA", fmt.Sprintf("other %d", index)
	if string(probe.Kind()) == "name-word" {
		identifier, name = fmt.Sprintf("ZZ-%d", index), "xa part"
	}
	if string(probe.Kind()) != "identifier-word" && string(probe.Kind()) != "name-word" {
		t.Fatalf("unexpected probe kind %s", probe.Kind())
	}
	payload, err := json.Marshal(map[string]string{
		"category": "gasket", "id": fmt.Sprintf("SYN-%d-%d", family, index),
		"identifier": identifier, "manufacturer": "Halden", "name": name,
		"status": "active", "tenant_id": "tenant-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	projected, err := New().Project(payload)
	if err != nil || len(projected) != 1 || !slices.Contains(projected[0].Keys(), probe.PhysicalKey()) {
		t.Fatalf("synthetic projection: %v", err)
	}
	candidate, err := adapter.NewCandidate(adapter.CandidateSpec{
		EntryID: projected[0].EntryID(), Score: 100, EchoedKey: probe.PhysicalKey(), Payload: payload,
	})
	if err != nil {
		t.Fatal(err)
	}
	return candidate
}

func TestCompleteSuperset999AndCeiling1000(t *testing.T) {
	semantics := New()
	request := []byte(`{"query":"xa","limit":1,"filters":{"manufacturer":"Halden"}}`)
	scope := []byte(`{"tenant_id":"tenant-a"}`)
	plan, err := semantics.Compile(request, scope)
	if err != nil {
		t.Fatal(err)
	}
	for _, total := range []int{999, 1000} {
		items := make([]adapter.ProbeEvidence, 0, len(plan.Probes()))
		for _, probe := range plan.Probes() {
			candidates := make([]adapter.Candidate, 0)
			switch string(probe.Kind()) {
			case "identifier-word":
				for index := 0; index < total/2; index++ {
					candidates = append(candidates, syntheticCandidate(t, probe, 1, index))
				}
			case "name-word":
				for index := 0; index < total-total/2; index++ {
					candidates = append(candidates, syntheticCandidate(t, probe, 2, index))
				}
			}
			item, err := adapter.NewProbeEvidence(adapter.ProbeEvidenceSpec{Probe: probe, RequestedPool: 1000, Candidates: candidates})
			if err != nil {
				t.Fatal(err)
			}
			items = append(items, item)
		}
		resolution, summary, err := semantics.ResolveWithAlternativeSummary(request, scope, plan, items)
		if total == 1000 {
			if !adapter.IsSummaryAtCeiling(err) || resolution.Valid() || summary.Valid() {
				t.Fatalf("1000 did not refuse completely: %v", err)
			}
		} else if err != nil || !resolution.Valid() || summary.TotalMatches() != 999 || len(resolution.Hits()) != 1 {
			t.Fatalf("999 failed: %v, total=%d, hits=%d", err, summary.TotalMatches(), len(resolution.Hits()))
		}
	}
}

func TestPayloadExactEscapingScalars(t *testing.T) {
	value := "x<>&\u2028\u2029x"
	request, err := json.Marshal(map[string]any{"query": "xa", "limit": 1,
		"filters": map[string]any{"category": value}})
	if err != nil {
		t.Fatal(err)
	}
	scope := []byte(`{"tenant_id":"tenant-a"}`)
	semantics := New()
	plan, err := semantics.Compile(request, scope)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(map[string]string{
		"category": value, "id": "SYN-SCALARS", "identifier": "XA",
		"manufacturer": "Halden", "name": "xa part", "status": "active", "tenant_id": "tenant-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	projected, err := semantics.Project(payload)
	if err != nil || len(projected) != 1 {
		t.Fatal(err)
	}
	evidence := make([]adapter.ProbeEvidence, 0, len(plan.Probes()))
	for _, probe := range plan.Probes() {
		var candidates []adapter.Candidate
		if slices.Contains(projected[0].Keys(), probe.PhysicalKey()) {
			candidate, err := adapter.NewCandidate(adapter.CandidateSpec{
				EntryID: "SYN-SCALARS", Score: 100, EchoedKey: probe.PhysicalKey(), Payload: payload,
			})
			if err != nil {
				t.Fatal(err)
			}
			candidates = []adapter.Candidate{candidate}
		}
		item, err := adapter.NewProbeEvidence(adapter.ProbeEvidenceSpec{Probe: probe, RequestedPool: 128, Candidates: candidates})
		if err != nil {
			t.Fatal(err)
		}
		evidence = append(evidence, item)
	}
	resolution, summary, err := semantics.ResolveWithAlternativeSummary(request, scope, plan, evidence)
	if err != nil || summary.TotalMatches() != 1 || len(resolution.Hits()) != 1 {
		t.Fatalf("scalar result: %v", err)
	}
	if group := summary.Groups()[0]; group.BasisTotal() != 1 || len(group.Buckets()) != 1 || group.Buckets()[0].Value() != value {
		t.Fatal("payload-exact bucket lost scalars")
	}
	var decoded map[string]string
	if err := json.Unmarshal(resolution.Hits()[0].Result(), &decoded); err != nil || decoded["category"] != value {
		t.Fatal("payload-exact hit lost scalars")
	}
}

func TestAlternativeLateInvalidEvidenceBeforeLimit(t *testing.T) {
	request := []byte(`{"query":"AX-100","partition_id":"north","limit":1,"filters":{"manufacturer":"Northstar"}}`)
	scope := []byte(`{"tenant_id":"tenant-a"}`)
	semantics := New()
	plan, err := semantics.Compile(request, scope)
	if err != nil {
		t.Fatal(err)
	}
	base := []byte(`{"id":"part-1","identifier":"AX-100","name":"Deep groove bearing","manufacturer":"Northstar","category":"bearing","status":"active","tenant_id":"tenant-a","partition_id":"north"}`)
	makeEvidence := func(payloads ...[]byte) []adapter.ProbeEvidence {
		t.Helper()
		out := make([]adapter.ProbeEvidence, len(plan.Probes()))
		for i, probe := range plan.Probes() {
			var candidates []adapter.Candidate
			if i < len(payloads) {
				candidate, err := adapter.NewCandidate(adapter.CandidateSpec{EntryID: "part-1", Score: 100, EchoedKey: probe.PhysicalKey(), Payload: payloads[i]})
				if err != nil {
					t.Fatal(err)
				}
				candidates = []adapter.Candidate{candidate}
			}
			out[i], err = adapter.NewProbeEvidence(adapter.ProbeEvidenceSpec{Probe: probe, RequestedPool: 128, Candidates: candidates})
			if err != nil {
				t.Fatal(err)
			}
		}
		return out
	}
	for _, change := range [][2]string{
		{"tenant-a", "tenant-b"}, {"north", "south"}, {"Northstar", "Vega"},
		{"bearing", "gasket"}, {"part-1", "part-2"},
	} {
		t.Run(change[0]+"-"+change[1], func(t *testing.T) {
			bad := bytes.Replace(base, []byte(change[0]), []byte(change[1]), 1)
			_, _, err := semantics.ResolveWithAlternativeSummary(request, scope, plan, makeEvidence(base, bad))
			var refusal *partscatalogdiscover.Error
			if !errors.As(err, &refusal) || refusal.Category != partscatalogdiscover.CategoryInvalidEvidence {
				t.Fatalf("late invalid candidate admitted: %v", err)
			}
		})
	}
	t.Run("duplicate-id-in-probe", func(t *testing.T) {
		evidence := makeEvidence(base)
		candidate := evidence[0].Candidates()[0]
		evidence[0], err = adapter.NewProbeEvidence(adapter.ProbeEvidenceSpec{Probe: plan.Probes()[0], RequestedPool: 128, Candidates: []adapter.Candidate{candidate, candidate}})
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := semantics.ResolveWithAlternativeSummary(request, scope, plan, evidence); err == nil {
			t.Fatal("duplicate admitted")
		}
	})
}
