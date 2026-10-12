package service

import (
	"bytes"
	"testing"

	"github.com/kurn-dev/kurn-search/adapter"
	"github.com/kurn-dev/kurn-search/adapters/partscatalogalternatives"
	"github.com/kurn-dev/kurn-search/orchestrator"
)

func alternativeDescriptor(t *testing.T) partscatalogalternatives.OperationalBinding {
	t.Helper()
	return partscatalogalternatives.OperationalBinding{
		PhysicalListName: "parts-catalog-discover-v1", ConfigSHA256: testConfig,
		TestedEngines: []string{"v0.5.3"}, TimeoutMillis: 10000, MaxInflight: 1,
		WorstCaseChargeBytes: 1 << 30,
	}
}

func TestAlternativePairing(t *testing.T) {
	descriptor, err := partscatalogalternatives.NewDescriptor(alternativeDescriptor(t))
	if err != nil {
		t.Fatal(err)
	}
	executor, err := orchestrator.NewWithAlternatives(descriptor, partscatalogalternatives.New(), unreachableBackend)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := WrapExecutor(executor); err == nil {
		t.Fatal("ordinary wrapper accepted alternatives")
	}
	if _, err := WrapFacetExecutor(executor); err == nil {
		t.Fatal("facet wrapper accepted alternatives")
	}
	runner, err := WrapAlternativeExecutor(executor)
	if err != nil {
		t.Fatal(err)
	}
	binding, err := NewAlternativeBinding(descriptor, runner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := newRegistry([]Binding{binding}, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := newRegistry([]Binding{{Descriptor: descriptor, Runner: runner}}, 0); err == nil {
		t.Fatal("registry accepted unmarked alternative runner")
	}
	if _, err := newRegistry([]Binding{{Descriptor: descriptor, Runner: runner, facets: true, alternatives: true}}, 0); err == nil {
		t.Fatal("registry accepted two modes")
	}
}

func TestAlternativeSuccessShape(t *testing.T) {
	summary, err := adapter.NewAlternativeSummary(adapter.AlternativeSummarySpec{
		TotalMatches: 0, Groups: []adapter.AlternativeSummaryGroupSpec{
			{Name: "category", BasisTotal: 0},
			{Name: "manufacturer", BasisTotal: 1, Buckets: []adapter.SummaryBucketSpec{{Count: 1, Value: "Northstar"}}},
			{Name: "status", BasisTotal: 0},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	execution, err := testZeroHitExecution()
	if err != nil {
		t.Fatal(err)
	}
	execution.alternativeSummary = summary
	body, err := encodeAlternativeSuccess(execution, 9, 64<<20)
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range [][]byte{
		[]byte(`"total_matches":0`),
		[]byte(`"category":{"basis_total":0,"buckets":[]}`),
		[]byte(`"manufacturer":{"basis_total":1,"buckets":[{"count":1,"value":"Northstar"}]}`),
		[]byte(`"status":{"basis_total":0,"buckets":[]}`),
	} {
		if !bytes.Contains(body, fragment) {
			t.Fatalf("missing %s in %s", fragment, body)
		}
	}
	if _, err := encodeAlternativeSuccess(execution, 9, len(body)-1); err == nil {
		t.Fatal("oversized success accepted")
	}
	if ids, err := readinessHitIDs(body, true); err != nil || len(ids) != 0 {
		t.Fatal("readiness rejected valid alternative success")
	}
	execution.summary = validTestSummary(t, 0)
	if _, err := encodeAlternativeSuccess(execution, 9, 64<<20); err == nil {
		t.Fatal("dual summary accepted")
	}
}
