package orchestrator

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

	"github.com/kurn-dev/kurn-search/adapters/catalogid"
	"github.com/kurn-dev/kurn-search/backend/kurnhttp"
	"github.com/kurn-dev/kurn-search/profile"
)

// Internal-package tests so the category mapping itself is asserted, not
// only its effect through Search. Helpers are self-contained (q3-prefixed);
// the backend is either an injected Backend func or an in-process httptest
// server. No external process is started.

const (
	q3List    = "catalog-identifiers-v1"
	q3Entries = 1000
)

var q3Config = strings.Repeat("c", 64)

func q3Stamp(base string, entries int) string {
	return strings.Repeat(base, 64) + fmt.Sprintf("@%d+j0+c%s", entries, q3Config)
}

func q3Served() string { return q3Stamp("a", q3Entries) }

func q3Descriptor(t *testing.T) profile.Descriptor {
	t.Helper()
	descriptor, err := catalogid.NewDescriptor(catalogid.OperationalBinding{
		PhysicalListName: q3List, ConfigSHA256: q3Config,
		TestedEngines: []string{"v0.5.3"}, TimeoutMillis: 10_000,
		MaxInflight: 1, WorstCaseChargeBytes: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	return descriptor
}

func q3Attempts(t *testing.T) int {
	t.Helper()
	attempts := q3Descriptor(t).Spec().Bounds.VersionAttempts
	if attempts < 2 {
		t.Fatalf("restart contrast needs VersionAttempts >= 2, got %d", attempts)
	}
	return attempts
}

func q3Search(t *testing.T, backend Backend) (Result, error) {
	t.Helper()
	executor, err := New(q3Descriptor(t), catalogid.New(), backend)
	if err != nil {
		t.Fatal(err)
	}
	return executor.Search(context.Background(),
		[]byte(`{"query":"abc","partition_id":"partition-1","limit":25}`), []byte(`{"tenant_id":"tenant-1"}`))
}

func q3Category(t *testing.T, err error) *Error {
	t.Helper()
	var target *Error
	if !errors.As(err, &target) {
		t.Fatalf("unexpected error type: %T %v", err, err)
	}
	return target
}

type q3Backend func(context.Context, []kurnhttp.Probe) (kurnhttp.BatchResult, error)

func (f q3Backend) Batch(ctx context.Context, probes []kurnhttp.Probe) (kurnhttp.BatchResult, error) {
	return f(ctx, probes)
}

type q3Request struct {
	Checks []struct {
		Query  string            `json:"q"`
		TopK   int               `json:"topk"`
		Filter map[string]string `json:"filter"`
	} `json:"checks"`
}

type q3Reply struct {
	header string // Kurn-Artifact-Verification value; "" omits the header
	served string
	basis  string // version_bases value; "" omits the member
	count  int    // candidates per check; -1 means pool-sized (incomplete)
}

// q3Server answers each Batch with the reply for that call number (1-based;
// the last reply repeats).
func q3Server(t *testing.T, replies ...q3Reply) (*kurnhttp.Client, *atomic.Int64) {
	t.Helper()
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := int(calls.Add(1))
		var request q3Request
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		reply := replies[min(call, len(replies))-1]
		results := make([]any, len(request.Checks))
		for index, check := range request.Checks {
			count := reply.count
			if count < 0 {
				count = check.TopK
			}
			candidates := make([]any, count)
			for i := range candidates {
				id := fmt.Sprintf("entry-%04d", i)
				candidates[i] = map[string]any{
					"list": q3List, "entry_id": id, "score": 100, "key": check.Query,
					"payload": map[string]any{"id": id, "identifier": "ABC-123", "tenant_id": "tenant-1", "partition_id": "partition-1"},
				}
			}
			result := map[string]any{
				"candidates": candidates, "versions": map[string]string{q3List: reply.served},
				"filter":       check.Filter,
				"filter_stats": map[string]any{q3List: map[string]int{"evaluated": count, "rejected": 0}},
				"took_us":      2,
			}
			if reply.basis != "" {
				result["version_bases"] = map[string]string{q3List: reply.basis}
			}
			results[index] = result
		}
		if reply.header != "" {
			w.Header().Set("Kurn-Artifact-Verification", reply.header)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"results": results, "took_us": 7})
	}))
	t.Cleanup(server.Close)
	client, err := kurnhttp.NewClient(kurnhttp.ClientConfig{
		BaseURL: server.URL, BearerToken: "test-key", MaxResponseBytes: 64 << 20,
		MaxRetryAfter: 5 * time.Second, Admission: kurnhttp.AdmissionPreparedFull,
		ExpectedBaseSHA256: strings.Repeat("a", 64), ExpectedEntries: q3Entries,
	})
	if err != nil {
		t.Fatal(err)
	}
	return client, &calls
}

func TestArtifactVerificationCategoryMapping(t *testing.T) {
	if got := mapBackendCategory(kurnhttp.CategoryArtifactVerificationMismatch); got != CategoryListIdentityMismatch {
		t.Fatalf("mapping = %q", got)
	}
	// The pre-existing mapping is unchanged.
	if mapBackendCategory(kurnhttp.CategoryListIdentityMismatch) != CategoryListIdentityMismatch ||
		mapBackendCategory(kurnhttp.CategoryBackendProtocol) != CategoryBackendProtocol ||
		mapBackendCategory(kurnhttp.Category("future_unknown")) != CategoryBackendProtocol {
		t.Fatal("existing mapping changed")
	}
	mapped := backendFailure(&kurnhttp.Error{Category: kurnhttp.CategoryArtifactVerificationMismatch, StatusCode: 200})
	if mapped.Category != CategoryListIdentityMismatch || mapped.Retryable || mapped.cause != nil {
		t.Fatalf("backendFailure = %#v", mapped)
	}
	for name, test := range map[string]struct {
		err  error
		want bool
	}{
		"direct":           {&kurnhttp.Error{Category: kurnhttp.CategoryArtifactVerificationMismatch}, true},
		"wrapped":          {fmt.Errorf("x: %w", &kurnhttp.Error{Category: kurnhttp.CategoryArtifactVerificationMismatch}), true},
		"legacy-identity":  {&kurnhttp.Error{Category: kurnhttp.CategoryListIdentityMismatch}, false},
		"protocol":         {&kurnhttp.Error{Category: kurnhttp.CategoryBackendProtocol}, false},
		"canceled":         {context.Canceled, false},
		"plain":            {errors.New("artifact_verification_mismatch"), false},
		"nil":              {nil, false},
		"orchestrator-err": {failure(CategoryListIdentityMismatch), false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := artifactVerificationRefusal(test.err); got != test.want {
				t.Fatalf("artifactVerificationRefusal = %t", got)
			}
		})
	}
	// No public category was added: the public spelling is the existing one.
	if CategoryListIdentityMismatch != "list_identity_mismatch" {
		t.Fatal("public category spelling changed")
	}
}

func TestArtifactVerificationRefusalReturnsWithoutRestart(t *testing.T) {
	for name, backendErr := range map[string]error{
		"direct":  &kurnhttp.Error{Category: kurnhttp.CategoryArtifactVerificationMismatch, StatusCode: 200, Retryable: true, RetryAfter: 2 * time.Second},
		"wrapped": fmt.Errorf("wrapped: %w", &kurnhttp.Error{Category: kurnhttp.CategoryArtifactVerificationMismatch}),
	} {
		t.Run(name, func(t *testing.T) {
			calls := 0
			_, err := q3Search(t, q3Backend(func(context.Context, []kurnhttp.Probe) (kurnhttp.BatchResult, error) {
				calls++
				return kurnhttp.BatchResult{}, backendErr
			}))
			got := q3Category(t, err)
			if got.Category != CategoryListIdentityMismatch || calls != 1 {
				t.Fatalf("policy refusal: calls=%d error=%#v", calls, got)
			}
			if got.Retryable || got.RetryAfter != 0 || got.Unwrap() != nil || got.Error() != "orchestrator: list_identity_mismatch" {
				t.Fatalf("policy refusal shape: %#v", got)
			}
		})
	}
}

func TestLegacyIdentityMismatchStillRestartsUpToVersionAttempts(t *testing.T) {
	attempts := q3Attempts(t)
	calls := 0
	_, err := q3Search(t, q3Backend(func(context.Context, []kurnhttp.Probe) (kurnhttp.BatchResult, error) {
		calls++
		return kurnhttp.BatchResult{}, &kurnhttp.Error{Category: kurnhttp.CategoryListIdentityMismatch}
	}))
	if got := q3Category(t, err); got.Category != CategoryListIdentityMismatch || calls != attempts {
		t.Fatalf("legacy identity exhaustion: calls=%d want %d error=%#v", calls, attempts, got)
	}

	// Legacy mismatch once (restart), then a policy refusal on the restarted
	// attempt (immediate). With VersionAttempts == 2 this asserts that the
	// legacy restart happened; the single-call tests assert no-restart.
	calls = 0
	_, err = q3Search(t, q3Backend(func(context.Context, []kurnhttp.Probe) (kurnhttp.BatchResult, error) {
		calls++
		if calls == 1 {
			return kurnhttp.BatchResult{}, &kurnhttp.Error{Category: kurnhttp.CategoryListIdentityMismatch}
		}
		return kurnhttp.BatchResult{}, &kurnhttp.Error{Category: kurnhttp.CategoryArtifactVerificationMismatch}
	}))
	if got := q3Category(t, err); got.Category != CategoryListIdentityMismatch || calls != 2 {
		t.Fatalf("restart then policy refusal: calls=%d error=%#v", calls, got)
	}
}

func TestPreparedClientAdmissionThroughExecutor(t *testing.T) {
	attempts := q3Attempts(t)
	for name, test := range map[string]struct {
		reply q3Reply
		calls int64
		ok    bool
	}{
		"full-verified":   {q3Reply{"full", q3Served(), "verified-base-id", 1}, 1, true},
		"full-empty":      {q3Reply{"full", q3Served(), "verified-base-id", 0}, 1, true},
		"trusted-header":  {q3Reply{"trusted", q3Served(), "verified-base-id", 1}, 1, false},
		"missing-header":  {q3Reply{"", q3Served(), "verified-base-id", 1}, 1, false},
		"declared-basis":  {q3Reply{"full", q3Served(), "declared-base-id", 1}, 1, false},
		"missing-bases":   {q3Reply{"full", q3Served(), "", 1}, 1, false},
		"empty-no-header": {q3Reply{"", q3Served(), "verified-base-id", 0}, 1, false},
		"other-base":      {q3Reply{"full", q3Stamp("b", q3Entries), "verified-base-id", 1}, 1, false},
		"other-entries":   {q3Reply{"full", q3Stamp("a", q3Entries+1), "verified-base-id", 1}, 1, false},
		"journal": {q3Reply{"full", strings.Repeat("a", 64) + fmt.Sprintf("@%d+j1.%s+c%s", q3Entries, strings.Repeat("d", 64), q3Config),
			"verified-base-id", 1}, 1, false},
		// A config mismatch keeps its existing classification and restart.
		"config-mismatch": {q3Reply{"full", strings.Repeat("a", 64) + fmt.Sprintf("@%d+j0+c%s", q3Entries, strings.Repeat("f", 64)),
			"verified-base-id", 1}, int64(attempts), false},
	} {
		t.Run(name, func(t *testing.T) {
			client, calls := q3Server(t, test.reply)
			result, err := q3Search(t, client)
			if test.ok {
				if err != nil || result.PoolStep() != 1 || result.VersionAttempt() != 1 || result.ListVersions()[0].Stamp() != test.reply.served {
					t.Fatalf("full evidence refused: %v", err)
				}
			} else if got := q3Category(t, err); got.Category != CategoryListIdentityMismatch || got.Retryable {
				t.Fatalf("refusal = %#v", got)
			}
			if calls.Load() != test.calls {
				t.Fatalf("calls = %d, want %d", calls.Load(), test.calls)
			}
		})
	}
}

func TestPreparedEveryRungIsAdmitted(t *testing.T) {
	// Rung one is fully verified but pool-sized (incomplete), so the ladder
	// moves on. A downgraded later rung refuses at once even though rung one
	// was fine and would have been discarded anyway.
	good := q3Reply{"full", q3Served(), "verified-base-id", -1}
	for name, downgrade := range map[string]q3Reply{
		"trusted-header":  {"trusted", q3Served(), "verified-base-id", 1},
		"declared-basis":  {"full", q3Served(), "declared-base-id", 1},
		"other-base":      {"full", q3Stamp("b", q3Entries), "verified-base-id", 1},
		"empty-no-header": {"", q3Served(), "", 0},
	} {
		t.Run(name, func(t *testing.T) {
			client, calls := q3Server(t, good, downgrade)
			_, err := q3Search(t, client)
			if got := q3Category(t, err); got.Category != CategoryListIdentityMismatch || calls.Load() != 2 {
				t.Fatalf("downgraded rung two: calls=%d error=%#v", calls.Load(), got)
			}
		})
	}
	// Rungs one and two good, rung three downgraded.
	client, calls := q3Server(t, good, good, q3Reply{"trusted", q3Served(), "verified-base-id", 1})
	if _, err := q3Search(t, client); q3Category(t, err).Category != CategoryListIdentityMismatch || calls.Load() != 3 {
		t.Fatalf("downgraded rung three: calls=%d err=%v", calls.Load(), err)
	}
	// Contrast: two verified rungs, rung two produces the result.
	client, calls = q3Server(t, good, q3Reply{"full", q3Served(), "verified-base-id", 1})
	result, err := q3Search(t, client)
	if err != nil || calls.Load() != 2 || result.PoolStep() != 2 || result.VersionAttempt() != 1 {
		t.Fatalf("two verified rungs: calls=%d err=%v", calls.Load(), err)
	}
}

func TestPreparedNon200KeepsExistingCategory(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "2")
		w.Header().Set("Kurn-Artifact-Verification", "trusted")
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprint(w, `{"error":"overloaded"}`)
	}))
	t.Cleanup(server.Close)
	client, err := kurnhttp.NewClient(kurnhttp.ClientConfig{
		BaseURL: server.URL, BearerToken: "test-key", MaxResponseBytes: 1 << 20, MaxRetryAfter: 5 * time.Second,
		Admission: kurnhttp.AdmissionPreparedFull, ExpectedBaseSHA256: strings.Repeat("a", 64), ExpectedEntries: q3Entries,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = q3Search(t, client)
	if got := q3Category(t, err); got.Category != CategoryBackendOverloaded || !got.Retryable || got.RetryAfter != 2*time.Second || calls.Load() != 1 {
		t.Fatalf("non-200 under prepared policy: calls=%d error=%#v", calls.Load(), got)
	}
}
