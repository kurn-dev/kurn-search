package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kurn-dev/kurn-search/adapter"
	"github.com/kurn-dev/kurn-search/adapters/catalogid"
	"github.com/kurn-dev/kurn-search/adapters/partscatalog"
	"github.com/kurn-dev/kurn-search/backend/kurnhttp"
	"github.com/kurn-dev/kurn-search/orchestrator"
	"github.com/kurn-dev/kurn-search/profile"
)

func TestFiltersCodecPreservesRawPresenceAndOldBytes(t *testing.T) {
	bounds := testDescriptor(t, time.Second).Spec().Bounds
	old := []byte("{\n  \"limit\": 25,\n  \"partition_id\": \"north\",\n  \"query\": \"AX-100\"\n}\n")
	omitted, err := parseSearchRequest([]byte(`{"query":"AX-100","limit":25,"partition_id":"north"}`), bounds)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := omitted.adapterJSON()
	if err != nil || !bytes.Equal(encoded, old) {
		t.Fatal("omitted filter bytes changed")
	}
	for _, value := range []string{`{}`, `{ "manufacturer" : "\u004eorthstar" }`, `[]`, `42`, `true`, `"bad"`} {
		body := []byte(`{"query":"AX-100","limit":25,"filters":` + value + `}`)
		parsed, err := parseSearchRequest(body, bounds)
		if err != nil {
			t.Fatal(err)
		}
		for i := range body {
			body[i] = '!'
		}
		encoded, err := parsed.adapterJSON()
		if err != nil {
			t.Fatal(err)
		}
		var members map[string]json.RawMessage
		if json.Unmarshal(encoded, &members) != nil || string(members["filters"]) != value {
			t.Fatalf("raw value lost: %s", encoded)
		}
		if _, err := catalogid.New().Compile(encoded, []byte(`{"tenant_id":"tenant-a"}`)); err == nil {
			t.Fatal("old adapter accepted filters")
		}
	}
	for _, value := range []string{`null`, `{"manufacturer":"a","manufacturer":"b"}`} {
		if _, err := parseSearchRequest([]byte(`{"query":"AX-100","limit":25,"filters":`+value+`}`), bounds); err == nil {
			t.Fatal("outer validation bypass")
		}
	}
}

func TestOldCatalogFiltersRefuseBeforeRun(t *testing.T) {
	runner := &fakeRunner{preflight: func(_ context.Context, request, scope []byte) error {
		if _, err := catalogid.New().Compile(request, scope); err != nil {
			return &orchestrator.Error{Category: orchestrator.CategoryInvalidInput}
		}
		return nil
	}}
	svc := testService(t, runner, time.Second, time.Millisecond)
	for _, value := range []string{`{}`, `{"manufacturer":"Northstar"}`, `[]`, `true`, `null`} {
		w := httptest.NewRecorder()
		svc.ServeHTTP(w, request(`{"query":"AX-100","limit":25,"filters":`+value+`}`))
		if w.Code != 400 || runner.runs.Load() != 0 || !bytes.Contains(w.Body.Bytes(), []byte("invalid_request")) {
			t.Fatal("old-route refusal")
		}
	}
	w := httptest.NewRecorder()
	svc.ServeHTTP(w, request(`{"query":"AX-100","limit":25}`))
	if w.Code != 200 || runner.runs.Load() != 1 {
		t.Fatal("old request no longer works")
	}
}

func TestPartsServicePreflightRejectsBadFiltersBeforeRun(t *testing.T) {
	descriptor, err := partscatalog.NewDescriptor(partscatalog.OperationalBinding{
		PhysicalListName: "catalog-v1", ConfigSHA256: testConfig, TestedEngines: []string{"v0.5.3"},
		TimeoutMillis: 1000, MaxInflight: 1, WorstCaseChargeBytes: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{preflight: func(_ context.Context, req, scope []byte) error {
		if _, err := partscatalog.New().Compile(req, scope); err != nil {
			return &orchestrator.Error{Category: orchestrator.CategoryInvalidInput}
		}
		if _, err := partscatalog.New().CompileFilter(req, scope); err != nil {
			return &orchestrator.Error{Category: orchestrator.CategoryInvalidFilter}
		}
		return nil
	}, run: func(_ context.Context, req, scope []byte) (Execution, error) {
		if string(scope) != "{\n  \"tenant_id\": \"tenant-sentinel\"\n}\n" {
			t.Fatal("trusted tenant changed")
		}
		out, err := testExecution()
		out.adapterName = "parts-catalog"
		out.behaviorSHA256 = descriptor.Identities().Behavior.Hex()
		return out, err
	}}
	svc := testService(t, runner, time.Second, time.Millisecond)
	svc.registry, err = newRegistry([]Binding{{Descriptor: descriptor, Runner: runner}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	run := func(body string) *httptest.ResponseRecorder {
		req := request(body)
		req.URL.Path = "/v1/search/parts-catalog"
		w := httptest.NewRecorder()
		svc.ServeHTTP(w, req)
		return w
	}
	for _, value := range []string{`null`, `[]`, `42`, `true`, `{"status":"archived"}`, `{"tenant_id":"other"}`, `{"partition_id":"north"}`, `{"manufacturer":""}`, `{"manufacturer":null}`, `{"manufacturer":"\ud800"}`, `{"manufacturer":{"eq":"x"}}`, `{"manufacturer":"a","manufacturer":"b"}`} {
		w := run(`{"query":"AX-100","limit":25,"filters":` + value + `}`)
		if w.Code != 400 || runner.runs.Load() != 0 {
			t.Fatalf("invalid parts filter: %d %s", w.Code, w.Body.String())
		}
	}
	for _, value := range []string{`{}`, `{"manufacturer":"Northstar","category":"bearing","status":"active"}`} {
		w := run(`{"query":"AX-100","limit":25,"filters":` + value + `}`)
		if w.Code != 200 {
			t.Fatalf("valid parts shape: %d %s", w.Code, w.Body.String())
		}
	}
	if runner.runs.Load() != 2 {
		t.Fatal("successful run census")
	}
	for _, field := range []string{"query", "manufacturer", "category"} {
		limit := 512
		if field == "query" {
			limit = 256
		}
		for _, extra := range []int{0, 1} {
			t.Run(fmt.Sprintf("%s-scalars-plus-%d", field, extra), func(t *testing.T) {
				body := map[string]any{"query": "AX-100", "limit": 25}
				if field == "query" {
					body[field] = strings.Repeat("x", limit+extra)
				} else {
					body["filters"] = map[string]string{field: strings.Repeat("😀", limit+extra)}
				}
				raw, err := json.Marshal(body)
				if err != nil {
					t.Fatal(err)
				}
				before := runner.runs.Load()
				response := run(string(raw))
				if extra == 0 {
					if response.Code != 200 || runner.runs.Load() != before+1 {
						t.Fatal("exact scalar limit refused before execution")
					}
				} else if response.Code != 400 || runner.runs.Load() != before || !bytes.Contains(response.Body.Bytes(), []byte("invalid_request")) {
					t.Fatal("over-limit request reached execution or wrong refusal")
				}
			})
		}
	}
}

const (
	testConfig = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testStamp  = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb@1+j0+c" + testConfig
)

type fakeRunner struct {
	preflight  func(context.Context, []byte, []byte) error
	run        func(context.Context, []byte, []byte) (Execution, error)
	preflights atomic.Int64
	runs       atomic.Int64
}

func (f *fakeRunner) Preflight(ctx context.Context, request, scope []byte) error {
	f.preflights.Add(1)
	if f.preflight != nil {
		return f.preflight(ctx, request, scope)
	}
	return nil
}

func (f *fakeRunner) Run(ctx context.Context, request, scope []byte) (Execution, error) {
	f.runs.Add(1)
	if f.run != nil {
		return f.run(ctx, request, scope)
	}
	return testExecution()
}

func testDescriptor(t *testing.T, timeout time.Duration) profile.Descriptor {
	t.Helper()
	descriptor, err := catalogid.NewDescriptor(catalogid.OperationalBinding{
		PhysicalListName: "catalog-v1", ConfigSHA256: testConfig,
		TestedEngines: []string{"v0.5.3"}, TimeoutMillis: uint64(timeout.Milliseconds()),
		MaxInflight: 1, WorstCaseChargeBytes: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	return descriptor
}

func testExecution() (Execution, error) {
	hit, err := adapter.NewResolvedHit(adapter.ResolvedHitSpec{EntryID: "item-1", Result: []byte(`{"id":"item-1"}`)})
	if err != nil {
		return Execution{}, err
	}
	resolution, err := adapter.NewResolution(adapter.ResolutionSpec{Complete: true, Hits: []adapter.ResolvedHit{hit}})
	if err != nil {
		return Execution{}, err
	}
	descriptor, err := catalogid.NewDescriptor(catalogid.OperationalBinding{
		PhysicalListName: "catalog-v1", ConfigSHA256: testConfig, TestedEngines: []string{"v0.5.3"},
		TimeoutMillis: 1000, MaxInflight: 1, WorstCaseChargeBytes: 1,
	})
	if err != nil {
		return Execution{}, err
	}
	return NewExecution(ExecutionSpec{
		Resolution:  resolution,
		Lists:       []ProducedListSpec{{Role: "identifiers", Name: "catalog-v1", Stamp: testStamp}},
		AdapterName: "catalog-identifier", AdapterVersion: "v1",
		BehaviorSHA256: descriptor.Identities().Behavior.Hex(), VersionAttempt: 1, PoolStep: 1, Pool: 128, BackendTookUS: 7,
	})
}

func testZeroHitExecution() (Execution, error) {
	execution, err := testExecution()
	if err != nil {
		return Execution{}, err
	}
	resolution, err := adapter.NewResolution(adapter.ResolutionSpec{Complete: true})
	if err != nil {
		return Execution{}, err
	}
	execution.resolution = resolution
	return execution, nil
}

func testService(t *testing.T, runner Runner, timeout, wait time.Duration) *Service {
	t.Helper()
	credential := filepath.Join(t.TempDir(), "kurnd.key")
	if err := os.WriteFile(credential, []byte("opaque-kurn-credential"), 0o600); err != nil {
		t.Fatal(err)
	}
	authenticator, err := NewConstantTimeBearerAuthenticator(map[string]string{"fixture-handle": "tenant-sentinel"})
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(Config{
		Bindings:      []Binding{{Descriptor: testDescriptor(t, timeout), Runner: runner}},
		Authenticator: authenticator, AttestedEngine: "v0.5.3",
		Rate: RateAttestation{RatePerSecond: 32}, AdmissionWait: wait,
		CredentialFile: credential, KurnEndpoint: "http://127.0.0.1:8080",
		ReadinessGoldens: []Golden{{
			Name: "positive", Route: "catalog-identifier", AuthHandle: "fixture-handle",
			SentinelTenant: "tenant-sentinel", Query: "item", Limit: 1, ExpectedIDs: []string{"item-1"}, ExpectedComplete: true,
		}},
		Now: func() time.Time { return time.Unix(1_700_000_000, 0) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if fake, ok := runner.(*fakeRunner); ok {
		fake.preflights.Store(0)
		fake.runs.Store(0)
	}
	return service
}

func request(body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/v1/search/catalog-identifier", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer fixture-handle")
	req.Header.Set("Content-Type", "application/json")
	return req
}

func TestSuccessScopeAndDeterministicEnvelope(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{}
	var gotRequest, gotScope []byte
	runner.preflight = func(_ context.Context, request, scope []byte) error {
		gotRequest = bytes.Clone(request)
		gotScope = bytes.Clone(scope)
		return nil
	}
	service := testService(t, runner, 2*time.Second, 50*time.Millisecond)
	w := httptest.NewRecorder()
	service.ServeHTTP(w, request(`{"query":"needle","partition_id":"north","adapter_version":"v1","limit":1}`))
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if string(gotScope) != "{\n  \"tenant_id\": \"tenant-sentinel\"\n}\n" {
		t.Fatalf("trusted scope mismatch: %q", gotScope)
	}
	if bytes.Contains(gotRequest, []byte("adapter_version")) || !bytes.Contains(gotRequest, []byte("partition_id")) {
		t.Fatalf("adapter request mismatch: %s", gotRequest)
	}
	if bytes.Contains(w.Body.Bytes(), []byte("evidence")) || bytes.Contains(w.Body.Bytes(), []byte("tenant-sentinel")) {
		t.Fatalf("response leaked forbidden data: %s", w.Body.Bytes())
	}
	var response successResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || !response.Complete || len(response.Hits) != 1 || len(response.Lists) != 1 {
		t.Fatalf("invalid success: %v %#v", err, response)
	}
}

func TestZeroHitSuccessEncodesEmptyArray(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{run: func(context.Context, []byte, []byte) (Execution, error) {
		return testZeroHitExecution()
	}}
	service := testService(t, runner, 2*time.Second, 50*time.Millisecond)
	w := httptest.NewRecorder()
	service.ServeHTTP(w, request(`{"query":"absent","limit":1}`))
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte(`"complete":true,"hits":[]`)) {
		t.Fatalf("zero-hit success is not an exact empty array: %s", w.Body.String())
	}
	var response successResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || response.Hits == nil || len(response.Hits) != 0 ||
		len(response.Lists) == 0 || !response.Complete {
		t.Fatalf("invalid zero-hit success: %v %#v", err, response)
	}
}

type detectingBody struct{ read atomic.Bool }

func (b *detectingBody) Read([]byte) (int, error) { b.read.Store(true); return 0, io.EOF }
func (*detectingBody) Close() error               { return nil }

func TestAuthenticationPrecedesBodyRead(t *testing.T) {
	t.Parallel()
	service := testService(t, &fakeRunner{}, time.Second, time.Millisecond)
	body := &detectingBody{}
	req := httptest.NewRequest(http.MethodPost, "/v1/search/catalog-identifier", nil)
	req.Body = body
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	service.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized || body.read.Load() {
		t.Fatalf("status=%d body_read=%v", w.Code, body.read.Load())
	}
}

func TestMalformedAndUnknownRouteAreIdentical(t *testing.T) {
	t.Parallel()
	service := testService(t, &fakeRunner{}, time.Second, time.Millisecond)
	var bodies []string
	for _, path := range []string{"/v1/search/Bad--Name", "/v1/search/not-registered"} {
		req := request(`{"query":"q","limit":1}`)
		req.URL.Path = path
		w := httptest.NewRecorder()
		service.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("%s status=%d", path, w.Code)
		}
		bodies = append(bodies, w.Body.String())
	}
	if bodies[0] != bodies[1] {
		t.Fatalf("route oracle: %q != %q", bodies[0], bodies[1])
	}
}

func TestStrictRequestRefusalsBeforeExecution(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{}
	service := testService(t, runner, time.Second, time.Millisecond)
	cases := []string{
		`{}`, `{"query":"x"}`, `{"query":"x","limit":null}`,
		`{"query":"x","limit":1,"limit":2}`,
		`{"query":"x","limit":1,"tenant_id":"smuggle"}`,
		`{"query":"x","limit":0}`, `{"query":"x","limit":1} {}`,
		`{"query":"x","limit":"1"}`, `{"query":"x","limit":true}`,
		`{"query":"x","limit":[1]}`, `{"query":"x","limit":{}}`,
		`{"query":1,"limit":1}`,
		`{"query":"\ud800","limit":1}`, `{"query":"\udc00","limit":1}`,
		`{"query":"\udc00\ud800","limit":1}`,
		`{"query":"x","partition_id":"\ud800","limit":1}`,
		`{"query":"x","adapter_version":"\ud800","limit":1}`,
	}
	for _, body := range cases {
		w := httptest.NewRecorder()
		service.ServeHTTP(w, request(body))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("body=%q status=%d response=%s", body, w.Code, w.Body.String())
		}
	}
	if runner.preflights.Load() != 0 || runner.runs.Load() != 0 {
		t.Fatalf("refusal reached execution: preflight=%d run=%d", runner.preflights.Load(), runner.runs.Load())
	}
}

func TestStrictRequestAcceptsValidSurrogatePair(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{}
	service := testService(t, runner, time.Second, time.Millisecond)
	w := httptest.NewRecorder()
	service.ServeHTTP(w, request(`{"query":"\uD83D\ude00","partition_id":"\ud83d\uDE00","adapter_version":"v1","limit":1}`))
	if w.Code != http.StatusOK || runner.preflights.Load() != 1 || runner.runs.Load() != 1 {
		t.Fatalf("status=%d preflight=%d run=%d body=%s", w.Code, runner.preflights.Load(), runner.runs.Load(), w.Body.String())
	}
}

func TestVersionMethodMediaAndAuthenticationStatuses(t *testing.T) {
	t.Parallel()
	service := testService(t, &fakeRunner{}, time.Second, time.Millisecond)
	tests := []struct {
		mutate        func(*http.Request)
		status        int
		header, value string
	}{
		{func(r *http.Request) { r.Header.Del("Authorization") }, 401, "WWW-Authenticate", "Bearer"},
		{func(r *http.Request) { r.Method = http.MethodGet }, 405, "Allow", "POST"},
		{func(r *http.Request) { r.Header.Set("Content-Type", "application/json; charset=utf-8") }, 415, "", ""},
	}
	for _, test := range tests {
		req := request(`{"query":"x","limit":1}`)
		test.mutate(req)
		w := httptest.NewRecorder()
		service.ServeHTTP(w, req)
		if w.Code != test.status || (test.header != "" && w.Header().Get(test.header) != test.value) {
			t.Fatalf("status=%d headers=%v", w.Code, w.Header())
		}
	}
	w := httptest.NewRecorder()
	service.ServeHTTP(w, request(`{"query":"x","limit":1,"adapter_version":"v2"}`))
	if w.Code != http.StatusConflict {
		t.Fatalf("version status=%d", w.Code)
	}
}

func TestBackendHintBoundAndCarrier(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		hint    time.Duration
		present bool
	}{
		{time.Second, true}, {5 * time.Second, true}, {6 * time.Second, false}, {500 * time.Millisecond, false}, {0, false},
	} {
		runner := &fakeRunner{run: func(context.Context, []byte, []byte) (Execution, error) {
			return Execution{}, &orchestrator.Error{Category: orchestrator.CategoryBackendRateLimited, RetryAfter: test.hint}
		}}
		service := testService(t, runner, time.Second, time.Millisecond)
		w := httptest.NewRecorder()
		service.ServeHTTP(w, request(`{"query":"x","limit":1}`))
		if w.Code != http.StatusTooManyRequests {
			t.Fatalf("hint=%s status=%d", test.hint, w.Code)
		}
		hasHeader := w.Header().Get("Retry-After") != ""
		hasBody := bytes.Contains(w.Body.Bytes(), []byte("retry_after_seconds"))
		if hasHeader != test.present || hasBody != test.present {
			t.Fatalf("hint=%s header=%v body=%s", test.hint, hasHeader, w.Body.String())
		}
	}
}

func TestAdmissionBoundAndOperationDeadline(t *testing.T) {
	for _, test := range []struct {
		name          string
		timeout, wait time.Duration
		status        int
	}{
		{"admission", time.Second, 20 * time.Millisecond, http.StatusServiceUnavailable},
		{"deadline", 20 * time.Millisecond, time.Second, http.StatusGatewayTimeout},
	} {
		t.Run(test.name, func(t *testing.T) {
			entered := make(chan struct{}, 1)
			release := make(chan struct{})
			runner := &fakeRunner{run: func(context.Context, []byte, []byte) (Execution, error) {
				select {
				case entered <- struct{}{}:
				default:
				}
				<-release
				return testExecution()
			}}
			service := testService(t, runner, test.timeout, test.wait)
			firstDone := make(chan struct{})
			go func() {
				defer close(firstDone)
				service.ServeHTTP(httptest.NewRecorder(), request(`{"query":"x","limit":1}`))
			}()
			<-entered
			w := httptest.NewRecorder()
			service.ServeHTTP(w, request(`{"query":"x","limit":1}`))
			if w.Code != test.status {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if test.status == http.StatusServiceUnavailable && w.Header().Get("Retry-After") != "1" {
				t.Fatalf("admission hint=%q", w.Header().Get("Retry-After"))
			}
			close(release)
			<-firstDone
		})
	}
}

func TestReadinessAndServerBounds(t *testing.T) {
	t.Parallel()
	service := testService(t, &fakeRunner{}, 600*time.Second, time.Millisecond)
	if err := service.Ready(context.Background()); err != nil {
		t.Fatal(err)
	}
	server, err := service.HTTPServer("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if server.ReadHeaderTimeout != 2*time.Second || server.ReadTimeout != 5*time.Second ||
		server.WriteTimeout != 605*time.Second || server.IdleTimeout != 30*time.Second || server.MaxHeaderBytes != 16<<10 {
		t.Fatalf("server bounds: %#v", server)
	}
}

func TestStartupAttestationsAndCredentialMode(t *testing.T) {
	t.Parallel()
	descriptor := testDescriptor(t, time.Second)
	runner := &fakeRunner{}
	auth, _ := NewConstantTimeBearerAuthenticator(map[string]string{"x": "tenant"})
	credential := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(credential, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	base := Config{
		Bindings: []Binding{{Descriptor: descriptor, Runner: runner}}, Authenticator: auth,
		AttestedEngine: "v0.5.3", Rate: RateAttestation{RatePerSecond: 32}, AdmissionWait: time.Millisecond,
		CredentialFile: credential, KurnEndpoint: "http://127.0.0.1:8080",
		ReadinessGoldens: []Golden{{Name: "g", Route: "catalog-identifier", AuthHandle: "x", SentinelTenant: "tenant", Query: "q", Limit: 1, ExpectedIDs: []string{"item-1"}, ExpectedComplete: true}},
	}
	if _, err := New(base); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Config){
		"rate-low": func(c *Config) { c.Rate.RatePerSecond = 31 },
		"engine":   func(c *Config) { c.AttestedEngine = "v9.9.9" },
		"endpoint": func(c *Config) { c.KurnEndpoint = "http://user:secret@host" },
		"goldens":  func(c *Config) { c.ReadinessGoldens = nil },
	} {
		t.Run(name, func(t *testing.T) {
			copy := base
			mutate(&copy)
			if _, err := New(copy); err == nil {
				t.Fatal("unexpected success")
			}
		})
	}
	if err := os.Chmod(credential, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := New(base); err == nil {
		t.Fatal("unsafe credential mode accepted")
	}
	base.Rate = RateAttestation{Unlimited: true}
	if err := os.Chmod(credential, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(base); err != nil {
		t.Fatalf("unlimited rejected: %v", err)
	}
}

func TestAdmissionRaceReleasesCapacity(t *testing.T) {
	service := testService(t, &fakeRunner{}, time.Second, 50*time.Millisecond)
	var group sync.WaitGroup
	for range 64 {
		group.Add(1)
		go func() {
			defer group.Done()
			w := httptest.NewRecorder()
			service.ServeHTTP(w, request(`{"query":"x","limit":1}`))
			if w.Code != http.StatusOK && w.Code != http.StatusServiceUnavailable {
				t.Errorf("status=%d", w.Code)
			}
		}()
	}
	group.Wait()
	w := httptest.NewRecorder()
	service.ServeHTTP(w, request(`{"query":"x","limit":1}`))
	if w.Code != http.StatusOK {
		t.Fatalf("capacity lost: %d", w.Code)
	}
}

func TestContractBytesAndPrincipalCardinality(t *testing.T) {
	t.Parallel()
	if _, err := Compose(nil); err == nil {
		t.Fatal("nil composition unexpectedly succeeded")
	}
	if !bytes.Equal(ContractJSON(), ContractJSON()) || !bytes.Contains(ContractJSON(), []byte(`"maximum_seconds":5`)) {
		t.Fatalf("unstable contract: %s", ContractJSON())
	}
	for _, tenants := range [][]string{nil, {}, {"a", "b"}, {""}} {
		if _, err := NewPrincipal(tenants); err == nil {
			t.Fatalf("accepted %#v", tenants)
		}
	}
	if principal, err := NewPrincipal([]string{"tenant"}); err != nil || !principal.Valid() {
		t.Fatal(err)
	}
}

func TestPreflightFailureHasZeroBackendRun(t *testing.T) {
	t.Parallel()
	var fail atomic.Bool
	runner := &fakeRunner{preflight: func(context.Context, []byte, []byte) error {
		if fail.Load() {
			return &orchestrator.Error{Category: orchestrator.CategoryInvalidFilter}
		}
		return nil
	}}
	service := testService(t, runner, time.Second, time.Millisecond)
	fail.Store(true)
	w := httptest.NewRecorder()
	service.ServeHTTP(w, request(`{"query":"x","limit":1}`))
	if w.Code != http.StatusBadRequest || runner.runs.Load() != 0 {
		t.Fatalf("status=%d runs=%d", w.Code, runner.runs.Load())
	}
}

func TestNoErrorEnvelopeEcho(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{run: func(context.Context, []byte, []byte) (Execution, error) {
		return Execution{}, errors.New("secret path /tmp/private")
	}}
	service := testService(t, runner, time.Second, time.Millisecond)
	w := httptest.NewRecorder()
	service.ServeHTTP(w, request(`{"query":"private-query","limit":1}`))
	if w.Code != http.StatusInternalServerError || bytes.Contains(w.Body.Bytes(), []byte("private")) || bytes.Contains(w.Body.Bytes(), []byte("/tmp")) {
		t.Fatalf("leaky error: %d %s", w.Code, w.Body.String())
	}
}

func TestRouteGrammarEdges(t *testing.T) {
	t.Parallel()
	valid := []string{"a", "a0", "catalog-identifier", strings.Repeat("a", 64)}
	invalid := []string{"", "-a", "a-", "a--b", "A", "a_b", strings.Repeat("a", 65), "é"}
	for _, value := range valid {
		if !validRoute(value) {
			t.Fatalf("valid route refused: %q", value)
		}
	}
	for _, value := range invalid {
		if validRoute(value) {
			t.Fatalf("invalid route accepted: %q", value)
		}
	}
}

func TestBodyOneOverBoundRefusedBeforePreflight(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{}
	service := testService(t, runner, time.Second, time.Millisecond)
	body := `{"query":"` + strings.Repeat("x", (1<<20)+1) + `","limit":1}`
	w := httptest.NewRecorder()
	service.ServeHTTP(w, request(body))
	if w.Code != http.StatusBadRequest || runner.preflights.Load() != 0 || runner.runs.Load() != 0 {
		t.Fatalf("status=%d preflight=%d run=%d", w.Code, runner.preflights.Load(), runner.runs.Load())
	}
}

func TestFiniteOrchestratorErrorMapping(t *testing.T) {
	t.Parallel()
	tests := map[orchestrator.Category]int{
		orchestrator.CategoryInvalidInput: 400, orchestrator.CategoryInvalidPlan: 400,
		orchestrator.CategoryInvalidFilter: 400, orchestrator.CategoryIncompleteAtPoolCeiling: 422,
		orchestrator.CategoryBackendRateLimited: 429, orchestrator.CategoryBackendOverloaded: 503,
		orchestrator.CategoryDeadlineExceeded: 504, orchestrator.CategoryCanceled: 504,
		orchestrator.CategoryInvalidEvidence: 502, orchestrator.CategoryAdapterResolution: 502,
		orchestrator.CategoryListIdentityMismatch: 502, orchestrator.CategoryTransportFailure: 502,
		orchestrator.CategoryResponseTooLarge: 502, orchestrator.CategoryBackendProtocol: 502,
		orchestrator.CategoryFilterMismatch: 502, orchestrator.CategoryBackendInline: 502,
		orchestrator.CategoryBackendUnauthorized: 502, orchestrator.CategoryBackendForbidden: 502,
		orchestrator.CategoryBackendNotFound: 502, orchestrator.CategoryBackendConflict: 502,
		orchestrator.CategoryBackendHTTP: 502, orchestrator.CategoryBackendRedirect: 502,
	}
	for category, status := range tests {
		gotStatus, gotCategory, _ := mapExecutionError(&orchestrator.Error{Category: category})
		if gotStatus != status || (status == 502 && gotCategory != string(category)) {
			t.Fatalf("%s => %d/%s", category, gotStatus, gotCategory)
		}
	}
}

func TestReadinessFailureIsGenericAndObserverIsClosed(t *testing.T) {
	t.Parallel()
	service := testService(t, &fakeRunner{}, time.Second, time.Millisecond)
	observer := &MemoryObserver{}
	service.observer = observer
	w := httptest.NewRecorder()
	service.ServeHTTP(w, request(`{"query":"secret-query","partition_id":"secret-partition","limit":1}`))
	if w.Code != http.StatusOK {
		t.Fatal(w.Code)
	}
	events, err := json.Marshal(observer.Events())
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"secret-query", "secret-partition", "tenant-sentinel", "item-1", "catalog-v1", testStamp} {
		if bytes.Contains(events, []byte(forbidden)) {
			t.Fatalf("observer leaked %q: %s", forbidden, events)
		}
	}
	service.goldens[0].ExpectedIDs = []string{"private-mutation"}
	if err := service.Ready(context.Background()); err == nil || strings.Contains(err.Error(), "private-mutation") {
		t.Fatalf("readiness failure was not generic: %v", err)
	}
}

func TestCredentialSymlinkRefused(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	target := filepath.Join(directory, "target")
	link := filepath.Join(directory, "link")
	if err := os.WriteFile(target, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if validateCredentialFile(link) == nil {
		t.Fatal("credential symlink accepted")
	}
}

// The following W6-S2 tests use a scripted loopback HTTP backend, the real
// concrete kurnhttp client, the unchanged executor and the actual parts route.
// They are protocol/control-flow evidence, not real Kurn or performance proof.
type partsWireRequest struct {
	Checks []struct {
		Query  string            `json:"q"`
		Lists  []string          `json:"lists"`
		TopK   int               `json:"topk"`
		Filter map[string]string `json:"filter"`
	} `json:"checks"`
}

const partsScriptRequest = `{"query":"AX-100","limit":25,"partition_id":"north","filters":{"manufacturer":"Northstar","category":"bearing","status":"active"}}`

func partsScriptStamp(base string) string {
	return strings.Repeat(base, 64) + "@2000+j0+c" + testConfig
}

func partsScriptSuccess(request partsWireRequest, count int, prefix, stamp string) map[string]any {
	results := make([]any, len(request.Checks))
	for i, check := range request.Checks {
		candidates := make([]any, count)
		for j := range candidates {
			id := fmt.Sprintf("%s-%04d", prefix, j)
			candidates[j] = map[string]any{
				"list": "parts-script-v1", "entry_id": id, "score": 100, "key": check.Query,
				"payload": map[string]any{"id": id, "identifier": "AX-100",
					"manufacturer": "Northstar", "category": "bearing", "status": "active",
					"tenant_id": "tenant-sentinel", "partition_id": "north"},
			}
		}
		results[i] = map[string]any{
			"candidates": candidates, "versions": map[string]string{"parts-script-v1": stamp},
			"filter":       check.Filter,
			"filter_stats": map[string]any{"parts-script-v1": map[string]int{"evaluated": count, "rejected": 0}},
			"took_us":      1,
		}
	}
	return map[string]any{"results": results, "took_us": 5}
}

func partsScriptService(t *testing.T, timeout, wait time.Duration,
	script func(http.ResponseWriter, *http.Request, int, partsWireRequest)) (*Service, *atomic.Int64) {
	t.Helper()
	calls := &atomic.Int64{}
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var wire partsWireRequest
		raw, readErr := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if r.URL.Path != "/v1/batch-query" || r.Method != http.MethodPost ||
			readErr != nil || json.Unmarshal(raw, &wire) != nil || len(wire.Checks) != 5 {
			t.Error("scripted parts backend received the wrong batch")
			http.Error(w, "fixture refusal", 400)
			return
		}
		var rawWire struct {
			Checks []struct {
				Filter json.RawMessage `json:"filter"`
			} `json:"checks"`
		}
		if json.Unmarshal(raw, &rawWire) != nil {
			t.Error("raw batch unavailable")
		}
		const canonical = `{"category":"bearing","manufacturer":"Northstar","partition_id":"north","status":"active","tenant_id":"tenant-sentinel"}`
		for _, check := range rawWire.Checks {
			if string(check.Filter) != canonical {
				t.Error("noncanonical forwarded filter bytes")
			}
		}
		wantFilter := map[string]string{"tenant_id": "tenant-sentinel", "partition_id": "north",
			"manufacturer": "Northstar", "category": "bearing", "status": "active"}
		seen := map[string]bool{}
		for _, check := range wire.Checks {
			if !reflect.DeepEqual(check.Filter, wantFilter) || !slices.Equal(check.Lists, []string{"parts-script-v1"}) ||
				!slices.Contains([]int{128, 256, 512, 1000}, check.TopK) || seen[check.Query] {
				t.Error("a parts probe lost scope, business predicates, list, bound or uniqueness")
			}
			seen[check.Query] = true
		}
		w.Header().Set("Content-Type", "application/json")
		script(w, r, int(calls.Add(1)), wire)
	}))
	t.Cleanup(backend.Close)
	return partsScriptBoundService(t, backend.URL, "tenant-sentinel", timeout, wait), calls
}

// Shared setup only: the original W6-S2 helper above retains its literal
// request/filter assertions. S4 supplies separate exact wire assertions.
func partsScriptBoundService(t *testing.T, endpoint, tenant string, timeout, wait time.Duration) *Service {
	t.Helper()
	client, err := kurnhttp.NewClient(kurnhttp.ClientConfig{
		BaseURL: endpoint, BearerToken: "unused-scripted-fixture",
		HTTPClient:       &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: nil}},
		MaxResponseBytes: 64 << 20, MaxRetryAfter: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err := partscatalog.NewDescriptor(partscatalog.OperationalBinding{
		PhysicalListName: "parts-script-v1", ConfigSHA256: testConfig,
		TestedEngines: []string{"v0.6.0-1-gf7423cd"}, TimeoutMillis: uint64(timeout.Milliseconds()),
		MaxInflight: 1, WorstCaseChargeBytes: 1 << 30,
	})
	if err != nil {
		t.Fatal(err)
	}
	executor, err := orchestrator.New(descriptor, partscatalog.New(), client)
	if err != nil {
		t.Fatal(err)
	}
	runner, err := WrapExecutor(executor)
	if err != nil {
		t.Fatal(err)
	}
	auth, err := NewConstantTimeBearerAuthenticator(map[string]string{"fixture-handle": tenant})
	if err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(t.TempDir(), "fixture-key")
	if err := os.WriteFile(key, []byte("unused-scripted-fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	svc, err := New(Config{
		Bindings: []Binding{{Descriptor: descriptor, Runner: runner}}, Authenticator: auth,
		AttestedEngine: "v0.6.0-1-gf7423cd", Rate: RateAttestation{RatePerSecond: 32},
		AdmissionWait: wait, CredentialFile: key, KurnEndpoint: endpoint,
		ReadinessGoldens: []Golden{{Name: "parts", Route: "parts-catalog",
			AuthHandle: "fixture-handle", SentinelTenant: tenant, Query: "AX-100",
			Limit: 25, ExpectedIDs: []string{"fresh-0000"}, ExpectedComplete: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

// S4-B09/B10: real service/executor/client with a scripted loopback backend.
// Each pair differs ONLY in filter echo bytes; no encoder may repair raw P.
func TestScalarHTTPExactEchoBusinessAndScopePairs(t *testing.T) {
	for _, tc := range []struct{ name, category, tenant, partition, canonical, wire string }{
		{"business", "<a&", "tenant-sentinel", "north",
			`{"category":"<a&","partition_id":"north","tenant_id":"tenant-sentinel"}`,
			`{"category":"\u003ca\u0026","partition_id":"north","tenant_id":"tenant-sentinel"}`},
		{"tenant", "bearing", "tenant-<&", "north",
			`{"category":"bearing","partition_id":"north","tenant_id":"tenant-<&"}`,
			`{"category":"bearing","partition_id":"north","tenant_id":"tenant-\u003c\u0026"}`},
		{"partition", "bearing", "tenant-sentinel", "north>&",
			`{"category":"bearing","partition_id":"north>&","tenant_id":"tenant-sentinel"}`,
			`{"category":"bearing","partition_id":"north\u003e\u0026","tenant_id":"tenant-sentinel"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var pairResponses [2][]byte
			for index, echo := range []string{tc.wire, tc.canonical} {
				t.Run([]string{"exact-wire", "raw-canonical"}[index], func(t *testing.T) {
					var calls atomic.Int64
					payload := map[string]string{"id": "fresh-0000", "identifier": "AX-100", "manufacturer": "Northstar",
						"category": tc.category, "status": "active", "tenant_id": tc.tenant, "partition_id": tc.partition}
					backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						calls.Add(1)
						defer r.Body.Close()
						raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
						var batch struct {
							Checks []struct {
								Query  string          `json:"q"`
								Lists  []string        `json:"lists"`
								TopK   int             `json:"topk"`
								Filter json.RawMessage `json:"filter"`
							} `json:"checks"`
						}
						if err != nil || json.Unmarshal(raw, &batch) != nil || len(batch.Checks) != 5 || r.Method != "POST" || r.URL.Path != "/v1/batch-query" {
							t.Error("scalar service request shape drift", err)
							http.Error(w, "fixture refusal", 400)
							return
						}
						results := make([]string, len(batch.Checks))
						seen := map[string]bool{}
						for i, check := range batch.Checks {
							if string(check.Filter) != tc.canonical || !slices.Equal(check.Lists, []string{"parts-script-v1"}) || check.TopK != 128 || seen[check.Query] {
								t.Error("scalar request lost exact P, list, first rung or unique probe")
							}
							seen[check.Query] = true
							candidate, err := json.Marshal(map[string]any{"list": "parts-script-v1", "entry_id": "fresh-0000", "score": 100, "key": check.Query, "payload": payload})
							if err != nil {
								t.Error(err)
								return
							}
							results[i] = `{"candidates":[` + string(candidate) + `],"versions":{"parts-script-v1":"` + partsScriptStamp("b") +
								`"},"filter":` + echo + `,"filter_stats":{"parts-script-v1":{"evaluated":1,"rejected":0}},"took_us":1}`
						}
						body := []byte(`{"results":[` + strings.Join(results, ",") + `],"took_us":5}`)
						if !json.Valid(body) || bytes.Count(body, []byte(`"filter":`+echo)) != 5 {
							t.Error("scripted scalar raw echo changed")
						}
						pairResponses[index] = bytes.Clone(body)
						w.Header().Set("Content-Type", "application/json")
						if _, err := w.Write(body); err != nil {
							t.Error(err)
						}
					}))
					defer backend.Close()
					svc := partsScriptBoundService(t, backend.URL, tc.tenant, 5*time.Second, time.Second)
					input, err := json.Marshal(map[string]any{"query": "AX-100", "limit": 25, "partition_id": tc.partition, "filters": map[string]string{"category": tc.category}})
					if err != nil {
						t.Fatal(err)
					}
					r := request(string(input))
					r.URL.Path = "/v1/search/parts-catalog"
					response := httptest.NewRecorder()
					svc.ServeHTTP(response, r)
					if calls.Load() != 1 {
						t.Fatal("scalar echo added transport attempts", calls.Load())
					}
					if index == 1 {
						partsScriptError(t, response, 502, "filter_identity_mismatch")
					} else {
						var body successResponse
						if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &body) != nil || !body.Complete || len(body.Hits) != 1 || body.Hits[0].EntryID != "fresh-0000" {
							t.Fatalf("exact scalar echo lost nonempty success: %d %s", response.Code, response.Body)
						}
						if body.VersionAttempt != 1 || body.PoolStep != 1 || body.Pool != 128 {
							t.Fatal("scalar success lost first-attempt producing provenance")
						}
					}
				})
			}
			if len(pairResponses[0]) == 0 || len(pairResponses[1]) == 0 ||
				!bytes.Equal(bytes.ReplaceAll(pairResponses[0], []byte(`"filter":`+tc.wire), []byte(`"filter":`+tc.canonical)), pairResponses[1]) {
				t.Fatal("scalar pair changed evidence other than exact echo spelling")
			}
		})
	}
}

func partsScriptInvoke(svc *Service, ctx context.Context) *httptest.ResponseRecorder {
	r := request(partsScriptRequest).WithContext(ctx)
	r.URL.Path = "/v1/search/parts-catalog"
	w := httptest.NewRecorder()
	svc.ServeHTTP(w, r)
	return w
}

func partsScriptError(t *testing.T, response *httptest.ResponseRecorder, status int, category string) {
	t.Helper()
	var body map[string]any
	if response.Code != status || json.Unmarshal(response.Body.Bytes(), &body) != nil || body["category"] != category {
		t.Fatalf("parts refusal: status=%d body=%s, want %d/%s", response.Code, response.Body, status, category)
	}
	for _, name := range []string{"hits", "complete", "filter", "filter_stats"} {
		if _, exists := body[name]; exists {
			t.Fatalf("refusal carried success/evidence member %s", name)
		}
	}
}

func TestPartsHTTPProducingRungsAndIdentityAttempts(t *testing.T) {
	for _, tc := range []struct {
		name                string
		pools               []int
		mismatch            int
		ceiling             bool
		hits, attempt, step int
	}{
		{"L07-L08-below-cap", []int{128}, 0, false, 127, 1, 1},
		{"L07-L08-L10-retired-rung", []int{128, 256}, 0, false, 1, 1, 2},
		{"L07-L08-ceiling", []int{128, 256, 512, 1000}, 0, true, 0, 0, 0},
		{"L07-L09-L10-restart", []int{128, 128}, 1, false, 1, 2, 1},
		{"L07-L09-exhaustion", []int{128, 128}, 2, false, 0, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, calls := partsScriptService(t, 5*time.Second, time.Second, func(w http.ResponseWriter, _ *http.Request, call int, wire partsWireRequest) {
				if call > len(tc.pools) {
					t.Error("unexpected retry")
					w.WriteHeader(500)
					return
				}
				for _, check := range wire.Checks {
					if check.TopK != tc.pools[call-1] {
						t.Error("wrong producing rung")
					}
				}
				n, prefix := tc.hits, "fresh"
				if tc.ceiling || (tc.mismatch == 0 && call < len(tc.pools)) {
					n, prefix = wire.Checks[0].TopK, "retired"
				}
				if tc.mismatch > 0 && call <= tc.mismatch {
					n, prefix = 1, "retired"
				}
				body := partsScriptSuccess(wire, n, prefix, partsScriptStamp("b"))
				if tc.mismatch > 0 && call <= tc.mismatch {
					body["results"].([]any)[0].(map[string]any)["versions"] = map[string]string{"parts-script-v1": partsScriptStamp("c")}
				}
				if err := json.NewEncoder(w).Encode(body); err != nil {
					t.Error(err)
				}
			})
			response := partsScriptInvoke(svc, context.Background())
			if calls.Load() != int64(len(tc.pools)) {
				t.Fatalf("calls=%d", calls.Load())
			}
			if tc.ceiling {
				partsScriptError(t, response, 422, "incomplete_at_pool_ceiling")
				return
			}
			if tc.mismatch == 2 {
				partsScriptError(t, response, 502, "list_identity_mismatch")
				return
			}
			var body successResponse
			if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &body) != nil || !body.Complete ||
				len(body.Hits) != min(tc.hits, 25) {
				t.Fatalf("parts success: %d %s", response.Code, response.Body)
			}
			var fields map[string]any
			if json.Unmarshal(response.Body.Bytes(), &fields) != nil ||
				fields["version_attempt"] != float64(tc.attempt) || fields["pool_step"] != float64(tc.step) ||
				fields["pool"] != float64(tc.pools[len(tc.pools)-1]) || bytes.Contains(response.Body.Bytes(), []byte("retired-")) {
				t.Fatal("retired evidence or incorrect producing provenance")
			}
			for i, hit := range body.Hits {
				if hit.EntryID != fmt.Sprintf("fresh-%04d", i) {
					t.Fatal("wrong producing IDs/order")
				}
			}
		})
	}
}

func TestPartsHTTPConcreteEchoStatsAndPayloadRefusals(t *testing.T) {
	type mutation struct {
		name, category string
		mutate         func(map[string]any)
	}
	cases := []mutation{
		{"L11-echo-absent", "filter_identity_mismatch", func(r map[string]any) { delete(r, "filter") }},
		{"L11-echo-null", "filter_identity_mismatch", func(r map[string]any) { r["filter"] = nil }},
		{"L11-echo-noncanonical", "filter_identity_mismatch", func(r map[string]any) {
			r["filter"] = json.RawMessage(`{"tenant_id":"tenant-sentinel","status":"active","partition_id":"north","manufacturer":"Northstar","category":"bearing"}`)
		}},
		{"L11-echo-extra", "filter_identity_mismatch", func(r map[string]any) { r["filter"].(map[string]string)["extra"] = "x" }},
		{"L11-echo-wrong", "filter_identity_mismatch", func(r map[string]any) { r["filter"].(map[string]string)["status"] = "discontinued" }},
		{"L11-stats-absent", "filter_identity_mismatch", func(r map[string]any) { delete(r, "filter_stats") }},
		{"L11-stats-null", "filter_identity_mismatch", func(r map[string]any) { r["filter_stats"] = nil }},
		{"L11-stats-negative", "backend_protocol_error", func(r map[string]any) {
			r["filter_stats"] = map[string]any{"parts-script-v1": map[string]int{"evaluated": -1, "rejected": 0}}
		}},
		{"L11-stats-string", "backend_protocol_error", func(r map[string]any) {
			r["filter_stats"] = map[string]any{"parts-script-v1": map[string]any{"evaluated": "1", "rejected": 0}}
		}},
		{"L11-stats-extra-list", "filter_identity_mismatch", func(r map[string]any) {
			r["filter_stats"].(map[string]any)["other"] = map[string]int{"evaluated": 1, "rejected": 0}
		}},
		{"L11-stats-rejected-over-evaluated", "backend_protocol_error", func(r map[string]any) {
			r["filter_stats"] = map[string]any{"parts-script-v1": map[string]int{"evaluated": 1, "rejected": 2}}
		}},
		{"O07-config", "list_identity_mismatch", func(r map[string]any) {
			r["versions"] = map[string]string{"parts-script-v1": strings.Repeat("b", 64) + "@2000+j0+c" + strings.Repeat("e", 64)}
		}},
		{"O07-stamp", "list_identity_mismatch", func(r map[string]any) { r["versions"] = map[string]string{"parts-script-v1": "bad"} }},
		{"O07-inline", "backend_inline_error", func(r map[string]any) {
			for k := range r {
				delete(r, k)
			}
			r["error"] = "private-scripted-prose"
		}},
	}
	for _, field := range []string{"id", "tenant_id", "partition_id", "manufacturer", "category", "status", "identifier"} {
		cases = append(cases, mutation{"L12-payload-" + field, "adapter_resolution_failed", func(r map[string]any) {
			p := r["candidates"].([]any)[0].(map[string]any)["payload"].(map[string]any)
			if field == "status" {
				p[field] = "discontinued"
			} else {
				p[field] = "other"
			}
		}})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, calls := partsScriptService(t, 5*time.Second, time.Second, func(w http.ResponseWriter, _ *http.Request, _ int, wire partsWireRequest) {
				body := partsScriptSuccess(wire, 1, "fresh", partsScriptStamp("b"))
				tc.mutate(body["results"].([]any)[0].(map[string]any))
				if err := json.NewEncoder(w).Encode(body); err != nil {
					t.Error(err)
				}
			})
			response := partsScriptInvoke(svc, context.Background())
			partsScriptError(t, response, 502, tc.category)
			wantCalls := int64(1)
			if tc.category == "list_identity_mismatch" {
				wantCalls = 2
			}
			if calls.Load() != wantCalls || bytes.Contains(response.Body.Bytes(), []byte("private-scripted-prose")) {
				t.Fatal("retry boundary or diagnostic disclosure")
			}
		})
	}
}

func TestPartsHTTPRedirectAndThrottleDoNotRetry(t *testing.T) {
	var followed atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { followed.Add(1) }))
	defer target.Close()
	for _, tc := range []struct {
		name                     string
		status                   int
		hint, category, wantHint string
	}{
		{"O05-redirect", 307, "", "backend_redirect", ""},
		{"O06-rate-bound", 429, "5", "backend_rate_limited", "5"},
		{"O06-rate-over", 429, "6", "backend_rate_limited", ""},
		{"O06-rate-date", 429, "Wed, 09 Sep 2026 00:00:00 GMT", "backend_rate_limited", ""},
		{"O06-overload", 503, "2", "backend_overloaded", "2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, calls := partsScriptService(t, 5*time.Second, time.Second, func(w http.ResponseWriter, _ *http.Request, _ int, _ partsWireRequest) {
				w.Header().Set("Location", target.URL)
				if tc.hint != "" {
					w.Header().Set("Retry-After", tc.hint)
				}
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, `{"error":"scripted backend refusal"}`)
			})
			response := partsScriptInvoke(svc, context.Background())
			status := tc.status
			if status == 307 {
				status = 502
			}
			partsScriptError(t, response, status, tc.category)
			if calls.Load() != 1 || followed.Load() != 0 || response.Header().Get("Retry-After") != tc.wantHint {
				t.Fatal("redirect/retry/hint policy changed")
			}
			if bytes.Contains(response.Body.Bytes(), []byte("retry_after_seconds")) != (tc.wantHint != "") {
				t.Fatal("retry hint header/body disagreement")
			}
		})
	}
}

func TestPartsHTTPAdmissionCancellationAndDeadlineRelease(t *testing.T) {
	for _, mode := range []string{"O01-O02-O04-admission-cancel", "O03-O04-deadline"} {
		t.Run(mode, func(t *testing.T) {
			timeout := 3 * time.Second
			if mode == "O03-O04-deadline" {
				timeout = 150 * time.Millisecond
			}
			entered, backendEnded := make(chan struct{}), make(chan struct{})
			svc, calls := partsScriptService(t, timeout, 30*time.Millisecond,
				func(w http.ResponseWriter, r *http.Request, call int, wire partsWireRequest) {
					if call == 1 {
						close(entered)
						defer close(backendEnded)
						select {
						case <-r.Context().Done():
						case <-time.After(4 * time.Second):
							t.Error("backend did not observe request cancellation")
						}
						return
					}
					if err := json.NewEncoder(w).Encode(partsScriptSuccess(wire, 1, "fresh", partsScriptStamp("b"))); err != nil {
						t.Error(err)
					}
				})
			if len(svc.admission.gates) != 1 || cap(svc.admission.gates["parts-script-v1"].tokens) != 1 {
				t.Fatal("admission must share one permit for all five probes of the one physical list")
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan *httptest.ResponseRecorder, 1)
			go func() { done <- partsScriptInvoke(svc, ctx) }()
			select {
			case <-entered:
			case <-time.After(2 * time.Second):
				t.Fatal("backend was not entered")
			}
			if mode == "O01-O02-O04-admission-cancel" {
				second := partsScriptInvoke(svc, context.Background())
				partsScriptError(t, second, 503, "service_admission_unavailable")
				if calls.Load() != 1 || second.Header().Get("Retry-After") != "1" {
					t.Fatal("admission refusal issued backend work or lost its bounded hint")
				}
				cancel()
			}
			select {
			case response := <-done:
				partsScriptError(t, response, 504, "operation_deadline")
			case <-time.After(2 * time.Second):
				t.Fatal("service did not return within the bound")
			}
			select {
			case <-backendEnded:
			case <-time.After(2 * time.Second):
				t.Fatal("backend request remained active")
			}
			if len(svc.admission.gates["parts-script-v1"].tokens) != 0 {
				t.Fatal("permit leaked")
			}
			last := partsScriptInvoke(svc, context.Background())
			if last.Code != 200 || calls.Load() != 2 {
				t.Fatalf("capacity not reusable: %d calls=%d", last.Code, calls.Load())
			}
		})
	}
}

// The queue bound: with max_inflight 1 and a queue of 1, the first request
// runs, the second waits, the third is refused at once with 503
// service_queue_full and Retry-After, and nothing it did reached the
// runner, preparation or the token wait; the default bound and an
// out-of-range bound are validated at construction.
func TestQueueFullRefusalBeforePreparation(t *testing.T) {
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	var runs atomic.Int64
	runner := &fakeRunner{run: func(context.Context, []byte, []byte) (Execution, error) {
		runs.Add(1)
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release
		return testExecution()
	}}
	credential := filepath.Join(t.TempDir(), "kurnd.key")
	if err := os.WriteFile(credential, []byte("opaque-kurn-credential"), 0o600); err != nil {
		t.Fatal(err)
	}
	authenticator, _ := NewConstantTimeBearerAuthenticator(map[string]string{"fixture-handle": "tenant-sentinel"})
	config := Config{
		Bindings:      []Binding{{Descriptor: testDescriptor(t, time.Second), Runner: runner}},
		Authenticator: authenticator, AttestedEngine: "v0.5.3",
		Rate: RateAttestation{RatePerSecond: 32}, AdmissionWait: time.Second, MaxQueue: 1,
		CredentialFile: credential, KurnEndpoint: "http://127.0.0.1:8080",
		ReadinessGoldens: []Golden{{
			Name: "positive", Route: "catalog-identifier", AuthHandle: "fixture-handle",
			SentinelTenant: "tenant-sentinel", Query: "item", Limit: 1, ExpectedIDs: []string{"item-1"}, ExpectedComplete: true,
		}},
	}
	observer := &MemoryObserver{}
	config.Observer = observer
	service, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []int{-1, MaxServiceQueue + 1} {
		config.MaxQueue = bad
		if _, err := New(config); err == nil {
			t.Fatalf("MaxQueue %d accepted", bad)
		}
	}
	config.MaxQueue = 0
	if _, err := New(config); err != nil {
		t.Fatalf("default queue bound refused: %v", err)
	}
	firstDone, secondDone := make(chan struct{}), make(chan *httptest.ResponseRecorder, 1)
	go func() {
		defer close(firstDone)
		service.ServeHTTP(httptest.NewRecorder(), request(`{"query":"x","limit":1}`))
	}()
	<-entered
	go func() {
		w := httptest.NewRecorder()
		service.ServeHTTP(w, request(`{"query":"x","limit":1}`))
		secondDone <- w
	}()
	deadline := time.Now().Add(time.Second)
	for service.admission.waiting("catalog-v1") != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if service.admission.waiting("catalog-v1") != 1 {
		t.Fatal("second request is not waiting")
	}
	w := httptest.NewRecorder()
	service.ServeHTTP(w, request(`{"query":"x","limit":1}`))
	if w.Code != http.StatusServiceUnavailable || !bytes.Contains(w.Body.Bytes(), []byte(`"service_queue_full"`)) || w.Header().Get("Retry-After") != "1" ||
		!bytes.Contains(w.Body.Bytes(), []byte(`"retry_after_seconds":1`)) {
		t.Fatalf("queue-full refusal: %d %q %s", w.Code, w.Header().Get("Retry-After"), w.Body.String())
	}
	var sawRefusal bool
	for _, event := range observer.Events() {
		if event.Kind == "admission" && event.Outcome == "refused" && event.Category == "service_queue_full" {
			sawRefusal = true
		}
	}
	if !sawRefusal || runs.Load() != 1 {
		t.Fatalf("refusal observed %v runs %d", sawRefusal, runs.Load())
	}
	close(release)
	<-firstDone
	if second := <-secondDone; second.Code != http.StatusOK {
		t.Fatalf("queued request: %d %s", second.Code, second.Body.String())
	}
	if service.admission.waiting("catalog-v1") != 0 || len(service.admission.gates["catalog-v1"].slots) != 0 {
		t.Fatal("positions leaked")
	}
}
