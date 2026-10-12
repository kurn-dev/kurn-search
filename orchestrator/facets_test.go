package orchestrator_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kurn-dev/kurn-search/adapter"
	"github.com/kurn-dev/kurn-search/adapters/partscatalogdiscover"
	"github.com/kurn-dev/kurn-search/adapters/partscatalogfacets"
	"github.com/kurn-dev/kurn-search/backend/kurnhttp"
	"github.com/kurn-dev/kurn-search/orchestrator"
	"github.com/kurn-dev/kurn-search/profile"
)

const facetList = "parts-catalog-discover-v1"

var facetConfigSHA = strings.Repeat("c", 64)

func facetDescriptor(t *testing.T) profile.Descriptor {
	t.Helper()
	descriptor, err := partscatalogfacets.NewDescriptor(partscatalogfacets.OperationalBinding{
		PhysicalListName: facetList, ConfigSHA256: facetConfigSHA,
		TestedEngines: []string{"v0.5.3"}, TimeoutMillis: 10_000,
		MaxInflight: 1, WorstCaseChargeBytes: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	return descriptor
}

// stubFacetSemantics embeds the delivered discover adapter and overrides only
// the summary resolution, for classification-mapping tests.
type stubFacetSemantics struct {
	partscatalogdiscover.Semantics
	resolve func() (adapter.Resolution, adapter.Summary, error)
}

func (s stubFacetSemantics) ResolveWithSummary(_, _ []byte, _ adapter.CompiledPlan, _ []adapter.ProbeEvidence) (adapter.Resolution, adapter.Summary, error) {
	return s.resolve()
}

func facetStamp() string { return strings.Repeat("a", 64) + "@152+j0+c" + facetConfigSHA }

// facetServer answers every probe with zero candidates and a valid identity,
// so resolution is reached with complete empty evidence.
func facetServer(t *testing.T, calls *atomic.Int64) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		calls.Add(1)
		var input struct {
			Checks []membershipCheck `json:"checks"`
		}
		if json.NewDecoder(r.Body).Decode(&input) != nil {
			t.Error("decode request")
			w.WriteHeader(500)
			return
		}
		results := make([]any, len(input.Checks))
		for i, check := range input.Checks {
			if len(check.Lists) != 1 || check.Lists[0] != facetList {
				t.Errorf("list drift: %v", check.Lists)
			}
			results[i] = map[string]any{
				"candidates": []any{}, "versions": map[string]string{facetList: facetStamp()},
				"filter": check.Filter, "filter_stats": map[string]any{facetList: map[string]int{"evaluated": 0, "rejected": 0}},
				"took_us": 1,
			}
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{"results": results, "took_us": 1}); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func facetClient(t *testing.T, server *httptest.Server) *kurnhttp.Client {
	t.Helper()
	client, err := kurnhttp.NewClient(kurnhttp.ClientConfig{
		BaseURL: server.URL, BearerToken: "test-key", MaxResponseBytes: 64 << 20,
		MaxRetryAfter: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

var facetRequest = []byte(`{"query":"sprocket","limit":5}`)
var facetScope = []byte(`{"tenant_id":"tenant-a"}`)

func TestNewWithFacetsConstruction(t *testing.T) {
	descriptor := facetDescriptor(t)
	if executor, err := orchestrator.NewWithFacets(descriptor, partscatalogfacets.New(), nil); err == nil || executor != nil {
		t.Fatal("nil backend accepted")
	}
	if executor, err := orchestrator.NewWithFacets(descriptor, nil, backendFunc(func(context.Context, []kurnhttp.Probe) (kurnhttp.BatchResult, error) {
		return kurnhttp.BatchResult{}, errors.New("unreachable")
	})); err == nil || executor != nil {
		t.Fatal("nil semantics accepted")
	}
	executor, err := orchestrator.NewWithFacets(descriptor, partscatalogfacets.New(), backendFunc(func(context.Context, []kurnhttp.Probe) (kurnhttp.BatchResult, error) {
		return kurnhttp.BatchResult{}, errors.New("unreachable")
	}))
	if err != nil || executor == nil || !executor.Facets() {
		t.Fatal("facet construction failed")
	}
}

func TestFacetCeilingCategoryNeverEscalates(t *testing.T) {
	var calls atomic.Int64
	server := facetServer(t, &calls)
	semantics := stubFacetSemantics{
		Semantics: partscatalogdiscover.New(),
		resolve: func() (adapter.Resolution, adapter.Summary, error) {
			return adapter.Resolution{}, adapter.Summary{}, adapter.ErrSummaryAtCeiling
		},
	}
	executor, err := orchestrator.NewWithFacets(facetDescriptor(t), semantics, facetClient(t, server))
	if err != nil {
		t.Fatal(err)
	}
	_, err = executor.Search(context.Background(), facetRequest, facetScope)
	if category(t, err).Category != orchestrator.CategoryIncompleteAtPoolCeiling {
		t.Fatalf("category: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("ceiling refusal caused extra backend rounds: %d backend calls", calls.Load())
	}
}

func TestFacetIncompleteKeepsDeliveredLadder(t *testing.T) {
	var calls atomic.Int64
	server := facetServer(t, &calls)
	semantics := stubFacetSemantics{
		Semantics: partscatalogdiscover.New(),
		resolve: func() (adapter.Resolution, adapter.Summary, error) {
			return adapter.Resolution{}, adapter.Summary{}, saturatedIncomplete(t)
		},
	}
	executor, err := orchestrator.NewWithFacets(facetDescriptor(t), semantics, facetClient(t, server))
	if err != nil {
		t.Fatal(err)
	}
	_, err = executor.Search(context.Background(), facetRequest, facetScope)
	if category(t, err).Category != orchestrator.CategoryIncompleteAtPoolCeiling {
		t.Fatalf("category: %v", err)
	}
	if calls.Load() != 4 {
		t.Fatalf("incomplete did not walk the delivered ladder: %d calls", calls.Load())
	}
}

// saturatedIncomplete builds evidence whose first probe is saturated, proving
// the delivered incomplete classification flows through the facet seam
// unchanged. Every candidate re-projects to its own probe's key and every
// entry identifier is unique per probe, exactly as the delegate validates.
func saturatedIncomplete(t *testing.T) error {
	t.Helper()
	delegate := partscatalogdiscover.New()
	plan, err := delegate.Compile(facetRequest, facetScope)
	if err != nil {
		t.Fatal(err)
	}
	evidence := make([]adapter.ProbeEvidence, 0, len(plan.Probes()))
	for probeIndex, probe := range plan.Probes() {
		candidates := make([]adapter.Candidate, 0, 128)
		for index := 0; index < 128; index++ {
			var identifier, name string
			switch string(probe.Kind()) {
			case "exact":
				identifier, name = "sprocket", "wheel hub"
			case "identifier-prefix", "identifier-word":
				identifier, name = "sprocket-x", "wheel hub"
			default: // name-prefix, name-word
				identifier, name = "ZB-9", "sprocket wheel"
			}
			payload := []byte(fmt.Sprintf(`{"category":"bearing","id":"sat-%d-%d","identifier":"%s","manufacturer":"Northstar","name":"%s","status":"active","tenant_id":"tenant-a"}`, probeIndex, index, identifier, name))
			entries, err := delegate.Project(payload)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Contains(entries[0].Keys(), probe.PhysicalKey()) {
				t.Fatalf("payload lacks probe key %s", probe.PhysicalKey())
			}
			candidate, err := adapter.NewCandidate(adapter.CandidateSpec{
				EntryID: entries[0].EntryID(), Score: 100, EchoedKey: probe.PhysicalKey(), Payload: payload,
			})
			if err != nil {
				t.Fatal(err)
			}
			candidates = append(candidates, candidate)
		}
		item, err := adapter.NewProbeEvidence(adapter.ProbeEvidenceSpec{
			Probe: probe, RequestedPool: 128, Candidates: candidates,
		})
		if err != nil {
			t.Fatal(err)
		}
		evidence = append(evidence, item)
	}
	_, err = delegate.Resolve(facetRequest, facetScope, plan, evidence)
	if err == nil || !delegate.IsIncomplete(err) {
		t.Fatalf("synthesized evidence did not produce incomplete: %v", err)
	}
	return err
}
