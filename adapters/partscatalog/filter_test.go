package partscatalog

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/kurn-dev/kurn-search/adapter"
)

func TestFilterSubsetsAndProvenance(t *testing.T) {
	for mask := 0; mask < 8; mask++ {
		fields := map[string]string{}
		names := []string{"category", "manufacturer", "status"}
		values := []string{"bearing", "Northstar", "active"}
		for i, name := range names {
			if mask&(1<<i) != 0 {
				fields[name] = values[i]
			}
		}
		raw, _ := json.Marshal(map[string]any{"query": "AX-100", "partition_id": "north", "filters": fields})
		plan, err := New().CompileFilter(raw, []byte(`{"tenant_id":"tenant-a"}`))
		if err != nil {
			t.Fatal(err)
		}
		if len(plan.Trusted()) != 1 || plan.Trusted()[0].Name() != "tenant_id" || plan.Trusted()[0].Value() != "tenant-a" {
			t.Fatal("trusted provenance")
		}
		got := map[string]string{}
		var order []string
		for _, term := range plan.Caller() {
			got[term.Name()] = term.Value()
			order = append(order, term.Name())
		}
		if !slices.IsSorted(order) || got["partition_id"] != "north" || len(got) != len(fields)+1 {
			t.Fatal("caller census/order")
		}
		for name, value := range fields {
			if got[name] != value {
				t.Fatal("literal field")
			}
		}
	}
	for _, raw := range []string{`{"query":"AX-100"}`, `{"query":"AX-100","filters":{}}`} {
		p, err := New().CompileFilter([]byte(raw), []byte(`{"tenant_id":"tenant-a"}`))
		if err != nil || len(p.Caller()) != 0 || len(p.Trusted()) != 1 {
			t.Fatal("empty or omitted filter")
		}
	}
}

// These are pure constructed-evidence checks, not additional live fixture
// queries. Keep the frozen 152-record/24-query behavioral identity unchanged.
func TestFreeValuedFiltersRemainLiteralAndUnknownValuesResolveEmpty(t *testing.T) {
	_, entries := fixtureEntries(t)
	for _, tc := range []struct {
		name, field, value string
		want               []string
	}{
		{"manufacturer-known", "manufacturer", "Northstar", []string{"PC-001", "PC-003", "PC-004", "PC-010", "PC-006", "PC-012"}},
		{"manufacturer-unknown", "manufacturer", "Unknown", nil},
		{"manufacturer-leading-space", "manufacturer", " Northstar", nil},
		{"manufacturer-trailing-space", "manufacturer", "Northstar ", nil},
		{"category-known", "category", "bearing", []string{"PC-001", "PC-002", "PC-003", "PC-010", "PC-011", "PC-006"}},
		{"category-unknown", "category", "Unknown", nil},
		{"category-leading-space", "category", " bearing", nil},
		{"category-trailing-space", "category", "bearing ", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := fixtureQuery{
				ID: tc.name, Scope: fixtureScope{TenantID: "tenant-a"},
				Request: fixtureRequest{Query: "AX-100", Limit: 25, Filters: map[string]string{tc.field: tc.value}},
			}
			raw, scope, err := encodeFixtureQuery(q)
			if err != nil {
				t.Fatal(err)
			}
			filter, err := New().CompileFilter(raw, scope)
			if err != nil {
				t.Fatal(err)
			}
			terms := filter.Caller()
			if len(terms) != 1 || terms[0].Name() != tc.field || terms[0].Value() != tc.value {
				t.Fatal("free-valued filter was normalized or omitted")
			}
			plan, err := New().Compile(raw, scope)
			if err != nil {
				t.Fatal(err)
			}
			evidence, err := synthesizeEvidence(entries, plan, q, 128)
			if err != nil {
				t.Fatal(err)
			}
			result, err := New().Resolve(raw, scope, plan, evidence)
			if err != nil {
				t.Fatal(err)
			}
			var ids []string
			for _, hit := range result.Hits() {
				ids = append(ids, hit.EntryID())
			}
			if !result.Complete() || !slices.Equal(ids, tc.want) {
				t.Fatalf("got complete=%v IDs=%v, want %v", result.Complete(), ids, tc.want)
			}
		})
	}
}

func TestFilterGrammarAndBoundaries(t *testing.T) {
	scope := []byte(`{"tenant_id":"tenant-a"}`)
	invalid := []string{
		`{"query":"x","filters":null}`, `{"query":"x","filters":[]}`, `{"query":"x","filters":""}`,
		`{"query":"x","filters":12}`, `{"query":"x","filters":true}`,
		`{"query":"x","filters":{"unknown":"x"}}`, `{"query":"x","filters":{"tenant_id":"x"}}`,
		`{"query":"x","filters":{"partition_id":"x"}}`, `{"query":"x","tenant_id":"x"}`,
		`{"query":"x","filters":{"status":"ACTIVE"}}`, `{"query":"x","filters":{"status":" active"}}`,
		`{"query":"x","filters":{"manufacturer":""}}`, `{"query":"x","filters":{"manufacturer":null}}`,
		`{"query":"x","filters":{"manufacturer":true}}`, `{"query":"x","filters":{"manufacturer":1}}`,
		`{"query":"x","filters":{"manufacturer":[]}}`, `{"query":"x","filters":{"manufacturer":{"eq":"x"}}}`,
		`{"query":"x","filters":{"manufacturer":"a","manufacturer":"b"}}`,
		`{"query":"x","filters":{},"filters":{}}`,
		`{"query":"x","filters":{"manufacturer":"\ud800"}}`,
		`{"query":"x","filters":{"manufacturer":"\udc00"}}`,
		`{"query":"x","query":"y"}`, `{"query":"x","partition_id":null}`,
		`{"query":"x","limit":1.5}`, `{"query":"x","limit":"2"}`, `{"query":"x","limit":0}`,
		`{"query":"x","limit":1001}`, `{"query":"x"} true`,
	}
	for i, raw := range invalid {
		if _, err := New().CompileFilter([]byte(raw), scope); err == nil {
			t.Fatalf("refusal %d admitted", i)
		}
		if _, err := New().Compile([]byte(raw), scope); err == nil {
			t.Fatalf("compile refusal %d admitted", i)
		}
	}
	for _, value := range []string{" Northstar", "northstar", "Unknown", " ", strings.Repeat("😀", 512)} {
		raw, _ := json.Marshal(map[string]any{"query": "AX-100", "filters": map[string]string{"manufacturer": value}})
		p, err := New().CompileFilter(raw, scope)
		if err != nil || len(p.Caller()) != 1 || p.Caller()[0].Value() != value {
			t.Fatal("literal scalar preservation")
		}
	}
	over, _ := json.Marshal(map[string]any{"query": "x", "filters": map[string]string{"manufacturer": strings.Repeat("😀", 513)}})
	if _, err := New().CompileFilter(over, scope); err == nil {
		t.Fatal("513 scalars admitted")
	}
	for _, name := range []string{"tenant_id", "partition_id"} {
		for _, length := range []int{512, 513} {
			text := strings.Repeat("😀", length)
			request := []byte(`{"query":"AX-100"}`)
			trusted := scope
			if name == "tenant_id" {
				trusted, _ = json.Marshal(map[string]string{name: text})
			} else {
				request, _ = json.Marshal(map[string]any{"query": "AX-100", name: text})
			}
			_, err := New().CompileFilter(request, trusted)
			if (err == nil) != (length == 512) {
				t.Fatalf("%s scalar boundary %d", name, length)
			}
		}
	}
	for _, badScope := range []string{`{}`, `{"tenant_id":null}`, `{"tenant_id":""}`, `{"tenant_id":"a","tenant_id":"b"}`, `{"tenant_id":"a","status":"active"}`, `{"tenant_id":"\ud800"}`} {
		if _, err := New().CompileFilter([]byte(`{"query":"AX-100"}`), []byte(badScope)); err == nil {
			t.Fatal("invalid trusted scope")
		}
	}
	var _ adapter.FilterCompiler = New()
}
