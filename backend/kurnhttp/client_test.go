package kurnhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

var (
	testBaseHash    = strings.Repeat("a", 64)
	testJournalHash = strings.Repeat("b", 64)
	testConfigHash  = strings.Repeat("c", 64)
	testStamp       = testBaseHash + "@2+j0+c" + testConfigHash
)

func intPtr(value int) *int           { return &value }
func floatPtr(value float64) *float64 { return &value }
func validProbeSpec() ProbeSpec {
	return ProbeSpec{Query: "needle", List: "catalog-v1", ExpectedConfigSHA256: testConfigHash}
}
func mustProbe(t *testing.T, spec ProbeSpec) Probe {
	t.Helper()
	probe, err := NewProbe(spec)
	if err != nil {
		t.Fatal(err)
	}
	return probe
}

func testClient(t *testing.T, base string, client *http.Client, maxBytes int64) *Client {
	t.Helper()
	if maxBytes == 0 {
		maxBytes = 1 << 20
	}
	got, err := NewClient(ClientConfig{
		BaseURL: base, BearerToken: "test-token", HTTPClient: client,
		MaxResponseBytes: maxBytes, MaxRetryAfter: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestBatchRequestGoldenAndFilteredSuccess(t *testing.T) {
	filter := mustFilter(t,
		FilterTerm{Name: "variant", Value: "v1"},
		FilterTerm{Name: "factory", Value: "north"},
	)
	spec := validProbeSpec()
	spec.Query = "A<&\n"
	spec.Threshold = floatPtr(0)
	spec.TopK = intPtr(25)
	spec.Filter = filter
	probe := mustProbe(t, spec)
	wantBody := `{"checks":[{"q":"A<&\n","lists":["catalog-v1"],"threshold":0,"topk":25,"filter":{"factory":"north","variant":"v1"}}]}`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/batch-query" || r.URL.RawQuery != "" {
			t.Errorf("request target = %s %s", r.Method, r.URL.RequestURI())
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("Authorization = %q", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q", got)
		}
		if got := r.Header.Get("Accept"); got != "application/json" {
			t.Errorf("Accept = %q", got)
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != wantBody {
			t.Errorf("body = %s, want %s", body, wantBody)
		}
		if strings.Contains(string(body), testConfigHash) {
			t.Error("local expected digest entered request bytes")
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		fmt.Fprintf(w, `{"results":[{"candidates":[],"versions":{"catalog-v1":%q},"filter":{"factory":"north","variant":"v1"},"filter_stats":{"catalog-v1":{"evaluated":2,"rejected":2}},"took_us":3}],"took_us":4}`, testStamp)
	}))
	defer server.Close()

	result, err := testClient(t, server.URL, nil, 0).Batch(context.Background(), []Probe{probe})
	if err != nil {
		t.Fatal(err)
	}
	if result.TookUS() != 4 || len(result.Results()) != 1 {
		t.Fatalf("batch result shape is wrong")
	}
	probeResult := result.Results()[0]
	stats, ok := probeResult.FilterStats()
	if probeResult.TookUS() != 3 || probeResult.Stamp().Raw != testStamp ||
		string(probeResult.AppliedFilter()) != string(filter.CanonicalJSON()) ||
		!ok || stats != (FilterStats{Evaluated: 2, Rejected: 2}) ||
		probeResult.Candidates() == nil || len(probeResult.Candidates()) != 0 {
		t.Fatalf("probe result is wrong")
	}
}

func TestMembershipBatchWireAndStrictEcho(t *testing.T) {
	filter, err := NewMembershipFilter([]MembershipTerm{
		{Name: "tenant_id", Values: []string{"tenant-a"}},
		{Name: "category", Values: []string{"filter", "bearing", "bearing"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	spec := validProbeSpec()
	spec.Membership = filter
	spec.TopK = intPtr(128)
	probe := mustProbe(t, spec)
	wantFilter := `{"category":{"in":["bearing","filter"]},"tenant_id":"tenant-a"}`
	wantBody := `{"checks":[{"q":"needle","lists":["catalog-v1"],"topk":128,"filter":` + wantFilter + `}]}`
	for _, tt := range []struct {
		name string
		echo string
		pass bool
	}{
		{"canonical", wantFilter, true},
		{"wrong-order", `{"tenant_id":"tenant-a","category":{"in":["bearing","filter"]}}`, false},
		{"wrong-member-order", `{"category":{"in":["filter","bearing"]},"tenant_id":"tenant-a"}`, false},
		{"duplicate-member", `{"category":{"in":["bearing","bearing","filter"]},"tenant_id":"tenant-a"}`, false},
		{"missing-scope", `{"category":{"in":["bearing","filter"]}}`, false},
		{"wrong-scope", `{"category":{"in":["bearing","filter"]},"tenant_id":"tenant-b"}`, false},
		{"changed-type", `{"category":"bearing","tenant_id":"tenant-a"}`, false},
		{"added-whitespace", `{"category": {"in":["bearing","filter"]},"tenant_id":"tenant-a"}`, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				body, err := io.ReadAll(r.Body)
				if err != nil || string(body) != wantBody {
					t.Errorf("membership batch is not the exact one-probe golden")
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"results":[{"candidates":[],"versions":{"catalog-v1":%q},"filter":%s,"filter_stats":{"catalog-v1":{"evaluated":7,"rejected":7}},"took_us":3}],"took_us":4}`, testStamp, tt.echo)
			}))
			defer server.Close()
			result, err := testClient(t, server.URL, nil, 0).Batch(context.Background(), []Probe{probe})
			if tt.pass {
				if err != nil || string(result.Results()[0].AppliedFilter()) != wantFilter {
					t.Fatalf("canonical membership response refused: %v", err)
				}
			} else {
				requireCategory(t, err, CategoryFilterMismatch)
			}
			if calls.Load() != 1 {
				t.Fatal("echo failure retried or membership fanned out")
			}
		})
	}
}

func TestProbeRefusesBothFilterModesAndSnapshotsOne(t *testing.T) {
	exact := mustFilter(t, FilterTerm{Name: "category", Value: "bearing"})
	membership, err := NewMembershipFilter([]MembershipTerm{{Name: "category", Values: []string{"bearing", "filter"}}})
	if err != nil {
		t.Fatal(err)
	}
	spec := validProbeSpec()
	spec.Filter, spec.Membership = exact, membership
	_, err = NewProbe(spec)
	requireCategory(t, err, CategoryInvalidRequest)
	spec.Filter = StringFilter{}
	probe := mustProbe(t, spec)
	want := string(marshalBatch([]Probe{probe}))
	// Package-internal mutation also cannot change the probe's byte snapshot.
	membership.canonical[0] = '!'
	membership.terms[0].Values[0] = "changed"
	if probe.filterMode != probeMembershipFilter || string(marshalBatch([]Probe{probe})) != want {
		t.Fatal("membership probe aliases the constructed filter")
	}
	spec.Membership, spec.Filter = MembershipFilter{}, exact
	legacy := mustProbe(t, spec)
	want = string(marshalBatch([]Probe{legacy}))
	exact.canonical[0] = '!'
	if legacy.filterMode != probeStringFilter || string(marshalBatch([]Probe{legacy})) != want {
		t.Fatal("legacy probe lost its exact canonical snapshot")
	}
}

type membershipCountedBody struct {
	reader io.Reader
	read   int
	closed bool
}

func (b *membershipCountedBody) Read(p []byte) (int, error) {
	n, err := b.reader.Read(p)
	b.read += n
	return n, err
}

func (b *membershipCountedBody) Close() error {
	b.closed = true
	return nil
}

// R09 is a synthetic envelope control, not a claim the parts recipe emits
// 64 probes or that a maximum-candidate response succeeds under this bound.
func TestMembershipMaximumBatchEnvelopeAndResponseBound(t *testing.T) {
	const executorProbeLimit = 64
	const publicBodyLimit = 32 << 20
	if maxBatchProbes != 100 || maxQueryRunes != 512 || MaxMembershipFilterBytes != 16384 {
		t.Fatal("the separately owned protocol/field bounds changed")
	}
	category, manufacturer := make([]string, 16), make([]string, 16)
	for i := range category {
		category[i] = fmt.Sprintf("c%02d", i) + strings.Repeat("x", 509)
		manufacturer[i] = fmt.Sprintf("m%02d", i) + strings.Repeat("x", 509)
	}
	manufacturer[15] = "m15" + strings.Repeat("x", 345)
	filter, err := NewMembershipFilter([]MembershipTerm{
		{Name: "category", Values: category}, {Name: "manufacturer", Values: manufacturer},
		{Name: "tenant_id", Values: []string{"tenant-a"}},
	})
	if err != nil || len(filter.CanonicalJSON()) != 16384 {
		t.Fatal("at-cap fixture", err)
	}
	spec := validProbeSpec()
	spec.Query = strings.Repeat("\x00", maxQueryRunes) // six JSON bytes per scalar
	spec.List = strings.Repeat("z", 64)
	spec.Threshold = floatPtr(math.Nextafter(1, 0))
	spec.TopK = intPtr(1000)
	spec.Membership = filter
	probe := mustProbe(t, spec)
	probes := make([]Probe, executorProbeLimit)
	for i := range probes {
		probes[i] = probe
	}
	body := marshalBatch(probes)
	// This golden spells the escaped query and field overhead independently
	// of marshalBatch/appendCanonicalString, then repeats the complete check.
	check := `{"q":"` + strings.Repeat(`\u0000`, 512) + `","lists":["` + spec.List +
		`"],"threshold":0.9999999999999999,"topk":1000,"filter":` + string(filter.CanonicalJSON()) + `}`
	want := `{"checks":[` + strings.Repeat(check+",", executorProbeLimit-1) + check + `]}`
	if !bytes.Equal(body, []byte(want)) || !json.Valid(body) || len(body) >= publicBodyLimit ||
		bytes.Count(body, filter.CanonicalJSON()) != executorProbeLimit {
		t.Fatal("maximum membership batch is not the bounded complete golden")
	}
	// Reserve 32 bytes for any valid serialized threshold, not only this
	// particular long threshold; this is a source-derived overhead ceiling.
	upper := len(body) + executorProbeLimit*(32-len("0.9999999999999999"))
	if upper >= publicBodyLimit {
		t.Fatal("derived numeric-overhead bound exceeds the public body ceiling")
	}
	item := `{"candidates":[],"versions":{"` + spec.List + `":"` + testStamp + `"},"filter":` +
		string(filter.CanonicalJSON()) + `,"filter_stats":{"` + spec.List + `":{"evaluated":0,"rejected":0}},"took_us":0}`
	response := `{"results":[` + strings.Repeat(item+",", executorProbeLimit-1) + item + `],"took_us":0}`
	for _, tc := range []struct {
		name string
		cap  int
		pass bool
	}{
		{"exact-bound-success", len(response), true},
		{"one-byte-over-refused", len(response) - 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stream := &membershipCountedBody{reader: strings.NewReader(response)}
			calls := 0
			transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
				calls++
				defer request.Body.Close()
				raw, readErr := io.ReadAll(io.LimitReader(request.Body, publicBodyLimit+1))
				if readErr != nil || !bytes.Equal(raw, body) || request.Method != http.MethodPost || request.URL.Path != "/v1/batch-query" {
					t.Error("transport did not receive the complete bounded batch")
				}
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: stream}, nil
			})
			client := testClient(t, "http://backend.invalid", &http.Client{Transport: transport}, int64(tc.cap))
			result, err := client.Batch(context.Background(), probes)
			if tc.pass {
				if err != nil || len(result.Results()) != executorProbeLimit {
					t.Fatal("complete echoed response refused", err)
				}
				for _, got := range result.Results() {
					if !bytes.Equal(got.AppliedFilter(), filter.CanonicalJSON()) || len(got.Candidates()) != 0 {
						t.Fatal("complete response lost membership evidence")
					}
				}
			} else {
				requireCategory(t, err, CategoryResponseTooLarge)
				if len(result.Results()) != 0 {
					t.Fatal("oversized response returned partial evidence")
				}
			}
			if calls != 1 || !stream.closed || stream.read != len(response) || stream.read > tc.cap+1 {
				t.Fatal("response bound, close or no-retry census", calls, stream.read, stream.closed)
			}
		})
	}
	t.Logf("synthetic R09: %d probes; canonical filter bytes/probe %d; actual serialized request %d bytes; derived overhead ceiling %d; complete empty-candidate response %d bytes", executorProbeLimit, len(filter.CanonicalJSON()), len(body), upper, len(response))
}

// These response spellings are literal goldens, not produced by the new
// probe expectation helper. The encoder comparison is a second oracle.
func TestFilteredTransportEchoGoldensBothModes(t *testing.T) {
	type vector struct {
		name, value, canonical, wire string
	}
	vectors := []vector{
		{"less", "<", `{"category":"<","tenant_id":"tenant-a"}`, `{"category":"\u003c","tenant_id":"tenant-a"}`},
		{"greater", ">", `{"category":">","tenant_id":"tenant-a"}`, `{"category":"\u003e","tenant_id":"tenant-a"}`},
		{"ampersand", "&", `{"category":"&","tenant_id":"tenant-a"}`, `{"category":"\u0026","tenant_id":"tenant-a"}`},
		{"line-separator", "\u2028", "{\"category\":\"\u2028\",\"tenant_id\":\"tenant-a\"}", `{"category":"\u2028","tenant_id":"tenant-a"}`},
		{"paragraph-separator", "\u2029", "{\"category\":\"\u2029\",\"tenant_id\":\"tenant-a\"}", `{"category":"\u2029","tenant_id":"tenant-a"}`},
		{"mixed", "<a>&/'\u2028\u2029", "{\"category\":\"<a>&/'\u2028\u2029\",\"tenant_id\":\"tenant-a\"}", `{"category":"\u003ca\u003e\u0026/'\u2028\u2029","tenant_id":"tenant-a"}`},
		{"live-mixed", "<a>&/'\u2028\u2029z", "{\"category\":\"<a>&/'\u2028\u2029z\",\"tenant_id\":\"tenant-a\"}", `{"category":"\u003ca\u003e\u0026/'\u2028\u2029z","tenant_id":"tenant-a"}`},
		{"slash-apostrophe-control", "/'", `{"category":"/'","tenant_id":"tenant-a"}`, `{"category":"/'","tenant_id":"tenant-a"}`},
		{"ascii", "bearing", `{"category":"bearing","tenant_id":"tenant-a"}`, `{"category":"bearing","tenant_id":"tenant-a"}`},
		{"quote-backslash", "\"\\", `{"category":"\"\\","tenant_id":"tenant-a"}`, `{"category":"\"\\","tenant_id":"tenant-a"}`},
		{"short-controls", "\b\f\n\r\t", `{"category":"\b\f\n\r\t","tenant_id":"tenant-a"}`, `{"category":"\b\f\n\r\t","tenant_id":"tenant-a"}`},
		{"long-control", "\x01", `{"category":"\u0001","tenant_id":"tenant-a"}`, `{"category":"\u0001","tenant_id":"tenant-a"}`},
		{"non-ascii", "é", `{"category":"é","tenant_id":"tenant-a"}`, `{"category":"é","tenant_id":"tenant-a"}`},
		{"del", "\x7f", "{\"category\":\"\x7f\",\"tenant_id\":\"tenant-a\"}", "{\"category\":\"\x7f\",\"tenant_id\":\"tenant-a\"}"},
	}
	for _, v := range vectors {
		t.Run(v.name, func(t *testing.T) {
			filter, err := NewMembershipFilter([]MembershipTerm{
				{Name: "category", Values: []string{v.value}}, {Name: "tenant_id", Values: []string{"tenant-a"}},
			})
			if err != nil || string(filter.CanonicalJSON()) != v.canonical {
				t.Fatal("canonical literal disagrees", err)
			}
			wire, err := json.Marshal(json.RawMessage(v.canonical))
			if err != nil || string(wire) != v.wire {
				t.Fatal("literal transport spelling disagrees with fixed encoder", err)
			}
			spec := validProbeSpec()
			spec.Membership = filter
			member := mustProbe(t, spec)
			spec.Membership = MembershipFilter{}
			spec.Filter = mustFilter(t, FilterTerm{Name: "category", Value: v.value}, FilterTerm{Name: "tenant_id", Value: "tenant-a"})
			legacy := mustProbe(t, spec)
			for _, mode := range []struct {
				name  string
				probe Probe
			}{{"membership", member}, {"legacy", legacy}} {
				echoes := []struct{ name, raw string }{{"wire", v.wire}}
				if v.canonical != v.wire {
					echoes = append(echoes, struct{ name, raw string }{"raw-canonical", v.canonical})
				}
				for _, echo := range echoes {
					t.Run(mode.name+"/"+echo.name, func(t *testing.T) {
						want := v.wire
						response := membershipEchoResponse(echo.raw)
						stream := &membershipCountedBody{reader: strings.NewReader(response)}
						calls := 0
						transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
							calls++
							defer request.Body.Close()
							raw, err := io.ReadAll(request.Body)
							expected := `{"checks":[{"q":"needle","lists":["catalog-v1"],"filter":` + v.canonical + `}]}`
							if err != nil || string(raw) != expected {
								t.Error("request P changed", err)
							}
							return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: stream}, nil
						})
						result, err := testClient(t, "http://backend.invalid", &http.Client{Transport: transport}, 0).Batch(context.Background(), []Probe{mode.probe})
						if echo.raw == want {
							if err != nil || len(result.Results()) != 1 || string(result.Results()[0].AppliedFilter()) != echo.raw {
								t.Fatal("exact transport response lost", err)
							}
							copyEcho := result.Results()[0].AppliedFilter()
							copyEcho[0] = '!'
							if string(result.Results()[0].AppliedFilter()) != echo.raw {
								t.Fatal("applied-filter accessor aliases evidence")
							}
						} else {
							requireCategory(t, err, CategoryFilterMismatch)
							if len(result.Results()) != 0 {
								t.Fatal("refusal leaked partial results")
							}
						}
						if calls != 1 || !stream.closed {
							t.Fatal("echo disposition retried or left body open")
						}
					})
				}
			}
		})
	}
}

func membershipEchoResponse(echo string) string {
	field := ""
	if echo != "" {
		field = `,"filter":` + echo
	}
	return `{"results":[{"candidates":[],"versions":{"catalog-v1":"` + testStamp +
		`"},"took_us":0` + field + `,"filter_stats":{"catalog-v1":{"evaluated":0,"rejected":0}}}],"took_us":0}`
}

// S4-B04: the hostile bodies are literal bytes. Passing them through a JSON
// encoder here would repair some mutations and invalidate the refusal proof.
func TestScalarTransportEchoRejectsEquivalentSpellings(t *testing.T) {
	const canonical = `{"category":"<&/z","partition_id":"p>","tenant_id":"t&"}`
	const wire = `{"category":"\u003c\u0026/z","partition_id":"p\u003e","tenant_id":"t\u0026"}`
	spec := validProbeSpec()
	spec.Filter = mustFilter(t, FilterTerm{Name: "category", Value: "<&/z"},
		FilterTerm{Name: "partition_id", Value: "p>"}, FilterTerm{Name: "tenant_id", Value: "t&"})
	probe := mustProbe(t, spec)
	if string(probe.filterJSON) != canonical || string(probe.expectedEchoJSON) != wire {
		t.Fatal("scalar trusted snapshots disagree with independent literals")
	}
	cases := []struct{ name, echo string }{
		{"exact", wire},
		{"raw-canonical", canonical},
		{"uppercase-escape", strings.Replace(wire, `\u003c`, `\u003C`, 1)},
		{"escaped-slash", strings.Replace(wire, "/", `\/`, 1)},
		{"escaped-ascii", strings.Replace(wire, "/z", `/\u007a`, 1)},
		{"changed-business", strings.Replace(wire, "/z", "/x", 1)},
		{"changed-tenant", strings.Replace(wire, `t\u0026`, "other", 1)},
		{"changed-partition", strings.Replace(wire, `p\u003e`, "other", 1)},
		{"raw-tenant", strings.Replace(wire, `t\u0026`, "t&", 1)},
		{"raw-partition", strings.Replace(wire, `p\u003e`, "p>", 1)},
		{"reordered-names", `{"tenant_id":"t\u0026","category":"\u003c\u0026/z","partition_id":"p\u003e"}`},
		{"duplicate-name", strings.Replace(wire, `"tenant_id":`, `"tenant_id":"t\u0026","tenant_id":`, 1)},
		{"extra-name", strings.Replace(wire, `{"category":`, `{"extra":"x","category":`, 1)},
		{"whitespace", strings.Replace(wire, `{"category":`, `{ "category":`, 1)},
		{"singleton-in", strings.Replace(wire, `"category":"\u003c\u0026/z"`, `"category":{"in":["\u003c\u0026/z"]}`, 1)},
		{"omitted-scope", strings.Replace(wire, `,"partition_id":"p\u003e"`, "", 1)},
		{"omitted-business", strings.Replace(wire, `"category":"\u003c\u0026/z",`, "", 1)},
		{"omitted-filter", ""},
		{"null-filter", "null"},
	}
	if len(cases) != 19 {
		t.Fatal("expected one success control and eighteen scalar refusals")
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			stream := &membershipCountedBody{reader: strings.NewReader(membershipEchoResponse(tc.echo))}
			transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
				calls++
				defer request.Body.Close()
				raw, err := io.ReadAll(request.Body)
				if err != nil || string(raw) != `{"checks":[{"q":"needle","lists":["catalog-v1"],"filter":`+canonical+`}]}` {
					t.Error("scalar request P changed", err)
				}
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: stream}, nil
			})
			result, err := testClient(t, "http://backend.invalid", &http.Client{Transport: transport}, 0).Batch(context.Background(), []Probe{probe})
			if tc.name == "exact" {
				if err != nil || len(result.Results()) != 1 || string(result.Results()[0].AppliedFilter()) != wire {
					t.Fatal("exact scalar wire refused", err)
				}
			} else {
				requireCategory(t, err, CategoryFilterMismatch)
				if len(result.Results()) != 0 {
					t.Fatal("hostile scalar echo yielded partial results")
				}
			}
			if calls != 1 || !stream.closed {
				t.Fatal("scalar echo retried or left body open")
			}
		})
	}
}

// S4-B07/B08: measured scripted serialization, not a live batch/capacity claim.
func TestScalarMaximumSerializedResponseBounds(t *testing.T) {
	const maximum = 30769
	glyphs := []string{"<", ">", "&", "\u2028", "\u2029"}
	finalControls := []rune{1, 2, 3, 4, 5, 6, 7, 14}
	for _, shape := range []string{"c0-max", "echo-max"} {
		t.Run(shape, func(t *testing.T) {
			terms := make([]FilterTerm, 8)
			for i := range terms {
				if shape == "c0-max" {
					terms[i] = FilterTerm{Name: strings.Repeat("\x01", 127) + string(finalControls[i]), Value: strings.Repeat("\x01", 512)}
				} else {
					terms[i] = FilterTerm{Name: strings.Repeat("<", 126) + glyphs[i/5] + glyphs[i%5], Value: strings.Repeat("&", 512)}
				}
			}
			filter := mustFilter(t, terms...)
			canonical := filter.CanonicalJSON()
			// Independent spelling oracle: these fixtures contain only long C0
			// escapes or the five glyphs, with no quotes/backslashes in data.
			wire := strings.NewReplacer("<", `\u003c`, ">", `\u003e`, "&", `\u0026`, "\u2028", `\u2028`, "\u2029", `\u2029`).Replace(string(canonical))
			encoded, err := json.Marshal(json.RawMessage(canonical))
			if err != nil || !bytes.Equal(encoded, []byte(wire)) || len(wire) != maximum || len(canonical) > maximum ||
				(shape == "c0-max" && len(canonical) != maximum) {
				t.Fatal("scalar maximum construction or independent echo disagrees", err)
			}
			spec := validProbeSpec()
			spec.Filter = filter
			probe := mustProbe(t, spec)
			for _, count := range []int{1, 64, 100} {
				t.Run(fmt.Sprint(count), func(t *testing.T) {
					probes := make([]Probe, count)
					items := make([]json.RawMessage, count)
					item := `{"candidates":[],"versions":{"catalog-v1":"` + testStamp + `"},"filter":` + string(canonical) +
						`,"filter_stats":{"catalog-v1":{"evaluated":0,"rejected":0}},"took_us":0}`
					for i := range probes {
						probes[i], items[i] = probe, json.RawMessage(item)
					}
					requestBody := marshalBatch(probes)
					response, err := json.Marshal(struct {
						Results []json.RawMessage `json:"results"`
						TookUS  int               `json:"took_us"`
					}{items, 0})
					if err != nil || bytes.Count(requestBody, canonical) != count || bytes.Count(response, []byte(wire)) != count {
						t.Fatal("actual scalar envelope lacks exact request/echo inventory", err)
					}
					for _, pass := range []bool{true, false} {
						t.Run(fmt.Sprintf("exact-cap-%t", pass), func(t *testing.T) {
							capBytes := len(response)
							if !pass {
								capBytes--
							}
							stream := &membershipCountedBody{reader: bytes.NewReader(response)}
							calls := 0
							transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
								calls++
								defer request.Body.Close()
								raw, err := io.ReadAll(request.Body)
								if err != nil || !bytes.Equal(raw, requestBody) {
									t.Error("bounded scalar request changed", err)
								}
								return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: stream}, nil
							})
							result, err := testClient(t, "http://backend.invalid", &http.Client{Transport: transport}, int64(capBytes)).Batch(context.Background(), probes)
							if pass {
								if err != nil || len(result.Results()) != count {
									t.Fatal("exact scalar response cap refused", err)
								}
								for _, r := range result.Results() {
									if string(r.AppliedFilter()) != wire || len(r.Candidates()) != 0 {
										t.Fatal("scalar raw evidence lost")
									}
								}
							} else {
								requireCategory(t, err, CategoryResponseTooLarge)
								if len(result.Results()) != 0 {
									t.Fatal("oversized scalar response yielded partial results")
								}
							}
							if calls != 1 || !stream.closed || stream.read != len(response) || stream.read > capBytes+1 {
								t.Fatal("scalar size/closure/no-retry census failed")
							}
						})
					}
					t.Logf("S4 scripted %s: probes=%d canonical=%d echo=%d request=%d response=%d; derived echo-only ceiling=%d excluding envelope/candidates", shape, count, len(canonical), len(wire), len(requestBody), len(response), count*maximum)
				})
			}
		})
	}
}

func TestMembershipTransportEchoRejectsEquivalentSpellings(t *testing.T) {
	filter, err := NewMembershipFilter([]MembershipTerm{
		{Name: "category", Values: []string{"z", "<&", "/"}},
		{Name: "partition_id", Values: []string{"p>"}},
		{Name: "tenant_id", Values: []string{"t&"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	const canonical = `{"category":{"in":["/","<&","z"]},"partition_id":"p>","tenant_id":"t&"}`
	const wire = `{"category":{"in":["/","\u003c\u0026","z"]},"partition_id":"p\u003e","tenant_id":"t\u0026"}`
	if string(filter.CanonicalJSON()) != canonical {
		t.Fatal("multi-member/scope canonical literal drift")
	}
	spec := validProbeSpec()
	spec.Membership = filter
	probe := mustProbe(t, spec)
	for _, tc := range []struct{ name, echo string }{
		{"exact", wire},
		{"raw-canonical", canonical},
		{"uppercase-escape", strings.Replace(wire, `\u003c`, `\u003C`, 1)},
		{"escaped-slash", strings.Replace(wire, `"/"`, `"\/"`, 1)},
		{"escaped-ascii", strings.Replace(wire, `"z"`, `"\u007a"`, 1)},
		{"changed-member", strings.Replace(wire, `"z"`, `"x"`, 1)},
		{"changed-scope", strings.Replace(wire, `"t\u0026"`, `"other"`, 1)},
		{"raw-partition", strings.Replace(wire, `p\u003e`, "p>", 1)},
		{"raw-tenant", strings.Replace(wire, `t\u0026`, "t&", 1)},
		{"duplicate-member", strings.Replace(wire, `"z"`, `"z","z"`, 1)},
		{"reordered-members", `{"category":{"in":["\u003c\u0026","/","z"]},"partition_id":"p\u003e","tenant_id":"t\u0026"}`},
		{"reordered-names", `{"tenant_id":"t\u0026","category":{"in":["/","\u003c\u0026","z"]},"partition_id":"p\u003e"}`},
		{"duplicate-name", strings.Replace(wire, `"tenant_id":`, `"tenant_id":"t\u0026","tenant_id":`, 1)},
		{"whitespace", strings.Replace(wire, `{"in":`, `{ "in":`, 1)},
		{"type-changed", `{"category":["/","\u003c\u0026","z"],"partition_id":"p\u003e","tenant_id":"t\u0026"}`},
		{"omitted-scope", `{"category":{"in":["/","\u003c\u0026","z"]},"tenant_id":"t\u0026"}`},
		{"omitted-echo", ""},
		{"null-echo", "null"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			stream := &membershipCountedBody{reader: strings.NewReader(membershipEchoResponse(tc.echo))}
			transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
				calls++
				defer request.Body.Close()
				raw, err := io.ReadAll(request.Body)
				if err != nil || !bytes.Contains(raw, []byte(canonical)) {
					t.Error("trusted P changed")
				}
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: stream}, nil
			})
			result, err := testClient(t, "http://backend.invalid", &http.Client{Transport: transport}, 0).Batch(context.Background(), []Probe{probe})
			if tc.name == "exact" {
				if err != nil || len(result.Results()) != 1 || string(result.Results()[0].AppliedFilter()) != wire {
					t.Fatal("exact multi-member and scope transport refused", err)
				}
			} else {
				requireCategory(t, err, CategoryFilterMismatch)
				if len(result.Results()) != 0 {
					t.Fatal("refusal returned partial results")
				}
			}
			if calls != 1 || !stream.closed {
				t.Fatal("hostile echo retried or left body open")
			}
		})
	}
}

// R09 supplement: actual serialization and scripted transport, not a
// 64-probe live recipe execution or a maximum-payload/capacity claim.
func TestMembershipHTMLHeavyMaximumResponseBound(t *testing.T) {
	const probeCount = 64
	category, manufacturer := make([]string, 16), make([]string, 16)
	for i := range category {
		category[i] = fmt.Sprintf("c%02d", i) + strings.Repeat("<", 509)
		manufacturer[i] = fmt.Sprintf("m%02d", i) + strings.Repeat("<", 509)
	}
	manufacturer[15] = "m15" + strings.Repeat("<", 345)
	filter, err := NewMembershipFilter([]MembershipTerm{
		{Name: "category", Values: category}, {Name: "manufacturer", Values: manufacturer},
		{Name: "tenant_id", Values: []string{"tenant-a"}},
	})
	if err != nil || len(filter.CanonicalJSON()) != 16384 {
		t.Fatal("HTML-heavy fixture must meet the same canonical cap", err)
	}
	canonical := filter.CanonicalJSON()
	wire := strings.ReplaceAll(string(canonical), "<", `\u003c`)
	encoded, err := json.Marshal(json.RawMessage(canonical))
	if err != nil || string(encoded) != wire || len(wire) != len(canonical)+5*16124 || len(wire) > 6*MaxMembershipFilterBytes {
		t.Fatal("literal and actual wire expansion disagree", err)
	}
	spec := validProbeSpec()
	spec.Query, spec.List = strings.Repeat("\x00", maxQueryRunes), strings.Repeat("z", 64)
	spec.Threshold, spec.TopK, spec.Membership = floatPtr(math.Nextafter(1, 0)), intPtr(1000), filter
	probe := mustProbe(t, spec)
	probes := make([]Probe, probeCount)
	for i := range probes {
		probes[i] = probe
	}
	requestBody := marshalBatch(probes)
	if len(requestBody) != 1254092 || bytes.Count(requestBody, canonical) != probeCount || len(requestBody) >= 32<<20 {
		t.Fatal("unchanged canonical request envelope drifted")
	}
	item := `{"candidates":[],"versions":{"` + spec.List + `":"` + testStamp + `"},"filter":` +
		string(canonical) + `,"filter_stats":{"` + spec.List + `":{"evaluated":0,"rejected":0}},"took_us":0}`
	envelope := struct {
		Results []json.RawMessage `json:"results"`
		TookUS  int               `json:"took_us"`
	}{Results: make([]json.RawMessage, probeCount)}
	for i := range envelope.Results {
		envelope.Results[i] = json.RawMessage(item)
	}
	response, err := json.Marshal(envelope)
	if err != nil || bytes.Count(response, []byte(wire)) != probeCount || len(response) >= 64<<20 {
		t.Fatal("actual serialized response lacks the expected bounded echoes", err)
	}
	for _, tc := range []struct {
		name string
		cap  int
		pass bool
	}{{"exact-bound-success", len(response), true}, {"one-byte-over-refused", len(response) - 1, false}} {
		t.Run(tc.name, func(t *testing.T) {
			stream := &membershipCountedBody{reader: bytes.NewReader(response)}
			calls := 0
			transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
				calls++
				defer request.Body.Close()
				raw, err := io.ReadAll(request.Body)
				if err != nil || !bytes.Equal(raw, requestBody) {
					t.Error("HTML-heavy canonical request changed")
				}
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: stream}, nil
			})
			result, err := testClient(t, "http://backend.invalid", &http.Client{Transport: transport}, int64(tc.cap)).Batch(context.Background(), probes)
			if tc.pass {
				if err != nil || len(result.Results()) != probeCount {
					t.Fatal("at-bound response refused", err)
				}
				for _, r := range result.Results() {
					if string(r.AppliedFilter()) != wire || len(r.Candidates()) != 0 {
						t.Fatal("actual raw echo was not retained")
					}
				}
			} else {
				requireCategory(t, err, CategoryResponseTooLarge)
				if len(result.Results()) != 0 {
					t.Fatal("oversized response yielded partial results")
				}
			}
			if calls != 1 || !stream.closed || stream.read != len(response) || stream.read > tc.cap+1 {
				t.Fatal("response bound/closure/no-retry census failed")
			}
		})
	}
	t.Logf("scripted R09 HTML-heavy: %d probes; canonical %d bytes/filter; actual wire %d bytes/filter; request %d bytes; response %d bytes; derived echo-only ceiling %d bytes, excluding envelope/candidates", probeCount, len(canonical), len(wire), len(requestBody), len(response), probeCount*6*MaxMembershipFilterBytes)
}

func TestClientConfigurationBounds(t *testing.T) {
	base := ClientConfig{BaseURL: "http://backend.invalid", BearerToken: "token", MaxResponseBytes: 1, MaxRetryAfter: time.Second}
	tests := []struct {
		id     string
		mutate func(*ClientConfig)
	}{
		{id: "relative", mutate: func(c *ClientConfig) { c.BaseURL = "backend.invalid" }},
		{id: "scheme", mutate: func(c *ClientConfig) { c.BaseURL = "ftp://backend.invalid" }},
		{id: "host", mutate: func(c *ClientConfig) { c.BaseURL = "http:///path" }},
		{id: "userinfo", mutate: func(c *ClientConfig) { c.BaseURL = "http://user@backend.invalid" }},
		{id: "path", mutate: func(c *ClientConfig) { c.BaseURL = "http://backend.invalid/base" }},
		{id: "query", mutate: func(c *ClientConfig) { c.BaseURL = "http://backend.invalid/?x=1" }},
		{id: "fragment", mutate: func(c *ClientConfig) { c.BaseURL = "http://backend.invalid/#x" }},
		{id: "token-empty", mutate: func(c *ClientConfig) { c.BearerToken = "" }},
		{id: "token-newline", mutate: func(c *ClientConfig) { c.BearerToken = "x\ny" }},
		{id: "response-zero", mutate: func(c *ClientConfig) { c.MaxResponseBytes = 0 }},
		{id: "retry-zero", mutate: func(c *ClientConfig) { c.MaxRetryAfter = 0 }},
	}
	for _, test := range tests {
		t.Run(test.id, func(t *testing.T) {
			cfg := base
			test.mutate(&cfg)
			_, err := NewClient(cfg)
			requireCategory(t, err, CategoryInvalidRequest)
		})
	}
	if _, err := NewClient(base); err != nil {
		t.Fatalf("valid config: %v", err)
	}
	base.MaxResponseBytes = math.MaxInt64
	if _, err := NewClient(base); err != nil {
		t.Fatalf("maximum explicit response bound: %v", err)
	}
}

func TestClientDoesNotForwardCookieJarCredentials(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if cookie := r.Header.Get("Cookie"); cookie != "" {
			t.Errorf("cookie credential was forwarded: %q", cookie)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"results":[%s],"took_us":0}`, unfilteredResult("catalog-v1", testStamp, `[]`))
	}))
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	request, _ := http.NewRequest(http.MethodGet, server.URL, nil)
	jar.SetCookies(request.URL, []*http.Cookie{{Name: "session", Value: "secret"}})
	baseClient := &http.Client{Jar: jar}
	_, err := testClient(t, server.URL, baseClient, 0).Batch(context.Background(), []Probe{mustProbe(t, validProbeSpec())})
	if err != nil {
		t.Fatal(err)
	}
}

func TestProbeAndBatchBounds(t *testing.T) {
	max := validProbeSpec()
	max.Query = strings.Repeat("😀", maxQueryRunes)
	max.List = "a" + strings.Repeat("z", 63)
	max.Threshold = floatPtr(1)
	max.TopK = intPtr(maxTopK)
	if _, err := NewProbe(max); err != nil {
		t.Fatalf("maximum probe: %v", err)
	}

	invalid := []struct {
		id     string
		mutate func(*ProbeSpec)
	}{
		{id: "query-empty", mutate: func(s *ProbeSpec) { s.Query = "" }},
		{id: "query-long", mutate: func(s *ProbeSpec) { s.Query = strings.Repeat("q", maxQueryRunes+1) }},
		{id: "query-utf8", mutate: func(s *ProbeSpec) { s.Query = string([]byte{0xff}) }},
		{id: "list-uppercase", mutate: func(s *ProbeSpec) { s.List = "Catalog" }},
		{id: "list-leading", mutate: func(s *ProbeSpec) { s.List = "-catalog" }},
		{id: "list-long", mutate: func(s *ProbeSpec) { s.List = "a" + strings.Repeat("z", 64) }},
		{id: "digest-short", mutate: func(s *ProbeSpec) { s.ExpectedConfigSHA256 = testConfigHash[:63] }},
		{id: "digest-uppercase", mutate: func(s *ProbeSpec) { s.ExpectedConfigSHA256 = strings.ToUpper(testConfigHash) }},
		{id: "threshold-negative", mutate: func(s *ProbeSpec) { s.Threshold = floatPtr(-0.1) }},
		{id: "threshold-large", mutate: func(s *ProbeSpec) { s.Threshold = floatPtr(1.1) }},
		{id: "threshold-nan", mutate: func(s *ProbeSpec) { s.Threshold = floatPtr(math.NaN()) }},
		{id: "threshold-inf", mutate: func(s *ProbeSpec) { s.Threshold = floatPtr(math.Inf(1)) }},
		{id: "topk-zero", mutate: func(s *ProbeSpec) { s.TopK = intPtr(0) }},
		{id: "topk-large", mutate: func(s *ProbeSpec) { s.TopK = intPtr(maxTopK + 1) }},
	}
	for _, test := range invalid {
		t.Run(test.id, func(t *testing.T) {
			spec := validProbeSpec()
			test.mutate(&spec)
			_, err := NewProbe(spec)
			requireCategory(t, err, CategoryInvalidRequest)
		})
	}

	var calls atomic.Int64
	client := testClient(t, "http://backend.invalid", &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, errors.New("must not send")
	})}, 0)
	probe := mustProbe(t, validProbeSpec())
	for _, probes := range [][]Probe{nil, make([]Probe, maxBatchProbes+1), {{}}} {
		_, err := client.Batch(context.Background(), probes)
		requireCategory(t, err, CategoryInvalidRequest)
	}
	if _, err := client.Batch(nil, []Probe{probe}); err == nil {
		t.Fatal("nil context accepted")
	}
	var zero Client
	if _, err := zero.Batch(context.Background(), []Probe{probe}); err == nil {
		t.Fatal("zero client accepted")
	}
	if calls.Load() != 0 {
		t.Fatalf("invalid requests sent %d times", calls.Load())
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func TestTopLevelStatusAndRetryClassification(t *testing.T) {
	tests := []struct {
		id         string
		status     int
		retryAfter string
		category   Category
		retryable  bool
		duration   time.Duration
	}{
		{id: "unauthorized", status: 401, category: CategoryBackendUnauthorized},
		{id: "forbidden", status: 403, category: CategoryBackendForbidden},
		{id: "not-found", status: 404, category: CategoryBackendNotFound},
		{id: "conflict", status: 409, category: CategoryBackendConflict},
		{id: "other", status: 418, category: CategoryBackendHTTP},
		{id: "rate-bare", status: 429, category: CategoryBackendRateLimited},
		{id: "rate-hinted", status: 429, retryAfter: "5", category: CategoryBackendRateLimited, retryable: true, duration: 5 * time.Second},
		{id: "overload-bare", status: 503, category: CategoryBackendOverloaded},
		{id: "overload-hinted", status: 503, retryAfter: "1", category: CategoryBackendOverloaded, retryable: true, duration: time.Second},
		{id: "zero", status: 503, retryAfter: "0", category: CategoryBackendOverloaded},
		{id: "over-bound", status: 503, retryAfter: "6", category: CategoryBackendOverloaded},
		{id: "plus", status: 503, retryAfter: "+1", category: CategoryBackendOverloaded},
		{id: "date", status: 503, retryAfter: "Sun, 06 Nov 1994 08:49:37 GMT", category: CategoryBackendOverloaded},
	}
	probe := mustProbe(t, validProbeSpec())
	for _, test := range tests {
		t.Run(test.id, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if test.retryAfter != "" {
					w.Header().Set("Retry-After", test.retryAfter)
				}
				w.WriteHeader(test.status)
				fmt.Fprint(w, `{"error":"opaque","additive":true}`)
			}))
			defer server.Close()
			_, err := testClient(t, server.URL, nil, 0).Batch(context.Background(), []Probe{probe})
			got := requireCategory(t, err, test.category)
			if got.StatusCode != test.status || got.Retryable != test.retryable || got.RetryAfter != test.duration {
				t.Fatalf("classification = %#v", got)
			}
		})
	}
}

func TestRetryAfterLexicalBounds(t *testing.T) {
	for _, value := range []string{"", "0", "+1", "-1", " 1", "1 ", "1.0", "6"} {
		if _, ok := parseRetryAfter(value, 5*time.Second); ok {
			t.Fatalf("Retry-After %q accepted", value)
		}
	}
	if got, ok := parseRetryAfter("5", 5*time.Second); !ok || got != 5*time.Second {
		t.Fatalf("bounded hint = %v, %v", got, ok)
	}
}

func TestDuplicateRetryAfterIsNotRetryable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Add("Retry-After", "1")
		w.Header().Add("Retry-After", "2")
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprint(w, `{"error":"opaque"}`)
	}))
	defer server.Close()
	_, err := testClient(t, server.URL, nil, 0).Batch(context.Background(), []Probe{mustProbe(t, validProbeSpec())})
	got := requireCategory(t, err, CategoryBackendOverloaded)
	if got.Retryable || got.RetryAfter != 0 {
		t.Fatalf("duplicate retry hint was trusted: %#v", got)
	}
}

func TestRedirectIsNeverFollowed(t *testing.T) {
	var targetCalls atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { targetCalls.Add(1) }))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Redirect(w, &http.Request{}, target.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()

	_, err := testClient(t, source.URL, nil, 0).Batch(context.Background(), []Probe{mustProbe(t, validProbeSpec())})
	got := requireCategory(t, err, CategoryBackendRedirect)
	if got.StatusCode != http.StatusTemporaryRedirect || targetCalls.Load() != 0 {
		t.Fatalf("redirect classification = %#v, target calls = %d", got, targetCalls.Load())
	}
}

func TestCancellationDeadlineAndTransportErrorsAreOpaque(t *testing.T) {
	probe := mustProbe(t, validProbeSpec())
	var calls atomic.Int64
	client := testClient(t, "http://secret-host.invalid", &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, errors.New("raw-secret-transport")
	})}, 0)

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := client.Batch(canceled, []Probe{probe})
	requireCategory(t, err, CategoryCanceled)
	if !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation identity lost")
	}
	deadline, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer stop()
	_, err = client.Batch(deadline, []Probe{probe})
	requireCategory(t, err, CategoryDeadlineExceeded)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("deadline identity lost")
	}
	if calls.Load() != 0 {
		t.Fatalf("pre-canceled requests reached transport %d times", calls.Load())
	}

	_, err = client.Batch(context.Background(), []Probe{probe})
	requireCategory(t, err, CategoryTransportFailure)
	for _, secret := range []string{"raw-secret-transport", "secret-host", "needle", "catalog-v1", "test-token"} {
		if strings.Contains(fmt.Sprintf("%v", err), secret) {
			t.Fatalf("error leaked %q: %v", secret, err)
		}
	}
}

type faultBody struct{ sent bool }

func (body *faultBody) Read(p []byte) (int, error) {
	if body.sent {
		return 0, io.ErrUnexpectedEOF
	}
	body.sent = true
	return copy(p, `{"results":[`), nil
}
func (*faultBody) Close() error { return nil }

func TestResponseLimitContentTypeShortReadAndErrorEnvelope(t *testing.T) {
	probe := mustProbe(t, validProbeSpec())
	tests := []struct {
		id       string
		client   *http.Client
		body     string
		ctype    string
		status   int
		maxBytes int64
		category Category
	}{
		{id: "too-large", body: `{"error":"123456789"}`, ctype: "application/json", status: 500, maxBytes: 4, category: CategoryResponseTooLarge},
		{id: "wrong-content-type", body: `{"error":"x"}`, ctype: "text/plain", status: 500, category: CategoryBackendProtocol},
		{id: "malformed-error", body: `{"message":"x"}`, ctype: "application/json", status: 500, category: CategoryBackendProtocol},
		{id: "duplicate-error", body: `{"error":"x","error":"y"}`, ctype: "application/json", status: 500, category: CategoryBackendProtocol},
		{id: "trailing-error", body: `{"error":"x"} {}`, ctype: "application/json", status: 500, category: CategoryBackendProtocol},
		{id: "duplicate-content-type", client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			body := batchEnvelope(`[` + unfilteredResult("catalog-v1", testStamp, `[]`) + `]`)
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json", "application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
		})}, category: CategoryBackendProtocol},
		{id: "short-read", client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: &faultBody{}}, nil
		})}, category: CategoryTransportFailure},
	}
	for _, test := range tests {
		t.Run(test.id, func(t *testing.T) {
			client := test.client
			base := "http://backend.invalid"
			var server *httptest.Server
			if client == nil {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", test.ctype)
					w.WriteHeader(test.status)
					fmt.Fprint(w, test.body)
				}))
				defer server.Close()
				base = server.URL
			}
			_, err := testClient(t, base, client, test.maxBytes).Batch(context.Background(), []Probe{probe})
			requireCategory(t, err, test.category)
		})
	}
}
