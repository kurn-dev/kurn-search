package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kurn-dev/kurn-search/adapters/partscatalog"
	"github.com/kurn-dev/kurn-search/adapters/partscatalogselect"
	"github.com/kurn-dev/kurn-search/backend/kurnhttp"
	"github.com/kurn-dev/kurn-search/orchestrator"
	"github.com/kurn-dev/kurn-search/profile"
)

const selectList = "parts-select-test-v1"

// This counts actual executor Batch invocations, not public query metrics.
// The wrapped client still performs the real HTTP serialization/decoder path.
type countedMembershipBackend struct {
	client *kurnhttp.Client
	calls  atomic.Int64
	before func()
}

func (b *countedMembershipBackend) Batch(ctx context.Context, probes []kurnhttp.Probe) (kurnhttp.BatchResult, error) {
	b.calls.Add(1)
	if b.before != nil {
		b.before()
	}
	return b.client.Batch(ctx, probes)
}

func selectDescriptor(t *testing.T, inflight int) profile.Descriptor {
	t.Helper()
	d, err := partscatalogselect.NewDescriptor(partscatalogselect.OperationalBinding{
		PhysicalListName: selectList, ConfigSHA256: testConfig, TestedEngines: []string{"v0.6.0-1-gf7423cd"},
		TimeoutMillis: 5000, MaxInflight: inflight, WorstCaseChargeBytes: 1 << 30,
	})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func selectService(t *testing.T) (*Service, *countedMembershipBackend, *atomic.Int64, *MemoryObserver) {
	t.Helper()
	httpCalls := &atomic.Int64{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpCalls.Add(1)
		defer r.Body.Close()
		var wire struct {
			Checks []struct {
				Filter json.RawMessage `json:"filter"`
				Lists  []string        `json:"lists"`
			} `json:"checks"`
		}
		if r.URL.Path != "/v1/batch-query" || json.NewDecoder(r.Body).Decode(&wire) != nil || len(wire.Checks) != 5 {
			t.Error("wire count/path")
			w.WriteHeader(500)
			return
		}
		results := make([]any, len(wire.Checks))
		for i, check := range wire.Checks {
			var fields map[string]json.RawMessage
			if json.Unmarshal(check.Filter, &fields) != nil || string(fields["tenant_id"]) != `"tenant-a"` || len(check.Lists) != 1 || check.Lists[0] != selectList {
				t.Error("missing scoped predicate")
			}
			results[i] = map[string]any{"candidates": []any{}, "versions": map[string]string{selectList: testStamp},
				"filter": check.Filter, "filter_stats": map[string]any{selectList: map[string]int{"evaluated": 0, "rejected": 0}}, "took_us": 0}
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{"results": results, "took_us": 0}); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(server.Close)
	client, err := kurnhttp.NewClient(kurnhttp.ClientConfig{BaseURL: server.URL, BearerToken: "fixture-only", MaxResponseBytes: 64 << 20, MaxRetryAfter: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	backend := &countedMembershipBackend{client: client}
	descriptor := selectDescriptor(t, 1)
	executor, err := orchestrator.NewWithMembership(descriptor, partscatalogselect.New(), backend)
	if err != nil {
		t.Fatal(err)
	}
	runner, err := WrapExecutor(executor)
	if err != nil {
		t.Fatal(err)
	}
	auth, err := NewConstantTimeBearerAuthenticator(map[string]string{"fixture-handle": "tenant-a"})
	if err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(t.TempDir(), "fixture-token")
	if err := os.WriteFile(key, []byte("fixture-only"), 0600); err != nil {
		t.Fatal(err)
	}
	observer := &MemoryObserver{}
	svc, err := New(Config{Bindings: []Binding{{Descriptor: descriptor, Runner: runner}}, Authenticator: auth,
		AttestedEngine: "v0.6.0-1-gf7423cd", Rate: RateAttestation{Unlimited: true}, AdmissionWait: time.Millisecond,
		CredentialFile: key, KurnEndpoint: server.URL, Observer: observer,
		ReadinessGoldens: []Golden{{Name: "select", Route: "parts-catalog-select", AuthHandle: "fixture-handle", SentinelTenant: "tenant-a", Query: "AX-100", Limit: 25, ExpectedIDs: []string{}, ExpectedComplete: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	backend.before = func() {
		if len(svc.admission.gates[selectList].tokens) != 1 {
			t.Error("successful Batch did not hold admission")
		}
	}
	return svc, backend, httpCalls, observer
}

func invokeSelect(svc *Service, raw []byte) *httptest.ResponseRecorder {
	req := request(string(raw))
	req.URL.Path = "/v1/search/parts-catalog-select"
	result := httptest.NewRecorder()
	svc.ServeHTTP(result, req)
	return result
}

func TestMembershipServiceRefusalsPrecedeAdmissionAndBackend(t *testing.T) {
	svc, backend, httpCalls, observer := selectService(t)
	encode := func(value any) []byte {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	vectors := [][]byte{}
	for _, value := range []string{
		`{"category":[]}`, `{"status":["active","active","active"]}`, `{"status":"retired"}`, `{"status":["active","retired"]}`,
		`{"category":null}`, `null`, `{"category":42}`, `{"category":true}`, `{"category":[null]}`, `{"category":[1]}`, `{"category":[true]}`,
		`{"category":[["x"]]}`, `{"category":{"in":["x"]}}`, `{"category":[{"eq":"x"}]}`,
		`{"category":""}`, `{"category":[""]}`, `{"category":["x","\ud800"]}`, `{"category":["\udc00","x"]}`,
		`{"other":"x"}`, `{"tenant_id":"tenant-b"}`, `{"partition_id":["north"]}`,
		`{"category":"bearing","category":["bearing"]}`, `{"category":"bearing","\u0063ategory":["bearing"]}`, `[]`,
	} {
		vectors = append(vectors, []byte(`{"query":"AX-100","limit":25,"filters":`+value+`}`))
	}
	for _, field := range []string{"manufacturer", "category"} {
		repeated := make([]string, 17)
		for i := range repeated {
			repeated[i] = "x"
		}
		for _, value := range []any{repeated, strings.Repeat("x", 513), []string{strings.Repeat("😀", 513)}} {
			vectors = append(vectors, encode(map[string]any{"query": "AX-100", "limit": 25, "filters": map[string]any{field: value}}))
		}
	}
	for _, raw := range []string{`{"limit":25}`, `{"query":"","limit":25}`, `{"query":" ","limit":25}`, `{"query":"AX-100","limit":0}`,
		`{"query":"AX-100","limit":1.5}`, `{"query":"AX-100","limit":1001}`, `{"query":"AX-100","limit":25,"partition_id":["north"]}`,
		`{"query":"AX-100","query":"AX-100","limit":25}`, `{"query":"AX-100","limit":25,"extra":true}`} {
		vectors = append(vectors, []byte(raw))
	}
	vectors = append(vectors, encode(map[string]any{"query": strings.Repeat("x", 257), "limit": 25}))
	vectors = append(vectors, bytes.Replace([]byte(`{"query":"AX-100","limit":25,"filters":{"category":"x"}}`), []byte(`"x"`), []byte{'"', 0xff, '"'}, 1))
	vectors = append(vectors, append([]byte(strings.Repeat(" ", 1<<20)), []byte(`{"query":"AX-100","limit":25}`)...))
	category, manufacturer := make([]string, 16), make([]string, 16)
	for i := range category {
		category[i] = fmt.Sprintf("c%02d", i) + strings.Repeat("x", 509)
		manufacturer[i] = fmt.Sprintf("m%02d", i) + strings.Repeat("x", 509)
	}
	manufacturer[15] = "m15" + strings.Repeat("x", 345)
	capRequest := map[string]any{"query": "AX-100", "limit": 25, "filters": map[string]any{"category": category, "manufacturer": manufacturer}}
	atCap := encode(capRequest)
	capRequest["partition_id"] = "north"
	vectors = append(vectors, encode(capRequest))
	delete(capRequest, "partition_id")
	manufacturer[15] += "x"
	vectors = append(vectors, encode(capRequest))
	// Occupy the real admission slot. Invalid input must still give 400,
	// not an admission timeout, and must not release/acquire this held slot.
	tokens := svc.admission.gates[selectList].tokens
	tokens <- struct{}{}
	for i, raw := range vectors {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			before := len(observer.Events())
			result := invokeSelect(svc, raw)
			partsScriptError(t, result, 400, "invalid_request")
			if backend.calls.Load() != 0 || httpCalls.Load() != 0 || len(tokens) != 1 {
				t.Fatal("invalid request reached backend/admission")
			}
			for _, event := range observer.Events()[before:] {
				if event.Kind == "admission" {
					t.Fatal("invalid request attempted admission")
				}
			}
		})
	}
	wrongVersion := invokeSelect(svc, []byte(`{"query":"AX-100","limit":25,"adapter_version":"v2"}`))
	partsScriptError(t, wrongVersion, 409, "adapter_version_mismatch")
	// The occupied-slot control must observe admission refusal for valid input.
	partsScriptError(t, invokeSelect(svc, atCap), 503, "service_admission_unavailable")
	if backend.calls.Load() != 0 || httpCalls.Load() != 0 {
		t.Fatal("admission refusal reached client")
	}
	<-tokens
	// An admitted at-cap control proves both counters and the admission hook
	// can observe success; the scripted daemon echoes the actual sent bytes.
	result := invokeSelect(svc, atCap)
	if result.Code != 200 || backend.calls.Load() != 1 || httpCalls.Load() != 1 || len(tokens) != 0 {
		t.Fatalf("success control %d %s", result.Code, result.Body)
	}
	admitted := 0
	for _, event := range observer.Events() {
		if event.Kind == "admission" && event.Outcome == "admitted" {
			admitted++
		}
	}
	if admitted != 1 {
		t.Fatal("admission success census", admitted)
	}
}

func TestMembershipSharedListPolicyAndLegacyRoutes(t *testing.T) {
	svc, backend, httpCalls, _ := selectService(t)
	current := svc.registry.bindings["parts-catalog-select"]
	for _, limit := range []int{1, 2} {
		old, err := partscatalog.NewDescriptor(partscatalog.OperationalBinding{PhysicalListName: selectList, ConfigSHA256: testConfig,
			TestedEngines: []string{"v0.6.0-1-gf7423cd"}, TimeoutMillis: 5000, MaxInflight: limit, WorstCaseChargeBytes: 1 << 30})
		if err != nil {
			t.Fatal(err)
		}
		executor, err := orchestrator.New(old, partscatalog.New(), backend)
		if err != nil {
			t.Fatal(err)
		}
		runner, err := WrapExecutor(executor)
		if err != nil {
			t.Fatal(err)
		}
		registry, err := newRegistry([]Binding{current, {Descriptor: old, Runner: runner}}, 0)
		if limit == 2 {
			if err == nil {
				t.Fatal("inconsistent shared-list policy accepted")
			}
			continue
		}
		if err != nil || len(registry.limits) != 1 {
			t.Fatal("shared-list agreement", err)
		}
		svc.registry = registry
		req := request(`{"query":"AX-100","limit":25,"filters":{"manufacturer":["Northstar","Halden"]}}`)
		req.URL.Path = "/v1/search/parts-catalog"
		result := httptest.NewRecorder()
		svc.ServeHTTP(result, req)
		partsScriptError(t, result, 400, "invalid_request")
		if backend.calls.Load() != 0 || httpCalls.Load() != 0 {
			t.Fatal("legacy array refusal lost")
		}
	}
}

type membershipFixtureAuthenticator struct{ principal Principal }

func (a membershipFixtureAuthenticator) Authenticate(context.Context, http.Header) (Principal, error) {
	return a.principal, nil
}

type membershipCountingRunner struct {
	Runner
	preflights int
}

func (r *membershipCountingRunner) Preflight(ctx context.Context, request, scope []byte) error {
	r.preflights++
	return r.Runner.Preflight(ctx, request, scope)
}

func TestMembershipServicePrincipalAndScopeEnvelopeAttribution(t *testing.T) {
	svc, backend, httpCalls, observer := selectService(t)
	binding := svc.registry.bindings["parts-catalog-select"]
	runner := &membershipCountingRunner{Runner: binding.Runner}
	binding.Runner = runner
	svc.registry.bindings["parts-catalog-select"] = binding
	maxScope := binding.Descriptor.Spec().Bounds.MaxTrustedScopeBytes
	if maxScope != 64<<10 {
		t.Fatal("scope envelope changed")
	}
	// A normal Principal is limited to 4096 UTF-8 bytes. NUL realizes the
	// worst six-output-bytes-per-input-byte JSON expansion. This boundary
	// control is NOT a claim that the recipe admits a 4096-scalar tenant.
	principal, err := NewPrincipal([]string{strings.Repeat("\x00", 4096)})
	if err != nil {
		t.Fatal(err)
	}
	emptyScope, err := trustedScopeJSON("")
	if err != nil {
		t.Fatal(err)
	}
	largest, err := trustedScopeJSON(principal.Tenant())
	if err != nil || len(largest) != len(emptyScope)+6*4096 || len(largest) >= maxScope {
		t.Fatal("normal principal does not prove the encoded scope bound")
	}
	if _, err := NewPrincipal([]string{strings.Repeat("x", 4097)}); err == nil {
		t.Fatal("principal exceeded its original bound")
	}
	// Deliberate in-package fault injection bypasses NewPrincipal ONLY to
	// exercise the existing service scope guard. A public authenticator
	// cannot construct this principal. It is not an external HTTP F vector.
	forged := Principal{tenant: strings.Repeat("\x00", maxScope/6+1), valid: true}
	if raw, err := trustedScopeJSON(forged.Tenant()); err != nil || len(raw) <= maxScope {
		t.Fatal("synthetic oversized scope not realized")
	}
	svc.authenticator = membershipFixtureAuthenticator{principal: forged}
	tokens := svc.admission.gates[selectList].tokens
	tokens <- struct{}{}
	before := len(observer.Events())
	result := invokeSelect(svc, []byte(`{"query":"AX-100","limit":25}`))
	partsScriptError(t, result, 403, "scope_refused")
	if runner.preflights != 0 || backend.calls.Load() != 0 || httpCalls.Load() != 0 || len(tokens) != 1 {
		t.Fatal("scope guard reached preflight/admission/backend")
	}
	authenticated := 0
	for _, event := range observer.Events()[before:] {
		if event.Kind == "admission" {
			t.Fatal("scope guard reached admission")
		}
		if event.Kind == "authentication" && event.Outcome == "success" {
			authenticated++
		}
	}
	if authenticated != 1 {
		t.Fatal("authentication masked the synthetic scope guard")
	}
	<-tokens
	// Restore a constructor-created principal and prove the same runner,
	// backend/HTTP counters and admission hook observe a successful call.
	normal, err := NewPrincipal([]string{"tenant-a"})
	if err != nil {
		t.Fatal(err)
	}
	svc.authenticator = membershipFixtureAuthenticator{principal: normal}
	result = invokeSelect(svc, []byte(`{"query":"AX-100","limit":25}`))
	if result.Code != 200 || runner.preflights != 1 || backend.calls.Load() != 1 || httpCalls.Load() != 1 || len(tokens) != 0 {
		t.Fatal("normal service instrumentation control failed", result.Code)
	}
	t.Logf("normal principal worst encoded scope: %d bytes; service cap: %d; synthetic guard: 403 before preflight/admission/backend", len(largest), maxScope)
}
