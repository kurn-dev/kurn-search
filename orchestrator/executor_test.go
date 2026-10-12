package orchestrator_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kurn-dev/kurn-search/adapter"
	"github.com/kurn-dev/kurn-search/adapters/catalogid"
	"github.com/kurn-dev/kurn-search/backend/kurnhttp"
	"github.com/kurn-dev/kurn-search/orchestrator"
	"github.com/kurn-dev/kurn-search/profile"
)

const listName = "catalog-identifiers-v1"

var configSHA = strings.Repeat("c", 64)

type batchRequest struct {
	Checks []struct {
		Query  string            `json:"q"`
		Lists  []string          `json:"lists"`
		TopK   int               `json:"topk"`
		Filter map[string]string `json:"filter"`
	} `json:"checks"`
}

type responseSpec struct {
	status  int
	headers map[string]string
	body    any
}

type scriptedServer struct {
	server *httptest.Server
	calls  atomic.Int64
}

func newScriptedServer(t *testing.T, script func(int, batchRequest) responseSpec) *scriptedServer {
	t.Helper()
	fixture := &scriptedServer{}
	fixture.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := int(fixture.calls.Add(1))
		defer r.Body.Close()
		var request batchRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		response := script(call, request)
		for name, value := range response.headers {
			w.Header().Set(name, value)
		}
		w.Header().Set("Content-Type", "application/json")
		if response.status == 0 {
			response.status = http.StatusOK
		}
		w.WriteHeader(response.status)
		if err := json.NewEncoder(w).Encode(response.body); err != nil {
			t.Errorf("encode response: %v", err)
		}
	}))
	t.Cleanup(fixture.server.Close)
	return fixture
}

func (s *scriptedServer) client(t *testing.T) *kurnhttp.Client {
	t.Helper()
	client, err := kurnhttp.NewClient(kurnhttp.ClientConfig{
		BaseURL: s.server.URL, BearerToken: "test-key", MaxResponseBytes: 64 << 20,
		MaxRetryAfter: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func descriptor(t *testing.T) profile.Descriptor {
	t.Helper()
	descriptor, err := catalogid.NewDescriptor(catalogid.OperationalBinding{
		PhysicalListName: listName, ConfigSHA256: configSHA,
		TestedEngines: []string{"v0.5.3"}, TimeoutMillis: 10_000,
		MaxInflight: 1, WorstCaseChargeBytes: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	return descriptor
}

func stamp(base string, entries int) string {
	return strings.Repeat(base, 64) + fmt.Sprintf("@%d+j0+c%s", entries, configSHA)
}

func journalStamp(base string, entries int) string {
	return strings.Repeat(base, 64) + fmt.Sprintf("@%d+j1.%s+c%s", entries, strings.Repeat("d", 64), configSHA)
}

func success(request batchRequest, candidateCount int, servedStamp string) any {
	results := make([]any, len(request.Checks))
	for index, check := range request.Checks {
		candidates := make([]any, candidateCount)
		for candidateIndex := range candidates {
			id := fmt.Sprintf("entry-%04d", candidateIndex)
			candidates[candidateIndex] = map[string]any{
				"list": listName, "entry_id": id, "score": 100,
				"key": check.Query,
				"payload": map[string]any{
					"id": id, "identifier": "ABC-123", "tenant_id": "tenant-1",
					"partition_id": "partition-1",
				},
			}
		}
		results[index] = map[string]any{
			"candidates": candidates, "versions": map[string]string{listName: servedStamp},
			"filter":       check.Filter,
			"filter_stats": map[string]any{listName: map[string]int{"evaluated": candidateCount, "rejected": 0}},
			"took_us":      2,
		}
	}
	return map[string]any{"results": results, "took_us": 7}
}

func searchRequest() []byte {
	return []byte(`{"query":"abc","partition_id":"partition-1","limit":25}`)
}

func trustedScope() []byte { return []byte(`{"tenant_id":"tenant-1"}`) }

func newExecutor(t *testing.T, semantics orchestrator.RuntimeSemantics, backend orchestrator.Backend) *orchestrator.Executor {
	t.Helper()
	executor, err := orchestrator.New(descriptor(t), semantics, backend)
	if err != nil {
		t.Fatal(err)
	}
	return executor
}

func category(t *testing.T, err error) *orchestrator.Error {
	t.Helper()
	var target *orchestrator.Error
	if !errors.As(err, &target) {
		t.Fatalf("unexpected error type: %T %v", err, err)
	}
	return target
}

func TestSearchBindsFilterAndReturnsProducingRungMetadata(t *testing.T) {
	served := stamp("a", 1)
	fixture := newScriptedServer(t, func(_ int, request batchRequest) responseSpec {
		for _, check := range request.Checks {
			if check.TopK != 128 || check.Filter["tenant_id"] != "tenant-1" ||
				check.Filter["partition_id"] != "partition-1" || len(check.Filter) != 2 {
				t.Fatalf("unexpected bound check: %#v", check)
			}
		}
		return responseSpec{body: success(request, 1, served)}
	})
	result, err := newExecutor(t, catalogid.New(), fixture.client(t)).Search(context.Background(), searchRequest(), trustedScope())
	if err != nil {
		t.Fatal(err)
	}
	versions := result.ListVersions()
	if !result.Valid() || result.Pool() != 128 || result.PoolStep() != 1 || result.VersionAttempt() != 1 ||
		result.BackendTookUS() != 7 || len(versions) != 1 || versions[0].Name() != listName || versions[0].Stamp() != served ||
		result.AdapterName() != "catalog-identifier" || result.AdapterVersion() != "v1" || len(result.BehaviorSHA256()) != 64 ||
		len(result.Resolution().Hits()) != 1 {
		t.Fatalf("unexpected result metadata: %#v", result)
	}
}

func TestSearchTreatsRungsAsIndependentAndReportsJournalStamp(t *testing.T) {
	before, after := stamp("a", 128), journalStamp("a", 129)
	fixture := newScriptedServer(t, func(call int, request batchRequest) responseSpec {
		if call == 1 {
			return responseSpec{body: success(request, 128, before)}
		}
		return responseSpec{body: success(request, 1, after)}
	})
	result, err := newExecutor(t, catalogid.New(), fixture.client(t)).Search(context.Background(), searchRequest(), trustedScope())
	if err != nil {
		t.Fatal(err)
	}
	versions := result.ListVersions()
	if fixture.calls.Load() != 2 || result.Pool() != 256 || result.PoolStep() != 2 || result.VersionAttempt() != 1 ||
		len(versions) != 1 || versions[0].Stamp() != after || versions[0].Stamp() == before || !strings.Contains(versions[0].Stamp(), "+j1.") {
		t.Fatalf("cross-rung mutation was not proven: calls=%d result=%#v", fixture.calls.Load(), result)
	}
}

func TestSearchRestartsOnlyForCurrentBatchIdentityMismatch(t *testing.T) {
	good := stamp("a", 1)
	fixture := newScriptedServer(t, func(call int, request batchRequest) responseSpec {
		if call == 1 {
			bad := success(request, 1, good).(map[string]any)
			results := bad["results"].([]any)
			results[0].(map[string]any)["versions"] = map[string]string{listName: strings.Repeat("e", 64) + "@1+j0+c" + strings.Repeat("f", 64)}
			return responseSpec{body: bad}
		}
		return responseSpec{body: success(request, 1, good)}
	})
	result, err := newExecutor(t, catalogid.New(), fixture.client(t)).Search(context.Background(), searchRequest(), trustedScope())
	if err != nil {
		t.Fatal(err)
	}
	if fixture.calls.Load() != 2 || result.VersionAttempt() != 2 || result.PoolStep() != 1 {
		t.Fatalf("identity restart did not restart at rung one: calls=%d result=%#v", fixture.calls.Load(), result)
	}

	alwaysBad := newScriptedServer(t, func(_ int, request batchRequest) responseSpec {
		bad := success(request, 1, good).(map[string]any)
		results := bad["results"].([]any)
		results[0].(map[string]any)["versions"] = map[string]string{listName: "malformed"}
		return responseSpec{body: bad}
	})
	_, err = newExecutor(t, catalogid.New(), alwaysBad.client(t)).Search(context.Background(), searchRequest(), trustedScope())
	if got := category(t, err); got.Category != orchestrator.CategoryListIdentityMismatch || alwaysBad.calls.Load() != 2 {
		t.Fatalf("identity exhaustion mismatch: calls=%d error=%#v", alwaysBad.calls.Load(), got)
	}
}

func TestSearchFailsClosedAtFinalPoolCeiling(t *testing.T) {
	fixture := newScriptedServer(t, func(_ int, request batchRequest) responseSpec {
		return responseSpec{body: success(request, request.Checks[0].TopK, stamp("a", request.Checks[0].TopK))}
	})
	_, err := newExecutor(t, catalogid.New(), fixture.client(t)).Search(context.Background(), searchRequest(), trustedScope())
	if got := category(t, err); got.Category != orchestrator.CategoryIncompleteAtPoolCeiling || fixture.calls.Load() != 4 {
		t.Fatalf("pool ceiling mismatch: calls=%d error=%#v", fixture.calls.Load(), got)
	}
}

func TestSearchNeverRetriesInlineOrTopLevelOverload(t *testing.T) {
	inline := newScriptedServer(t, func(_ int, request batchRequest) responseSpec {
		results := make([]any, len(request.Checks))
		for index := range results {
			results[index] = map[string]string{"error": "secret backend prose"}
		}
		return responseSpec{body: map[string]any{"results": results, "took_us": 1}}
	})
	_, err := newExecutor(t, catalogid.New(), inline.client(t)).Search(context.Background(), searchRequest(), trustedScope())
	if got := category(t, err); got.Category != orchestrator.CategoryBackendInline || got.Retryable || inline.calls.Load() != 1 || strings.Contains(err.Error(), "secret") {
		t.Fatalf("inline policy mismatch: calls=%d error=%#v", inline.calls.Load(), got)
	}

	overload := newScriptedServer(t, func(_ int, _ batchRequest) responseSpec {
		return responseSpec{status: http.StatusServiceUnavailable, headers: map[string]string{"Retry-After": "2"}, body: map[string]string{"error": "overloaded"}}
	})
	_, err = newExecutor(t, catalogid.New(), overload.client(t)).Search(context.Background(), searchRequest(), trustedScope())
	if got := category(t, err); got.Category != orchestrator.CategoryBackendOverloaded || !got.Retryable || got.RetryAfter != 2*time.Second || overload.calls.Load() != 1 {
		t.Fatalf("overload policy mismatch: calls=%d error=%#v", overload.calls.Load(), got)
	}
}

type backendFunc func(context.Context, []kurnhttp.Probe) (kurnhttp.BatchResult, error)

func (f backendFunc) Batch(ctx context.Context, probes []kurnhttp.Probe) (kurnhttp.BatchResult, error) {
	return f(ctx, probes)
}

func TestEveryBackendCategoryMapsWithoutUnboundedRetry(t *testing.T) {
	tests := []struct {
		backend kurnhttp.Category
		want    orchestrator.Category
		calls   int
	}{
		{kurnhttp.CategoryInvalidRequest, orchestrator.CategoryInvalidInput, 1},
		{kurnhttp.CategoryTransportFailure, orchestrator.CategoryTransportFailure, 1},
		{kurnhttp.CategoryCanceled, orchestrator.CategoryCanceled, 1},
		{kurnhttp.CategoryDeadlineExceeded, orchestrator.CategoryDeadlineExceeded, 1},
		{kurnhttp.CategoryResponseTooLarge, orchestrator.CategoryResponseTooLarge, 1},
		{kurnhttp.CategoryBackendProtocol, orchestrator.CategoryBackendProtocol, 1},
		{kurnhttp.CategoryListIdentityMismatch, orchestrator.CategoryListIdentityMismatch, 2},
		{kurnhttp.CategoryFilterMismatch, orchestrator.CategoryFilterMismatch, 1},
		{kurnhttp.CategoryBackendInline, orchestrator.CategoryBackendInline, 1},
		{kurnhttp.CategoryBackendRateLimited, orchestrator.CategoryBackendRateLimited, 1},
		{kurnhttp.CategoryBackendOverloaded, orchestrator.CategoryBackendOverloaded, 1},
		{kurnhttp.CategoryBackendUnauthorized, orchestrator.CategoryBackendUnauthorized, 1},
		{kurnhttp.CategoryBackendForbidden, orchestrator.CategoryBackendForbidden, 1},
		{kurnhttp.CategoryBackendNotFound, orchestrator.CategoryBackendNotFound, 1},
		{kurnhttp.CategoryBackendConflict, orchestrator.CategoryBackendConflict, 1},
		{kurnhttp.CategoryBackendHTTP, orchestrator.CategoryBackendHTTP, 1},
		{kurnhttp.CategoryBackendRedirect, orchestrator.CategoryBackendRedirect, 1},
	}
	for _, test := range tests {
		t.Run(string(test.backend), func(t *testing.T) {
			calls := 0
			backend := backendFunc(func(context.Context, []kurnhttp.Probe) (kurnhttp.BatchResult, error) {
				calls++
				return kurnhttp.BatchResult{}, &kurnhttp.Error{
					Category: test.backend, Retryable: true, RetryAfter: 2 * time.Second,
				}
			})
			_, err := newExecutor(t, catalogid.New(), backend).Search(context.Background(), searchRequest(), trustedScope())
			got := category(t, err)
			if got.Category != test.want || calls != test.calls {
				t.Fatalf("mapping mismatch: calls=%d error=%#v", calls, got)
			}
			if test.backend != kurnhttp.CategoryListIdentityMismatch && (!got.Retryable || got.RetryAfter != 2*time.Second) {
				t.Fatalf("bounded retry hint was not retained: %#v", got)
			}
			if strings.Contains(got.Error(), "://") || strings.Contains(got.Error(), "tenant-1") {
				t.Fatalf("bounded error leaked input: %q", got.Error())
			}
		})
	}
}

func TestMalformedEvidenceAndFilterEchoFailClosed(t *testing.T) {
	served := stamp("a", 1)
	malformedPayload := newScriptedServer(t, func(_ int, request batchRequest) responseSpec {
		body := success(request, 1, served).(map[string]any)
		results := body["results"].([]any)
		for _, raw := range results {
			candidate := raw.(map[string]any)["candidates"].([]any)[0].(map[string]any)
			candidate["payload"] = map[string]any{
				"id": candidate["entry_id"], "identifier": "ABC-123", "tenant_id": "wrong-tenant",
				"partition_id": "partition-1",
			}
		}
		return responseSpec{body: body}
	})
	_, err := newExecutor(t, catalogid.New(), malformedPayload.client(t)).Search(context.Background(), searchRequest(), trustedScope())
	if got := category(t, err); got.Category != orchestrator.CategoryAdapterResolution || malformedPayload.calls.Load() != 1 {
		t.Fatalf("malformed payload was not refused: calls=%d error=%#v", malformedPayload.calls.Load(), got)
	}

	wrongFilter := newScriptedServer(t, func(_ int, request batchRequest) responseSpec {
		body := success(request, 1, served).(map[string]any)
		results := body["results"].([]any)
		results[0].(map[string]any)["filter"] = map[string]string{"tenant_id": "wrong"}
		return responseSpec{body: body}
	})
	_, err = newExecutor(t, catalogid.New(), wrongFilter.client(t)).Search(context.Background(), searchRequest(), trustedScope())
	if got := category(t, err); got.Category != orchestrator.CategoryFilterMismatch || wrongFilter.calls.Load() != 1 {
		t.Fatalf("filter mismatch was not refused: calls=%d error=%#v", wrongFilter.calls.Load(), got)
	}
}

func TestDeadlineIsDiscoverableAndNeverRetried(t *testing.T) {
	calls := 0
	backend := backendFunc(func(ctx context.Context, _ []kurnhttp.Probe) (kurnhttp.BatchResult, error) {
		calls++
		<-ctx.Done()
		return kurnhttp.BatchResult{}, ctx.Err()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := newExecutor(t, catalogid.New(), backend).Search(ctx, searchRequest(), trustedScope())
	if got := category(t, err); got.Category != orchestrator.CategoryDeadlineExceeded ||
		!errors.Is(err, context.DeadlineExceeded) || calls != 1 {
		t.Fatalf("deadline mismatch: calls=%d error=%#v", calls, got)
	}
}

type crossedSemantics struct{ catalogid.Semantics }

func (crossedSemantics) CompileFilter(_, _ []byte) (adapter.ExactFilterPlan, error) {
	term, _ := adapter.NewExactFilterTerm("tenant_id", "attacker")
	return adapter.NewExactFilterPlan(adapter.ExactFilterPlanSpec{Caller: []adapter.ExactFilterTerm{term}})
}

func TestInvalidInputAndFilterProvenancePerformZeroIO(t *testing.T) {
	fixture := newScriptedServer(t, func(_ int, _ batchRequest) responseSpec {
		t.Fatal("backend called for invalid input")
		return responseSpec{}
	})
	executor := newExecutor(t, catalogid.New(), fixture.client(t))
	_, err := executor.Search(context.Background(), []byte(`{"query":"abc","tenant_id":"attacker"}`), trustedScope())
	if category(t, err).Category != orchestrator.CategoryInvalidInput || fixture.calls.Load() != 0 {
		t.Fatal("invalid request reached backend")
	}
	executor = newExecutor(t, crossedSemantics{catalogid.New()}, fixture.client(t))
	_, err = executor.Search(context.Background(), searchRequest(), trustedScope())
	if category(t, err).Category != orchestrator.CategoryInvalidFilter || fixture.calls.Load() != 0 {
		t.Fatal("filter crossover reached backend")
	}
}

func TestCancellationIsDiscoverableAndNeverSent(t *testing.T) {
	fixture := newScriptedServer(t, func(_ int, _ batchRequest) responseSpec {
		t.Fatal("backend called after cancellation")
		return responseSpec{}
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := newExecutor(t, catalogid.New(), fixture.client(t)).Search(ctx, searchRequest(), trustedScope())
	if category(t, err).Category != orchestrator.CategoryCanceled || !errors.Is(err, context.Canceled) || fixture.calls.Load() != 0 {
		t.Fatalf("cancellation mismatch: %v", err)
	}
}

func TestPreflightRevalidatesRequestAndScopeWithoutIO(t *testing.T) {
	fixture := newScriptedServer(t, func(_ int, _ batchRequest) responseSpec {
		t.Fatal("preflight performed backend I/O")
		return responseSpec{}
	})
	executor := newExecutor(t, catalogid.New(), fixture.client(t))
	if err := executor.Preflight(context.Background(), searchRequest(), trustedScope()); err != nil {
		t.Fatal(err)
	}
	if fixture.calls.Load() != 0 {
		t.Fatal("valid preflight reached backend")
	}
	if err := executor.Preflight(context.Background(), searchRequest(), []byte(`{"tenant_id":"a","tenant_id":"b"}`)); category(t, err).Category != orchestrator.CategoryInvalidInput || fixture.calls.Load() != 0 {
		t.Fatal("invalid trusted scope did not fail before I/O")
	}
}
