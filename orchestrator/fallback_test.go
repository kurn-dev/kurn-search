package orchestrator_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kurn-dev/kurn-search/adapters/partscatalogsuggest"
	"github.com/kurn-dev/kurn-search/backend/kurnhttp"
	"github.com/kurn-dev/kurn-search/orchestrator"
)

const suggestList = "parts-catalog-suggest-v1"

type anyOfCheck struct {
	Query      string          `json:"q"`
	ExactAnyOf []string        `json:"exact_any_of"`
	Lists      []string        `json:"lists"`
	Threshold  *float64        `json:"threshold"`
	TopK       int             `json:"topk"`
	Filter     json.RawMessage `json:"filter"`
}

type anyOfRequest struct {
	Checks []anyOfCheck `json:"checks"`
}

// suggestServer scripts one check per batch for the suggest recipe: the
// script returns the candidates and the stamp to serve.
type suggestServer struct {
	server *httptest.Server
	calls  atomic.Int64
	seen   []anyOfCheck
}

func newSuggestServer(t *testing.T, script func(call int, check anyOfCheck) ([]any, string)) *suggestServer {
	t.Helper()
	s := &suggestServer{}
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := int(s.calls.Add(1))
		var request anyOfRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || len(request.Checks) != 1 {
			t.Errorf("decode: %v", err)
			return
		}
		check := request.Checks[0]
		s.seen = append(s.seen, check)
		candidates, stamp := script(call, check)
		if candidates == nil {
			candidates = []any{}
		}
		body := map[string]any{"results": []any{map[string]any{
			"candidates": candidates, "versions": map[string]string{suggestList: stamp},
			"filter": check.Filter, "filter_stats": map[string]any{suggestList: map[string]int{"evaluated": len(candidates), "rejected": 0}},
			"took_us": 2,
		}}, "took_us": call}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(s.server.Close)
	return s
}

func suggestCandidate(id, identifier string) any {
	return map[string]any{"list": suggestList, "entry_id": id, "score": 100, "key": identifier,
		"payload": map[string]any{"id": id, "identifier": identifier, "manufacturer": "Northstar", "category": "bearing", "status": "active", "tenant_id": "tenant-a"}}
}

func suggestExecutor(t *testing.T, s *suggestServer) *orchestrator.Executor {
	t.Helper()
	descriptor, err := partscatalogsuggest.NewDescriptor(partscatalogsuggest.OperationalBinding{
		PhysicalListName: suggestList, ConfigSHA256: configSHA, TestedEngines: []string{"v0.6.0-1-gf7423cd"},
		TimeoutMillis: 10_000, MaxInflight: 1, WorstCaseChargeBytes: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	client, err := kurnhttp.NewClient(kurnhttp.ClientConfig{BaseURL: s.server.URL, BearerToken: "k", MaxResponseBytes: 1 << 20, MaxRetryAfter: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	executor, err := orchestrator.NewWithMembershipFallback(descriptor, partscatalogsuggest.New(), client)
	if err != nil {
		t.Fatal(err)
	}
	return executor
}

func TestFallbackStepRunsOnEmptySimilarityEvidence(t *testing.T) {
	served := stamp("a", 152)
	s := newSuggestServer(t, func(call int, check anyOfCheck) ([]any, string) {
		if call == 1 {
			return nil, served
		}
		return []any{suggestCandidate("PC-001", "AX-100"), suggestCandidate("PC-002", "AX-100")}, served
	})
	result, err := suggestExecutor(t, s).Search(context.Background(), []byte(`{"query":"XA-100","limit":25}`), []byte(`{"tenant_id":"tenant-a"}`))
	if err != nil {
		t.Fatal(err)
	}
	if s.calls.Load() != 2 || len(s.seen) != 2 {
		t.Fatalf("calls %d", s.calls.Load())
	}
	first, second := s.seen[0], s.seen[1]
	if first.Query != "XA-100" || first.ExactAnyOf != nil || first.Threshold == nil || *first.Threshold != 0.65 || first.TopK != 128 {
		t.Fatalf("similarity check: %+v", first)
	}
	if second.Query != "" || strings.Join(second.ExactAnyOf, ",") != "AX-100,XA-010" || second.Threshold != nil || second.TopK != 128 ||
		!bytes.Equal(second.Filter, first.Filter) || len(second.Filter) == 0 {
		t.Fatalf("fallback check: %+v", second)
	}
	hits := result.Resolution().Hits()
	if len(hits) != 2 || hits[0].EntryID() != "PC-001" || !bytes.Contains(hits[0].Evidence(), []byte(`"transposition": "yes"`)) ||
		result.BackendTookUS() != 3 || result.Pool() != 128 || result.PoolStep() != 1 || result.VersionAttempt() != 1 ||
		!result.FallbackTaken() || result.FallbackHits() != 2 || result.FallbackCalls() != 1 ||
		len(result.ListVersions()) != 1 || result.ListVersions()[0].Stamp() != served {
		t.Fatalf("result: %+v hits %d took %d", result, len(hits), result.BackendTookUS())
	}
}

func TestFallbackStepNotTakenBesideCandidatesOrWithoutVariants(t *testing.T) {
	served := stamp("a", 152)
	s := newSuggestServer(t, func(call int, check anyOfCheck) ([]any, string) {
		return []any{suggestCandidate("PC-001", "AX-100")}, served
	})
	result, err := suggestExecutor(t, s).Search(context.Background(), []byte(`{"query":"AX-100","limit":25}`), []byte(`{"tenant_id":"tenant-a"}`))
	if err != nil || s.calls.Load() != 1 || len(result.Resolution().Hits()) != 1 || bytes.Contains(result.Resolution().Hits()[0].Evidence(), []byte("transposition")) {
		t.Fatalf("similarity hit: %v calls %d", err, s.calls.Load())
	}
	empty := newSuggestServer(t, func(call int, check anyOfCheck) ([]any, string) { return nil, served })
	result, err = suggestExecutor(t, empty).Search(context.Background(), []byte(`{"query":"Z","limit":25}`), []byte(`{"tenant_id":"tenant-a"}`))
	if err != nil || empty.calls.Load() != 1 || len(result.Resolution().Hits()) != 0 {
		t.Fatalf("no variants: %v calls %d", err, empty.calls.Load())
	}
}

func TestFallbackStepRestartsOnVersionChangeBetweenSteps(t *testing.T) {
	before, after := stamp("a", 152), stamp("b", 153)
	s := newSuggestServer(t, func(call int, check anyOfCheck) ([]any, string) {
		switch call {
		case 1:
			return nil, before
		case 2:
			return []any{suggestCandidate("PC-001", "AX-100")}, after // swapped under the attempt
		case 3:
			return nil, after
		default:
			return []any{suggestCandidate("PC-001", "AX-100")}, after
		}
	})
	result, err := suggestExecutor(t, s).Search(context.Background(), []byte(`{"query":"XA-100","limit":25}`), []byte(`{"tenant_id":"tenant-a"}`))
	if err != nil {
		t.Fatal(err)
	}
	if s.calls.Load() != 4 || result.VersionAttempt() != 2 || result.PoolStep() != 1 || result.ListVersions()[0].Stamp() != after || len(result.Resolution().Hits()) != 1 || result.FallbackCalls() != 2 {
		t.Fatalf("restart: calls %d attempt %d stamp %s", s.calls.Load(), result.VersionAttempt(), result.ListVersions()[0].Stamp())
	}
}

func TestFallbackStepRefusesForeignEvidence(t *testing.T) {
	served := stamp("a", 152)
	s := newSuggestServer(t, func(call int, check anyOfCheck) ([]any, string) {
		if call == 1 {
			return nil, served
		}
		return []any{suggestCandidate("PC-006", "AX-101")}, served // not a variant of XA-100
	})
	_, err := suggestExecutor(t, s).Search(context.Background(), []byte(`{"query":"XA-100","limit":25}`), []byte(`{"tenant_id":"tenant-a"}`))
	if got := category(t, err); got.Category != orchestrator.CategoryAdapterResolution {
		t.Fatalf("foreign fallback hit: %#v", got)
	}
}

// Under the opt-in osa1 policy the fallback runs when no first-pass
// candidate is accepted by the verifier, even though the raw pool is not
// empty; the service sees the fallback flags on the result.
func TestFallbackStepUnderOSA1PolicyOnRejectedPool(t *testing.T) {
	served := stamp("a", 152)
	s := newSuggestServer(t, func(call int, check anyOfCheck) ([]any, string) {
		if call == 1 {
			return []any{map[string]any{"list": suggestList, "entry_id": "PC-012", "score": 66, "key": "AX-103",
				"payload": map[string]any{"id": "PC-012", "identifier": "AX-103", "manufacturer": "Northstar", "category": "bearing", "status": "active", "tenant_id": "tenant-a"}}}, served
		}
		return []any{suggestCandidate("PC-001", "AX-100")}, served
	})
	descriptor, err := partscatalogsuggest.NewDescriptor(partscatalogsuggest.OperationalBinding{
		PhysicalListName: suggestList, ConfigSHA256: configSHA, Policy: partscatalogsuggest.PolicyOSA1, TestedEngines: []string{"v0.6.0-1-gf7423cd"},
		TimeoutMillis: 10_000, MaxInflight: 1, WorstCaseChargeBytes: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	semantics, err := partscatalogsuggest.NewWithPolicy(partscatalogsuggest.PolicyOSA1)
	if err != nil {
		t.Fatal(err)
	}
	client, err := kurnhttp.NewClient(kurnhttp.ClientConfig{BaseURL: s.server.URL, BearerToken: "k", MaxResponseBytes: 1 << 20, MaxRetryAfter: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	executor, err := orchestrator.NewWithMembershipFallback(descriptor, semantics, client)
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.Search(context.Background(), []byte(`{"query":"XA-100","limit":25}`), []byte(`{"tenant_id":"tenant-a"}`))
	if err != nil {
		t.Fatal(err)
	}
	if s.calls.Load() != 2 || s.seen[0].Threshold == nil || *s.seen[0].Threshold != 0.45 || len(s.seen[1].ExactAnyOf) != 2 {
		t.Fatalf("policy wire: calls %d first %+v second %+v", s.calls.Load(), s.seen[0], s.seen[1])
	}
	hits := result.Resolution().Hits()
	if len(hits) != 1 || hits[0].EntryID() != "PC-001" || !result.FallbackTaken() || result.FallbackHits() != 1 {
		t.Fatalf("result: hits %d taken %v fallback hits %d", len(hits), result.FallbackTaken(), result.FallbackHits())
	}
	// The default policy on the same wire answers from the first pass and
	// reports no fallback.
	plain := newSuggestServer(t, func(call int, check anyOfCheck) ([]any, string) {
		return []any{map[string]any{"list": suggestList, "entry_id": "PC-012", "score": 66, "key": "AX-103",
			"payload": map[string]any{"id": "PC-012", "identifier": "AX-103", "manufacturer": "Northstar", "category": "bearing", "status": "active", "tenant_id": "tenant-a"}}}, served
	})
	result, err = suggestExecutor(t, plain).Search(context.Background(), []byte(`{"query":"XA-100","limit":25}`), []byte(`{"tenant_id":"tenant-a"}`))
	if err != nil || plain.calls.Load() != 1 || result.FallbackTaken() || result.FallbackHits() != 0 || len(result.Resolution().Hits()) != 1 {
		t.Fatalf("default policy: %v calls %d taken %v", err, plain.calls.Load(), result.FallbackTaken())
	}
}
