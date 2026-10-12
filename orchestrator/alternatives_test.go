package orchestrator_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/kurn-dev/kurn-search/adapter"
	"github.com/kurn-dev/kurn-search/adapters/partscatalogalternatives"
	"github.com/kurn-dev/kurn-search/backend/kurnhttp"
	"github.com/kurn-dev/kurn-search/orchestrator"
	"github.com/kurn-dev/kurn-search/profile"
)

type stubAlternatives struct {
	partscatalogalternatives.Semantics
	resolve func() (adapter.Resolution, adapter.AlternativeSummary, error)
}

func (s stubAlternatives) ResolveWithAlternativeSummary(_, _ []byte, _ adapter.CompiledPlan, _ []adapter.ProbeEvidence) (adapter.Resolution, adapter.AlternativeSummary, error) {
	return s.resolve()
}

func alternativeDescriptor(t *testing.T) profile.Descriptor {
	t.Helper()
	descriptor, err := partscatalogalternatives.NewDescriptor(partscatalogalternatives.OperationalBinding{
		PhysicalListName: facetList, ConfigSHA256: facetConfigSHA,
		TestedEngines: []string{"v0.5.3"}, TimeoutMillis: 10_000,
		MaxInflight: 1, WorstCaseChargeBytes: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	return descriptor
}

func TestAlternativeConstructionAndCeiling(t *testing.T) {
	descriptor := alternativeDescriptor(t)
	if executor, err := orchestrator.NewWithAlternatives(descriptor, partscatalogalternatives.New(), nil); err == nil || executor != nil {
		t.Fatal("nil backend accepted")
	}
	if executor, err := orchestrator.NewWithAlternatives(descriptor, nil, backendFunc(func(context.Context, []kurnhttp.Probe) (kurnhttp.BatchResult, error) {
		return kurnhttp.BatchResult{}, errors.New("unreachable")
	})); err == nil || executor != nil {
		t.Fatal("nil semantics accepted")
	}
	var calls atomic.Int64
	server := facetServer(t, &calls)
	semantics := stubAlternatives{Semantics: partscatalogalternatives.New(),
		resolve: func() (adapter.Resolution, adapter.AlternativeSummary, error) {
			return adapter.Resolution{}, adapter.AlternativeSummary{}, adapter.ErrSummaryAtCeiling
		}}
	executor, err := orchestrator.NewWithAlternatives(descriptor, semantics, facetClient(t, server))
	if err != nil || executor == nil || !executor.Alternatives() || executor.Facets() {
		t.Fatal("alternative construction failed")
	}
	_, err = executor.Search(context.Background(), facetRequest, facetScope)
	if category(t, err).Category != orchestrator.CategoryIncompleteAtPoolCeiling || calls.Load() != 1 {
		t.Fatalf("ceiling triggered extra round or wrong category: %v, calls %d", err, calls.Load())
	}
}

func TestAlternativeIncompleteKeepsClosedLadder(t *testing.T) {
	var calls atomic.Int64
	server := facetServer(t, &calls)
	semantics := stubAlternatives{Semantics: partscatalogalternatives.New(),
		resolve: func() (adapter.Resolution, adapter.AlternativeSummary, error) {
			return adapter.Resolution{}, adapter.AlternativeSummary{}, saturatedIncomplete(t)
		}}
	executor, err := orchestrator.NewWithAlternatives(alternativeDescriptor(t), semantics, facetClient(t, server))
	if err != nil {
		t.Fatal(err)
	}
	_, err = executor.Search(context.Background(), facetRequest, facetScope)
	if category(t, err).Category != orchestrator.CategoryIncompleteAtPoolCeiling || calls.Load() != 4 {
		t.Fatalf("ladder/refusal mismatch: %v, calls %d", err, calls.Load())
	}
}

func TestAlternativeMixedFullStampsRestartAndRefuse(t *testing.T) {
	for _, test := range []struct {
		name      string
		badCalls  int
		wantError bool
	}{
		{"restart", 1, false}, {"exhausted", 2, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer r.Body.Close()
				call := int(calls.Add(1))
				var input struct {
					Checks []membershipCheck `json:"checks"`
				}
				if json.NewDecoder(r.Body).Decode(&input) != nil || len(input.Checks) < 2 {
					t.Error("alternative batch malformed")
					w.WriteHeader(500)
					return
				}
				results := make([]any, len(input.Checks))
				for index, check := range input.Checks {
					stamp := facetStamp()
					if call <= test.badCalls && index == 0 {
						stamp = strings.Repeat("b", 64) + "@152+j0+c" + facetConfigSHA
					}
					results[index] = map[string]any{
						"candidates": []any{}, "versions": map[string]string{facetList: stamp},
						"filter": check.Filter, "filter_stats": map[string]any{facetList: map[string]int{"evaluated": 0, "rejected": 0}},
						"took_us": 1,
					}
				}
				w.Header().Set("Content-Type", "application/json")
				if err := json.NewEncoder(w).Encode(map[string]any{"results": results, "took_us": 1}); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			executor, err := orchestrator.NewWithAlternatives(alternativeDescriptor(t), partscatalogalternatives.New(), facetClient(t, server))
			if err != nil {
				t.Fatal(err)
			}
			result, err := executor.Search(context.Background(), facetRequest, facetScope)
			if test.wantError {
				if category(t, err).Category != orchestrator.CategoryListIdentityMismatch {
					t.Fatalf("identity refusal: %v", err)
				}
			} else if err != nil || result.VersionAttempt() != 2 || !result.AlternativeSummary().Valid() {
				t.Fatalf("identity restart: %v", err)
			}
			if calls.Load() != 2 {
				t.Fatalf("identity calls=%d", calls.Load())
			}
		})
	}
}
