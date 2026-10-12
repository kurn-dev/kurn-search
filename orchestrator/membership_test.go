package orchestrator_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kurn-dev/kurn-search/adapter"
	"github.com/kurn-dev/kurn-search/adapters/partscatalogselect"
	"github.com/kurn-dev/kurn-search/backend/kurnhttp"
	"github.com/kurn-dev/kurn-search/orchestrator"
	"github.com/kurn-dev/kurn-search/profile"
)

const membershipList = "parts-catalog-v1"

var membershipRequest = []byte(`{"query":"ZB-9","limit":1000,"filters":{"manufacturer":["Northstar","Halden"],"status":["discontinued","active"]}}`)
var membershipScope = []byte(`{"tenant_id":"tenant-a"}`)

const membershipWire = `{"manufacturer":{"in":["Halden","Northstar"]},"status":{"in":["active","discontinued"]},"tenant_id":"tenant-a"}`

type membershipSemantics struct {
	partscatalogselect.Semantics
	membershipCalls, legacyCalls int
	substitute                   *adapter.MembershipFilterPlan
}

func (s *membershipSemantics) CompileMembershipFilter(request, scope []byte) (adapter.MembershipFilterPlan, error) {
	s.membershipCalls++
	if s.substitute != nil {
		return *s.substitute, nil
	}
	return s.Semantics.CompileMembershipFilter(request, scope)
}
func (s *membershipSemantics) CompileFilter(_, _ []byte) (adapter.ExactFilterPlan, error) {
	s.legacyCalls++
	return adapter.ExactFilterPlan{}, fmt.Errorf("legacy strategy must not be probed")
}

func membershipDescriptor(t *testing.T) profile.Descriptor {
	t.Helper()
	d, err := partscatalogselect.NewDescriptor(partscatalogselect.OperationalBinding{
		PhysicalListName: membershipList, ConfigSHA256: configSHA, TestedEngines: []string{"v0.6.0-1-gf7423cd"},
		TimeoutMillis: 10000, MaxInflight: 1, WorstCaseChargeBytes: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

type membershipCheck struct {
	Query  string          `json:"q"`
	Lists  []string        `json:"lists"`
	TopK   int             `json:"topk"`
	Filter json.RawMessage `json:"filter"`
}

func TestMembershipExecutorStoredStrategyRungsAndIdentityBounds(t *testing.T) {
	cases := []struct {
		name                             string
		saturate                         int
		badIdentity                      int
		wantCalls, wantPool, wantAttempt int
		wantError                        orchestrator.Category
	}{
		{"first", 0, 0, 1, 128, 1, ""},
		{"second", 1, 0, 2, 256, 1, ""},
		{"fourth", 3, 0, 4, 1000, 1, ""},
		{"ceiling", 4, 0, 4, 0, 0, orchestrator.CategoryIncompleteAtPoolCeiling},
		{"identity-restart", 0, 1, 2, 128, 2, ""},
		{"identity-exhausted", 0, 2, 2, 0, 0, orchestrator.CategoryListIdentityMismatch},
	}
	d := membershipDescriptor(t)
	purePlan, err := partscatalogselect.New().Compile(membershipRequest, membershipScope)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer r.Body.Close()
				call := int(calls.Add(1))
				var input struct {
					Checks []membershipCheck `json:"checks"`
				}
				if json.NewDecoder(r.Body).Decode(&input) != nil || len(input.Checks) != len(purePlan.Probes()) {
					t.Error("probe expansion")
					w.WriteHeader(500)
					return
				}
				results := make([]any, len(input.Checks))
				for i, check := range input.Checks {
					if string(check.Filter) != membershipWire || len(check.Lists) != 1 || check.Lists[0] != membershipList {
						t.Errorf("filter/scope/list drift: %s", check.Filter)
					}
					wantPool := 128
					if tc.badIdentity == 0 {
						wantPool = []int{128, 256, 512, 1000}[call-1]
					}
					if check.TopK != wantPool {
						t.Errorf("pool: %d want %d", check.TopK, wantPool)
					}
					candidates := make([]any, 0)
					if call <= tc.saturate {
						for n := 0; n < check.TopK; n++ {
							source := []byte(fmt.Sprintf(`{"id":"synthetic-%04d","identifier":"ZB-9","manufacturer":"Northstar","category":"bearing","status":"active","tenant_id":"tenant-a"}`, n))
							entries, err := partscatalogselect.New().Project(source)
							if err != nil {
								t.Error(err)
								w.WriteHeader(500)
								return
							}
							candidates = append(candidates, map[string]any{"list": membershipList, "entry_id": entries[0].EntryID(), "key": check.Query, "score": 100, "payload": json.RawMessage(entries[0].Payload())})
						}
					}
					served := stamp("a", 1000)
					if call <= tc.badIdentity {
						served = strings.Repeat("a", 64) + "@1000+j0+c" + strings.Repeat("f", 64)
					}
					results[i] = map[string]any{"candidates": candidates, "versions": map[string]string{membershipList: served},
						"filter": check.Filter, "filter_stats": map[string]any{membershipList: map[string]int{"evaluated": len(candidates), "rejected": 0}}, "took_us": 1}
				}
				w.Header().Set("Content-Type", "application/json")
				if err := json.NewEncoder(w).Encode(map[string]any{"results": results, "took_us": 1}); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			client, err := kurnhttp.NewClient(kurnhttp.ClientConfig{BaseURL: server.URL, BearerToken: "test-key", MaxResponseBytes: 64 << 20, MaxRetryAfter: time.Second})
			if err != nil {
				t.Fatal(err)
			}
			s := &membershipSemantics{}
			executor, err := orchestrator.NewWithMembership(d, s, client)
			if err != nil {
				t.Fatal(err)
			}
			if err := executor.Preflight(context.Background(), membershipRequest, membershipScope); err != nil || calls.Load() != 0 {
				t.Fatal("preflight I/O", err)
			}
			result, err := executor.Search(context.Background(), membershipRequest, membershipScope)
			if tc.wantError != "" {
				if category(t, err).Category != tc.wantError {
					t.Fatal(err)
				}
			} else if err != nil || result.Pool() != tc.wantPool || result.VersionAttempt() != tc.wantAttempt || len(result.Resolution().Hits()) != 0 {
				t.Fatalf("whole producing rung: %v", err)
			}
			if int(calls.Load()) != tc.wantCalls || s.membershipCalls != 2 || s.legacyCalls != 0 {
				t.Fatalf("dispatch/calls: %d %d %d", calls.Load(), s.membershipCalls, s.legacyCalls)
			}
		})
	}
}

type noMembershipIO struct{ calls int }

func (b *noMembershipIO) Batch(context.Context, []kurnhttp.Probe) (kurnhttp.BatchResult, error) {
	b.calls++
	return kurnhttp.BatchResult{}, fmt.Errorf("unexpected I/O")
}

func TestMembershipExecutorRefusesInvalidProvenanceWithoutFallback(t *testing.T) {
	d := membershipDescriptor(t)
	term := func(name, value string) adapter.ExactFilterTerm {
		v, e := adapter.NewExactFilterTerm(name, value)
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	member := func(name string) adapter.MembershipFilterTerm {
		v, e := adapter.NewMembershipFilterTerm(name, []string{"x", "y"})
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	plans := []adapter.MembershipFilterPlan{{}}
	for _, spec := range []adapter.MembershipFilterPlanSpec{
		{}, {Trusted: []adapter.ExactFilterTerm{term("unknown", "tenant-a")}},
		{Trusted: []adapter.ExactFilterTerm{term("tenant_id", "tenant-a")}, Caller: []adapter.MembershipFilterTerm{member("unknown")}},
		{Caller: []adapter.MembershipFilterTerm{member("tenant_id")}},
	} {
		p, e := adapter.NewMembershipFilterPlan(spec)
		if e != nil {
			t.Fatal(e)
		}
		plans = append(plans, p)
	}
	for i, p := range plans {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			b := &noMembershipIO{}
			s := &membershipSemantics{substitute: &p}
			e, err := orchestrator.NewWithMembership(d, s, b)
			if err != nil {
				t.Fatal(err)
			}
			if got := category(t, e.Preflight(context.Background(), membershipRequest, membershipScope)); got.Category != orchestrator.CategoryInvalidFilter {
				t.Fatal(got)
			}
			_, err = e.Search(context.Background(), membershipRequest, membershipScope)
			if category(t, err).Category != orchestrator.CategoryInvalidFilter || b.calls != 0 || s.legacyCalls != 0 {
				t.Fatal("fallback or invalid classification", err)
			}
		})
	}
	var nilSemantics *membershipSemantics
	if _, err := orchestrator.NewWithMembership(d, nilSemantics, &noMembershipIO{}); err == nil {
		t.Fatal("typed nil")
	}
	var nilBackend *noMembershipIO
	if _, err := orchestrator.NewWithMembership(d, &membershipSemantics{}, nilBackend); err == nil {
		t.Fatal("typed nil backend")
	}
}

func TestMembershipExecutorRequestAndScopeEnvelopeBoundaries(t *testing.T) {
	d := membershipDescriptor(t)
	if d.Spec().Bounds.MaxRequestBytes != 1<<20 || d.Spec().Bounds.MaxTrustedScopeBytes != 64<<10 {
		t.Fatal("descriptor envelope changed")
	}
	pad := func(raw []byte, size int) []byte {
		return append([]byte(strings.Repeat(" ", size-len(raw))), raw...)
	}
	for _, tc := range []struct {
		name           string
		request, scope []byte
		valid          bool
	}{
		{"request-at-bound", pad(membershipRequest, 1<<20), membershipScope, true},
		{"scope-at-bound", membershipRequest, pad(membershipScope, 64<<10), true},
		{"request-over-bound", pad(membershipRequest, (1<<20)+1), membershipScope, false},
		{"scope-over-bound", membershipRequest, pad(membershipScope, (64<<10)+1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &noMembershipIO{}
			s := &membershipSemantics{}
			e, err := orchestrator.NewWithMembership(d, s, b)
			if err != nil {
				t.Fatal(err)
			}
			_, compileErr := partscatalogselect.New().Compile(tc.request, tc.scope)
			_, filterErr := partscatalogselect.New().CompileMembershipFilter(tc.request, tc.scope)
			preflightErr := e.Preflight(context.Background(), tc.request, tc.scope)
			if tc.valid {
				if compileErr != nil || filterErr != nil || preflightErr != nil || b.calls != 0 || s.membershipCalls != 1 {
					t.Fatal("at-bound pure/preflight control failed", compileErr, filterErr, preflightErr)
				}
			} else {
				if compileErr == nil || filterErr == nil || category(t, preflightErr).Category != orchestrator.CategoryInvalidInput {
					t.Fatal("over-bound pure/preflight refusal missing")
				}
				_, searchErr := e.Search(context.Background(), tc.request, tc.scope)
				if category(t, searchErr).Category != orchestrator.CategoryInvalidInput || b.calls != 0 || s.membershipCalls != 0 {
					t.Fatal("over-bound search reached filter/backend", searchErr)
				}
			}
			if s.legacyCalls != 0 {
				t.Fatal("envelope check fell back to legacy strategy")
			}
		})
	}
}
