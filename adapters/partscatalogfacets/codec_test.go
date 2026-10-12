package partscatalogfacets

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"

	"github.com/kurn-dev/kurn-search/adapter"
	"github.com/kurn-dev/kurn-search/adapters/partscatalogdiscover"
)

// oracleRequests returns the raw request bytes of every frozen oracle query
// plus the refusal-matrix shapes.
func oracleRequests(t *testing.T) [][]byte {
	t.Helper()
	fixture, err := loadOracle()
	if err != nil {
		t.Fatal(err)
	}
	requests := make([][]byte, 0, len(fixture.Queries))
	for _, query := range fixture.Queries {
		raw, _, err := encodeOracleRequest(query)
		if err != nil {
			t.Fatal(err)
		}
		requests = append(requests, raw)
	}
	return requests
}

// TestSubstitutionIdentity is the R4 gate: for every frozen oracle request the
// substituted document parses to the identical request in every member except
// limit, and the delegate compiles both to the identical probe plan.
func TestSubstitutionIdentity(t *testing.T) {
	for _, raw := range oracleRequests(t) {
		original, err := parseRequestView(raw)
		if err != nil {
			t.Fatalf("oracle request refused: %v", err)
		}
		substituted := substituteLimit(original)
		again, err := parseRequestView(substituted)
		if err != nil {
			t.Fatalf("substituted request refused: %v", err)
		}
		if again.limit != adapter.MaxHits {
			t.Fatalf("substituted limit is %d", again.limit)
		}
		if len(again.members) != len(original.members)+1 && len(again.members) != len(original.members) {
			t.Fatal("substitution changed the member census")
		}
		for name, value := range original.members {
			if name == "limit" {
				continue
			}
			var before, after any
			if err := json.Unmarshal(value, &before); err != nil {
				t.Fatalf("original member %s undecodable: %v", name, err)
			}
			if err := json.Unmarshal(again.members[name], &after); err != nil {
				t.Fatalf("substituted member %s undecodable: %v", name, err)
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatalf("member %s changed by substitution", name)
			}
		}
		delegate := partscatalogdiscover.New()
		planOriginal, err := delegate.Compile(raw, mustScope(t))
		if err != nil {
			t.Fatalf("delegate refused original: %v", err)
		}
		planSubstituted, err := delegate.Compile(substituted, mustScope(t))
		if err != nil {
			t.Fatalf("delegate refused substituted: %v", err)
		}
		if !slices.Equal(planKeys(planOriginal), planKeys(planSubstituted)) {
			t.Fatal("substitution changed the compiled plan")
		}
	}
}

func planKeys(plan adapter.CompiledPlan) []string {
	keys := make([]string, 0, len(plan.Probes()))
	for _, probe := range plan.Probes() {
		keys = append(keys, string(probe.Kind())+"\x00"+probe.PhysicalKey())
	}
	return keys
}

func mustScope(t *testing.T) []byte {
	t.Helper()
	return []byte(`{"tenant_id":"tenant-a"}`)
}

func TestSubstitutionRefusalMatrix(t *testing.T) {
	cases := map[string]string{
		"unknown member":  `{"query":"ball","facet":"category"}`,
		"missing query":   `{"limit":5}`,
		"trailing data":   `{"query":"ball"} {}`,
		"empty":           ``,
		"array":           `["ball"]`,
		"duplicate query": `{"query":"ball","query":"ball"}`,
		"limit zero":      `{"query":"ball","limit":0}`,
		"limit over":      `{"query":"ball","limit":1001}`,
		"limit string":    `{"query":"ball","limit":"25"}`,
		"limit fraction":  `{"query":"ball","limit":2.5}`,
	}
	for name, raw := range cases {
		if _, err := parseRequestView([]byte(raw)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// Nested duplicates inside filters are refused by the delegate's own strict
	// parser after substitution, keeping classification identical.
	view, err := parseRequestView([]byte(`{"query":"ball","filters":{"manufacturer":"Vega","manufacturer":"Halden"}}`))
	if err == nil {
		delegate := partscatalogdiscover.New()
		if _, err := delegate.Compile(substituteLimit(view), mustScope(t)); err == nil {
			t.Error("nested duplicate filter member accepted by delegate")
		}
	}
}
