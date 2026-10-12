package service

import (
	"bytes"
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kurn-dev/kurn-search/adapter"
	"github.com/kurn-dev/kurn-search/adapters/catalogid"
	"github.com/kurn-dev/kurn-search/adapters/partscatalogfacets"
	"github.com/kurn-dev/kurn-search/backend/kurnhttp"
	"github.com/kurn-dev/kurn-search/orchestrator"
	"github.com/kurn-dev/kurn-search/profile"
)

type facetBackendFunc func(context.Context, []kurnhttp.Probe) (kurnhttp.BatchResult, error)

func (f facetBackendFunc) Batch(ctx context.Context, probes []kurnhttp.Probe) (kurnhttp.BatchResult, error) {
	return f(ctx, probes)
}

var unreachableBackend = facetBackendFunc(func(context.Context, []kurnhttp.Probe) (kurnhttp.BatchResult, error) {
	return kurnhttp.BatchResult{}, errors.New("unreachable")
})

func facetDescriptor(t *testing.T) profile.Descriptor {
	t.Helper()
	descriptor, err := partscatalogfacets.NewDescriptor(partscatalogfacets.OperationalBinding{
		PhysicalListName: "parts-catalog-discover-v1", ConfigSHA256: strings.Repeat("c", 64),
		TestedEngines: []string{"v0.5.3"}, TimeoutMillis: 10_000, MaxInflight: 1, WorstCaseChargeBytes: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	return descriptor
}

func realFacetExecutor(t *testing.T) *orchestrator.Executor {
	t.Helper()
	executor, err := orchestrator.NewWithFacets(facetDescriptor(t), partscatalogfacets.New(), unreachableBackend)
	if err != nil {
		t.Fatal(err)
	}
	return executor
}

func ordinaryExecutor(t *testing.T) *orchestrator.Executor {
	t.Helper()
	executor, err := orchestrator.New(testDescriptor(t, time.Second), catalogid.New(), unreachableBackend)
	if err != nil {
		t.Fatal(err)
	}
	return executor
}

func TestFacetPairingConstructorsAndRegistry(t *testing.T) {
	if _, err := WrapExecutor(realFacetExecutor(t)); err == nil {
		t.Fatal("ordinary wrapper accepted a facet executor")
	}
	if _, err := WrapFacetExecutor(ordinaryExecutor(t)); err == nil {
		t.Fatal("facet wrapper accepted an ordinary executor")
	}
	facetRunner, err := WrapFacetExecutor(realFacetExecutor(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewFacetBinding(profile.Descriptor{}, facetRunner); err == nil {
		t.Fatal("facet binding accepted an invalid descriptor")
	}
	binding, err := NewFacetBinding(facetDescriptor(t), facetRunner)
	if err != nil {
		t.Fatal(err)
	}
	if !binding.facets {
		t.Fatal("facet binding lost its discriminator")
	}
	if _, err := newRegistry([]Binding{binding}, 0); err != nil {
		t.Fatalf("facet pairing refused at registry: %v", err)
	}
	if _, err := newRegistry([]Binding{{Descriptor: facetDescriptor(t), Runner: facetRunner}}, 0); err == nil {
		t.Fatal("registry accepted a facet runner under an ordinary binding")
	}
	if _, err := newRegistry([]Binding{{Descriptor: facetDescriptor(t), Runner: &fakeRunner{}, facets: true}}, 0); err == nil {
		t.Fatal("registry accepted an ordinary runner marked facet-expected")
	}
}

func validTestSummary(t *testing.T, total int) adapter.Summary {
	t.Helper()
	var buckets []adapter.SummaryBucketSpec
	if total > 0 {
		buckets = []adapter.SummaryBucketSpec{{Count: total, Value: "gasket"}}
	}
	var manufacturer, status []adapter.SummaryBucketSpec
	if total > 0 {
		manufacturer = []adapter.SummaryBucketSpec{{Count: total, Value: "Halden"}}
		status = []adapter.SummaryBucketSpec{{Count: total, Value: "active"}}
	}
	summary, err := adapter.NewSummary(adapter.SummarySpec{TotalMatches: total, Groups: []adapter.SummaryGroupSpec{
		{Name: "category", Buckets: buckets},
		{Name: "manufacturer", Buckets: manufacturer},
		{Name: "status", Buckets: status},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return summary
}

func testExecutionWithSummary(t *testing.T, summary adapter.Summary) Execution {
	execution, err := testExecution()
	if err != nil {
		t.Fatal(err)
	}
	execution.summary = summary
	return execution
}

func TestEncodeSuccessFacetShape(t *testing.T) {
	execution := testExecutionWithSummary(t, validTestSummary(t, 1))
	body, err := encodeSuccess(execution, 9, 64<<20)
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range [][]byte{
		[]byte(`"total_matches":1`), []byte(`"facets":{`),
		[]byte(`"category":[{"count":1,"value":"gasket"}]`),
		[]byte(`"manufacturer":[{"count":1,"value":"Halden"}]`),
		[]byte(`"status":[{"count":1,"value":"active"}]`),
	} {
		if !bytes.Contains(body, fragment) {
			t.Fatalf("envelope lacks %s: %s", fragment, body)
		}
	}
	if bytes.Index(body, []byte(`"total_matches"`)) > bytes.Index(body, []byte(`"hits"`)) {
		t.Fatal("total_matches must precede hits")
	}
	if bytes.Index(body, []byte(`"facets"`)) < bytes.Index(body, []byte(`"hits"`)) {
		t.Fatal("facets must follow hits")
	}
	zeroHit, err := testZeroHitExecution()
	if err != nil {
		t.Fatal(err)
	}
	zeroHit.summary = validTestSummary(t, 0)
	body, err = encodeSuccess(zeroHit, 9, 64<<20)
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range [][]byte{
		[]byte(`"total_matches":0`),
		[]byte(`"category":[]`), []byte(`"manufacturer":[]`), []byte(`"status":[]`),
	} {
		if !bytes.Contains(body, fragment) {
			t.Fatalf("zero-match envelope lacks %s: %s", fragment, body)
		}
	}
}

func TestEncodeSuccessOldRouteHasNoFacetMembers(t *testing.T) {
	execution, err := testExecution()
	if err != nil {
		t.Fatal(err)
	}
	body, err := encodeSuccess(execution, 9, 64<<20)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(body, []byte(`"total_matches"`)) || bytes.Contains(body, []byte(`"facets"`)) {
		t.Fatalf("ordinary execution emitted facet members: %s", body)
	}
}

func TestOrdinaryBindingSummaryExecutionRefused(t *testing.T) {
	summary := validTestSummary(t, 1)
	runner := &fakeRunner{run: func(context.Context, []byte, []byte) (Execution, error) {
		return testExecutionWithSummary(t, summary), nil
	}}
	service := testService(t, runner, 2*time.Second, 50*time.Millisecond)
	recorder := httptest.NewRecorder()
	service.ServeHTTP(recorder, request(`{"query":"item","limit":1}`))
	if recorder.Code != 502 {
		t.Fatalf("summary on an ordinary route: got %d %s", recorder.Code, recorder.Body)
	}
}

func TestFacetCeilingMapsTo422(t *testing.T) {
	status, category, retry := mapExecutionError(&orchestrator.Error{Category: orchestrator.CategoryIncompleteAtPoolCeiling})
	if status != 422 || category != "incomplete_at_pool_ceiling" || retry != 0 {
		t.Fatalf("mapping: %d %s %v", status, category, retry)
	}
}

// TestFacetRunnerRefusesSummaryLessSuccess proves the runner-side bidirectional
// enforcement: a facet resolution without a summary is a service failure.
func TestFacetRunnerRefusesSummaryLessSuccess(t *testing.T) {
	executor := realFacetExecutor(t)
	runner, err := WrapFacetExecutor(executor)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Run(context.Background(), []byte(`{"query":"sprocket"}`), []byte(`{"tenant_id":"tenant-a"}`)); err == nil {
		t.Fatal("summary-less facet resolution accepted")
	}
}
