package kurnhttp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// These tests exercise the AdmissionPreparedFull evidence gate through an
// injected http.RoundTripper (and httptest where real header framing is the
// subject). No kurnd or other process is involved.

const admEntries = 2 // testStamp is testBaseHash@2+j0+c<testConfigHash>

var admVerifiedBases = `{"catalog-v1":"verified-base-id"}`

func admFullHeader() http.Header {
	return http.Header{
		"Content-Type":               []string{"application/json"},
		"Kurn-Artifact-Verification": []string{"full"},
	}
}

func admHeader(values ...string) http.Header {
	header := http.Header{"Content-Type": []string{"application/json"}}
	if values != nil {
		header["Kurn-Artifact-Verification"] = values
	}
	return header
}

// admResult is one successful probe result. bases is the raw version_bases
// member value; the empty string omits the member entirely.
func admResult(stamp, candidates, bases string) string {
	result := fmt.Sprintf(`{"candidates":%s,"versions":{"catalog-v1":%q},"took_us":2`, candidates, stamp)
	if bases != "" {
		result += `,"version_bases":` + bases
	}
	return result + `}`
}

func admBody(results ...string) string {
	return batchEnvelope(`[` + strings.Join(results, ",") + `]`)
}

func admGood() string { return admBody(admResult(testStamp, `[]`, admVerifiedBases)) }

const admCandidate = `{"list":"catalog-v1","entry_id":"e1","score":100,"key":"needle","payload":{"id":"e1"}}`

type admReply struct {
	status int
	header http.Header
	body   string
}

func admTransport(calls *atomic.Int64, reply func(*http.Request) (admReply, error)) *http.Client {
	return &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		r, err := reply(request)
		if err != nil {
			return nil, err
		}
		status := r.status
		if status == 0 {
			status = http.StatusOK
		}
		return &http.Response{
			StatusCode: status, Header: r.header.Clone(), Request: request,
			Body: io.NopCloser(strings.NewReader(r.body)),
		}, nil
	})}
}

func admFixed(calls *atomic.Int64, status int, header http.Header, body string) *http.Client {
	return admTransport(calls, func(*http.Request) (admReply, error) {
		return admReply{status: status, header: header, body: body}, nil
	})
}

func admPreparedConfig(base string, client *http.Client) ClientConfig {
	return ClientConfig{
		BaseURL: base, BearerToken: "test-token", HTTPClient: client,
		MaxResponseBytes: 1 << 20, MaxRetryAfter: 5 * time.Second,
		Admission: AdmissionPreparedFull, ExpectedBaseSHA256: testBaseHash, ExpectedEntries: admEntries,
	}
}

func admPrepared(t *testing.T, client *http.Client) *Client {
	t.Helper()
	got, err := NewClient(admPreparedConfig("http://backend.invalid", client))
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func admBatch(t *testing.T, client *Client, probes ...Probe) (BatchResult, error) {
	t.Helper()
	if len(probes) == 0 {
		probes = []Probe{mustProbe(t, validProbeSpec())}
	}
	return client.Batch(context.Background(), probes)
}

func requireVerificationRefusal(t *testing.T, err error) {
	t.Helper()
	got := requireCategory(t, err, CategoryArtifactVerificationMismatch)
	if got.StatusCode != http.StatusOK || got.Retryable || got.RetryAfter != 0 || errors.Unwrap(got) != nil {
		t.Fatalf("verification refusal shape = %#v", got)
	}
}

func TestAdmissionPolicyValidation(t *testing.T) {
	legacy := ClientConfig{BaseURL: "http://backend.invalid", BearerToken: "token", MaxResponseBytes: 1, MaxRetryAfter: time.Second}
	prepared := legacy
	prepared.Admission, prepared.ExpectedBaseSHA256, prepared.ExpectedEntries = AdmissionPreparedFull, testBaseHash, 1

	for name, cfg := range map[string]ClientConfig{
		"legacy-zero":  legacy,
		"prepared-min": prepared,
		"prepared-max": func() ClientConfig { c := prepared; c.ExpectedEntries = 100_000; return c }(),
		"prepared-digits": func() ClientConfig {
			c := prepared
			c.ExpectedBaseSHA256 = strings.Repeat("0123456789abcdef", 4)
			return c
		}(),
		"legacy-explicit-0": func() ClientConfig { c := legacy; c.Admission = AdmissionLegacy; return c }(),
	} {
		t.Run("accept/"+name, func(t *testing.T) {
			if _, err := NewClient(cfg); err != nil {
				t.Fatalf("valid policy refused: %v", err)
			}
		})
	}
	if AdmissionLegacy != 0 || AdmissionPreparedFull != 1 {
		t.Fatal("frozen policy enum values changed")
	}
	var zero ClientConfig
	if zero.Admission != AdmissionLegacy {
		t.Fatal("zero ClientConfig is not legacy")
	}

	refusals := map[string]func(*ClientConfig){
		"unknown-enum-2":        func(c *ClientConfig) { *c = prepared; c.Admission = 2 },
		"unknown-enum-255":      func(c *ClientConfig) { *c = legacy; c.Admission = 255 },
		"legacy-with-base":      func(c *ClientConfig) { *c = legacy; c.ExpectedBaseSHA256 = testBaseHash },
		"legacy-with-entries":   func(c *ClientConfig) { *c = legacy; c.ExpectedEntries = 1 },
		"legacy-with-both":      func(c *ClientConfig) { *c = prepared; c.Admission = AdmissionLegacy },
		"prepared-empty-base":   func(c *ClientConfig) { *c = prepared; c.ExpectedBaseSHA256 = "" },
		"prepared-short-base":   func(c *ClientConfig) { *c = prepared; c.ExpectedBaseSHA256 = testBaseHash[:63] },
		"prepared-long-base":    func(c *ClientConfig) { *c = prepared; c.ExpectedBaseSHA256 = testBaseHash + "a" },
		"prepared-upper-base":   func(c *ClientConfig) { *c = prepared; c.ExpectedBaseSHA256 = strings.ToUpper(testBaseHash) },
		"prepared-nonhex-base":  func(c *ClientConfig) { *c = prepared; c.ExpectedBaseSHA256 = strings.Repeat("g", 64) },
		"prepared-spaced-base":  func(c *ClientConfig) { *c = prepared; c.ExpectedBaseSHA256 = " " + testBaseHash[1:] },
		"prepared-zero-entries": func(c *ClientConfig) { *c = prepared; c.ExpectedEntries = 0 },
		"prepared-over-entries": func(c *ClientConfig) { *c = prepared; c.ExpectedEntries = 100_001 },
		"prepared-huge-entries": func(c *ClientConfig) { *c = prepared; c.ExpectedEntries = ^uint64(0) },
	}
	for name, mutate := range refusals {
		t.Run("refuse/"+name, func(t *testing.T) {
			var cfg ClientConfig
			mutate(&cfg)
			got, err := NewClient(cfg)
			requireCategory(t, err, CategoryInvalidRequest)
			if got != nil {
				t.Fatal("refused policy returned a client")
			}
		})
	}
}

func TestPreparedFullAdmitsFullVerifiedEvidence(t *testing.T) {
	var calls atomic.Int64
	body := admBody(admResult(testStamp, `[`+admCandidate+`]`, admVerifiedBases))
	result, err := admBatch(t, admPrepared(t, admFixed(&calls, 200, admFullHeader(), body)))
	if err != nil {
		t.Fatalf("full verified evidence refused: %v", err)
	}
	results := result.Results()
	if calls.Load() != 1 || len(results) != 1 || results[0].Stamp().Raw != testStamp ||
		results[0].Stamp().BaseSHA256 != testBaseHash || results[0].Stamp().Entries != admEntries ||
		len(results[0].Candidates()) != 1 || results[0].Candidates()[0].EntryID() != "e1" {
		t.Fatalf("admitted result shape is wrong: calls=%d", calls.Load())
	}

	// Real header framing over loopback, same evidence.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Kurn-Artifact-Verification", "full")
		fmt.Fprint(w, body)
	}))
	defer server.Close()
	client, err := NewClient(admPreparedConfig(server.URL, nil))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admBatch(t, client); err != nil {
		t.Fatalf("loopback full evidence refused: %v", err)
	}
}

func TestPreparedFullEmptyCandidatesNeedFullEvidence(t *testing.T) {
	var calls atomic.Int64
	result, err := admBatch(t, admPrepared(t, admFixed(&calls, 200, admFullHeader(), admGood())))
	if err != nil {
		t.Fatalf("empty result with full evidence refused: %v", err)
	}
	if candidates := result.Results()[0].Candidates(); candidates == nil || len(candidates) != 0 {
		t.Fatal("empty admitted result changed shape")
	}
	for name, reply := range map[string]admReply{
		"no-header":           {header: admHeader(), body: admGood()},
		"no-version-bases":    {header: admFullHeader(), body: admBody(admResult(testStamp, `[]`, ""))},
		"no-evidence-at-all":  {header: admHeader(), body: admBody(admResult(testStamp, `[]`, ""))},
		"declared-empty":      {header: admFullHeader(), body: admBody(admResult(testStamp, `[]`, `{"catalog-v1":"declared-base-id"}`))},
		"trusted-empty":       {header: admHeader("trusted"), body: admGood()},
		"wrong-entries-empty": {header: admFullHeader(), body: admBody(admResult(testBaseHash+"@3+j0+c"+testConfigHash, `[]`, admVerifiedBases))},
	} {
		t.Run(name, func(t *testing.T) {
			var calls atomic.Int64
			_, err := admBatch(t, admPrepared(t, admFixed(&calls, 200, reply.header, reply.body)))
			requireVerificationRefusal(t, err)
			if calls.Load() != 1 {
				t.Fatalf("calls = %d", calls.Load())
			}
		})
	}
}

func TestPreparedFullHeaderRefusals(t *testing.T) {
	for name, values := range map[string][]string{
		"absent":             nil,
		"empty":              {""},
		"duplicate-full":     {"full", "full"},
		"conflicting":        {"full", "trusted"},
		"conflicting-order":  {"trusted", "full"},
		"trusted":            {"trusted"},
		"unknown":            {"partial"},
		"case":               {"Full"},
		"padded":             {"full "},
		"comma-list":         {"full, trusted"},
		"comma-list-same":    {"full,full"},
		"quoted":             {`"full"`},
		"trailing-semicolon": {"full;"},
	} {
		t.Run(name, func(t *testing.T) {
			var calls atomic.Int64
			_, err := admBatch(t, admPrepared(t, admFixed(&calls, 200, admHeader(values...), admGood())))
			requireVerificationRefusal(t, err)
		})
	}

	// The same duplicate/conflict over real HTTP header framing.
	for name, values := range map[string][]string{
		"loopback-duplicate":   {"full", "full"},
		"loopback-conflicting": {"full", "trusted"},
		"loopback-trusted":     {"trusted"},
		"loopback-absent":      nil,
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				for _, value := range values {
					w.Header().Add("Kurn-Artifact-Verification", value)
				}
				fmt.Fprint(w, admGood())
			}))
			defer server.Close()
			client, err := NewClient(admPreparedConfig(server.URL, nil))
			if err != nil {
				t.Fatal(err)
			}
			_, err = admBatch(t, client)
			requireVerificationRefusal(t, err)
		})
	}
}

func TestPreparedFullVersionBasesRefusals(t *testing.T) {
	for name, bases := range map[string]string{
		"absent":         "",
		"null":           `null`,
		"empty-object":   `{}`,
		"array":          `[]`,
		"string":         `"verified-base-id"`,
		"extra-key":      `{"catalog-v1":"verified-base-id","other-v1":"verified-base-id"}`,
		"duplicate-key":  `{"catalog-v1":"verified-base-id","catalog-v1":"verified-base-id"}`,
		"duplicate-diff": `{"catalog-v1":"verified-base-id","catalog-v1":"declared-base-id"}`,
		"wrong-list":     `{"other-v1":"verified-base-id"}`,
		"list-case":      `{"Catalog-v1":"verified-base-id"}`,
		"declared":       `{"catalog-v1":"declared-base-id"}`,
		"unknown-basis":  `{"catalog-v1":"verified"}`,
		"basis-case":     `{"catalog-v1":"Verified-Base-Id"}`,
		"basis-null":     `{"catalog-v1":null}`,
		"basis-number":   `{"catalog-v1":1}`,
		"basis-object":   `{"catalog-v1":{"basis":"verified-base-id"}}`,
		"basis-array":    `{"catalog-v1":["verified-base-id"]}`,
	} {
		t.Run(name, func(t *testing.T) {
			body := admBody(admResult(testStamp, `[]`, bases))
			var calls atomic.Int64
			_, err := admBatch(t, admPrepared(t, admFixed(&calls, 200, admFullHeader(), body)))
			requireVerificationRefusal(t, err)
		})
	}

	// A duplicated version_bases MEMBER (not key) is caught by the existing
	// closed result-object decoder first: still refused, existing category.
	duplicateMember := strings.Replace(admResult(testStamp, `[]`, admVerifiedBases),
		`"version_bases":`, `"version_bases":`+admVerifiedBases+`,"version_bases":`, 1)
	var calls atomic.Int64
	_, err := admBatch(t, admPrepared(t, admFixed(&calls, 200, admFullHeader(), admBody(duplicateMember))))
	requireCategory(t, err, CategoryBackendProtocol)

	// Malformed JSON inside version_bases makes the whole body invalid JSON:
	// the existing closed body decoder refuses it, existing category.
	malformed := strings.Replace(admResult(testStamp, `[]`, admVerifiedBases), admVerifiedBases, `{"catalog-v1":"verified-base-id"x}`, 1)
	_, err = admBatch(t, admPrepared(t, admFixed(&calls, 200, admFullHeader(), admBody(malformed))))
	requireCategory(t, err, CategoryBackendProtocol)
}

func TestPreparedFullStampIdentityRefusals(t *testing.T) {
	otherConfig := strings.Repeat("e", 64)
	for name, test := range map[string]struct {
		stamp    string
		category Category
	}{
		"wrong-base":       {strings.Repeat("d", 64) + "@2+j0+c" + testConfigHash, CategoryArtifactVerificationMismatch},
		"wrong-entries":    {testBaseHash + "@3+j0+c" + testConfigHash, CategoryArtifactVerificationMismatch},
		"zero-entries":     {testBaseHash + "@0+j0+c" + testConfigHash, CategoryArtifactVerificationMismatch},
		"journal":          {testBaseHash + "@2+j1." + testJournalHash + "+c" + testConfigHash, CategoryArtifactVerificationMismatch},
		"journal-large":    {testBaseHash + "@2+j4096." + testJournalHash + "+c" + testConfigHash, CategoryArtifactVerificationMismatch},
		"wrong-config":     {testBaseHash + "@2+j0+c" + otherConfig, CategoryListIdentityMismatch},
		"malformed-stamp":  {"not-a-stamp", CategoryListIdentityMismatch},
		"uppercase-base":   {strings.ToUpper(testBaseHash) + "@2+j0+c" + testConfigHash, CategoryListIdentityMismatch},
		"config-and-base":  {strings.Repeat("d", 64) + "@2+j0+c" + otherConfig, CategoryListIdentityMismatch},
		"leading-zero-ent": {testBaseHash + "@02+j0+c" + testConfigHash, CategoryArtifactVerificationMismatch},
	} {
		t.Run(name, func(t *testing.T) {
			var calls atomic.Int64
			body := admBody(admResult(test.stamp, `[]`, admVerifiedBases))
			_, err := admBatch(t, admPrepared(t, admFixed(&calls, 200, admFullHeader(), body)))
			if test.category == CategoryArtifactVerificationMismatch {
				requireVerificationRefusal(t, err)
			} else {
				requireCategory(t, err, test.category)
			}
		})
	}

	// The existing config check precedes the added version_bases check: a
	// config mismatch with no version_bases stays a list identity mismatch.
	var calls atomic.Int64
	body := admBody(admResult(testBaseHash+"@2+j0+c"+otherConfig, `[]`, ""))
	_, err := admBatch(t, admPrepared(t, admFixed(&calls, 200, admFullHeader(), body)))
	requireCategory(t, err, CategoryListIdentityMismatch)

	// The existing closed versions map is unchanged under the policy.
	body = admBody(fmt.Sprintf(`{"candidates":[],"versions":{"catalog-v1":%q,"x":%q},"version_bases":%s,"took_us":2}`, testStamp, testStamp, admVerifiedBases))
	_, err = admBatch(t, admPrepared(t, admFixed(&calls, 200, admFullHeader(), body)))
	requireCategory(t, err, CategoryListIdentityMismatch)
}

func TestPreparedFullKeepsCandidateAndEchoChecks(t *testing.T) {
	filter := mustFilter(t, FilterTerm{Name: "factory", Value: "north"})
	filtered := validProbeSpec()
	filtered.Filter = filter
	filteredProbe := mustProbe(t, filtered)
	echoed := func(echo string) string {
		return admBody(fmt.Sprintf(`{"candidates":[],"versions":{"catalog-v1":%q},"version_bases":%s,"filter":%s,"filter_stats":{"catalog-v1":{"evaluated":1,"rejected":1}},"took_us":2}`,
			testStamp, admVerifiedBases, echo))
	}

	var calls atomic.Int64
	if _, err := admBatch(t, admPrepared(t, admFixed(&calls, 200, admFullHeader(), echoed(`{"factory":"north"}`))), filteredProbe); err != nil {
		t.Fatalf("filtered full evidence refused: %v", err)
	}

	for name, test := range map[string]struct {
		body     string
		probe    *Probe
		category Category
	}{
		"candidate-wrong-list":  {admBody(admResult(testStamp, `[{"list":"other","entry_id":"e1","score":1,"key":"k"}]`, admVerifiedBases)), nil, CategoryListIdentityMismatch},
		"candidate-no-id":       {admBody(admResult(testStamp, `[{"list":"catalog-v1","score":1,"key":"k"}]`, admVerifiedBases)), nil, CategoryBackendProtocol},
		"candidate-duplicate":   {admBody(admResult(testStamp, `[`+admCandidate+`,`+admCandidate+`]`, admVerifiedBases)), nil, CategoryBackendProtocol},
		"candidates-missing":    {admBody(fmt.Sprintf(`{"versions":{"catalog-v1":%q},"version_bases":%s,"took_us":2}`, testStamp, admVerifiedBases)), nil, CategoryBackendProtocol},
		"unfiltered-with-echo":  {admBody(fmt.Sprintf(`{"candidates":[],"versions":{"catalog-v1":%q},"version_bases":%s,"filter":{},"took_us":2}`, testStamp, admVerifiedBases)), nil, CategoryFilterMismatch},
		"filtered-wrong-echo":   {echoed(`{"factory":"south"}`), &filteredProbe, CategoryFilterMismatch},
		"filtered-missing-echo": {admBody(admResult(testStamp, `[]`, admVerifiedBases)), &filteredProbe, CategoryFilterMismatch},
		// Verification evidence precedes candidate and echo checks.
		"bad-candidate-and-no-bases": {admBody(admResult(testStamp, `[{"list":"other","entry_id":"e1","score":1,"key":"k"}]`, "")), nil, CategoryArtifactVerificationMismatch},
		"bad-echo-and-declared":      {admBody(fmt.Sprintf(`{"candidates":[],"versions":{"catalog-v1":%q},"version_bases":{"catalog-v1":"declared-base-id"},"filter":{"factory":"south"},"filter_stats":{"catalog-v1":{"evaluated":1,"rejected":1}},"took_us":2}`, testStamp)), &filteredProbe, CategoryArtifactVerificationMismatch},
	} {
		t.Run(name, func(t *testing.T) {
			probes := []Probe{mustProbe(t, validProbeSpec())}
			if test.probe != nil {
				probes = []Probe{*test.probe}
			}
			var calls atomic.Int64
			_, err := admBatch(t, admPrepared(t, admFixed(&calls, 200, admFullHeader(), test.body)), probes...)
			requireCategory(t, err, test.category)
		})
	}
}

type admCanceledBody struct{ cancel context.CancelFunc }

func (b admCanceledBody) Read([]byte) (int, error) {
	b.cancel()
	return 0, context.Canceled
}
func (admCanceledBody) Close() error { return nil }

func TestPreparedFullPrecedence(t *testing.T) {
	trustedHeader := func(ctype string) http.Header {
		return http.Header{"Content-Type": []string{ctype}, "Kurn-Artifact-Verification": []string{"trusted"}}
	}
	tests := []struct {
		name     string
		reply    admReply
		transErr error
		maxBytes int64
		category Category
		status   int
	}{
		// Non-200 needs no verification evidence; a trusted header changes nothing.
		{name: "non200-plain", reply: admReply{status: 500, header: admHeader(), body: `{"error":"x"}`}, category: CategoryBackendHTTP, status: 500},
		{name: "non200-trusted", reply: admReply{status: 500, header: trustedHeader("application/json"), body: `{"error":"x"}`}, category: CategoryBackendHTTP, status: 500},
		{name: "unauthorized-trusted", reply: admReply{status: 401, header: trustedHeader("application/json"), body: `{"error":"x"}`}, category: CategoryBackendUnauthorized, status: 401},
		{name: "overloaded-no-header", reply: admReply{status: 503, header: admHeader(), body: `{"error":"x"}`}, category: CategoryBackendOverloaded, status: 503},
		{name: "non200-malformed-envelope", reply: admReply{status: 500, header: trustedHeader("application/json"), body: `{"message":"x"}`}, category: CategoryBackendProtocol, status: 500},
		{name: "redirect-trusted", reply: admReply{status: 307, header: trustedHeader("application/json"), body: ``}, category: CategoryBackendRedirect, status: 307},
		{name: "redirect-full", reply: admReply{status: 302, header: admFullHeader(), body: admGood()}, category: CategoryBackendRedirect, status: 302},
		{name: "too-large-trusted", reply: admReply{header: trustedHeader("application/json"), body: admGood()}, maxBytes: 8, category: CategoryResponseTooLarge, status: 200},
		{name: "content-type-trusted", reply: admReply{header: trustedHeader("text/plain"), body: admGood()}, category: CategoryBackendProtocol, status: 200},
		{name: "duplicate-content-type-no-header", reply: admReply{header: http.Header{"Content-Type": []string{"application/json", "application/json"}}, body: admGood()}, category: CategoryBackendProtocol, status: 200},
		{name: "transport-error", transErr: errors.New("raw transport"), category: CategoryTransportFailure},
		// On a 200 the header gate precedes ANY body decoding.
		{name: "200-no-header-malformed-body", reply: admReply{header: admHeader(), body: `not json`}, category: CategoryArtifactVerificationMismatch, status: 200},
		{name: "200-no-header-inline-error", reply: admReply{header: admHeader(), body: admBody(`{"error":"x"}`)}, category: CategoryArtifactVerificationMismatch, status: 200},
		{name: "200-full-malformed-body", reply: admReply{header: admFullHeader(), body: `not json`}, category: CategoryBackendProtocol, status: 200},
		// Inline-error precedence is unchanged once the header is admitted.
		{name: "200-full-inline-error", reply: admReply{header: admFullHeader(), body: admBody(`{"error":"x"}`)}, category: CategoryBackendInline, status: 200},
		{name: "200-full-inline-with-bases", reply: admReply{header: admFullHeader(), body: admBody(`{"error":"x","version_bases":{"catalog-v1":"declared-base-id"}}`)}, category: CategoryBackendInline, status: 200},
		{name: "200-full-inline-mixed", reply: admReply{header: admFullHeader(), body: admBody(`{"error":"x","versions":{}}`)}, category: CategoryBackendProtocol, status: 200},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int64
			client := admTransport(&calls, func(*http.Request) (admReply, error) { return test.reply, test.transErr })
			cfg := admPreparedConfig("http://backend.invalid", client)
			if test.maxBytes != 0 {
				cfg.MaxResponseBytes = test.maxBytes
			}
			prepared, err := NewClient(cfg)
			if err != nil {
				t.Fatal(err)
			}
			_, err = admBatch(t, prepared)
			got := requireCategory(t, err, test.category)
			if got.StatusCode != test.status || calls.Load() != 1 {
				t.Fatalf("status = %d calls = %d", got.StatusCode, calls.Load())
			}
		})
	}

	t.Run("pre-canceled", func(t *testing.T) {
		var calls atomic.Int64
		prepared := admPrepared(t, admFixed(&calls, 200, admHeader(), admGood()))
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := prepared.Batch(ctx, []Probe{mustProbe(t, validProbeSpec())})
		requireCategory(t, err, CategoryCanceled)
		if !errors.Is(err, context.Canceled) || calls.Load() != 0 {
			t.Fatalf("pre-canceled: calls=%d", calls.Load())
		}
	})
	t.Run("pre-expired-deadline", func(t *testing.T) {
		var calls atomic.Int64
		prepared := admPrepared(t, admFixed(&calls, 200, admHeader(), admGood()))
		ctx, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		defer stop()
		_, err := prepared.Batch(ctx, []Probe{mustProbe(t, validProbeSpec())})
		requireCategory(t, err, CategoryDeadlineExceeded)
		if !errors.Is(err, context.DeadlineExceeded) || calls.Load() != 0 {
			t.Fatalf("pre-expired: calls=%d", calls.Load())
		}
	})
	t.Run("canceled-in-flight", func(t *testing.T) {
		var calls atomic.Int64
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		client := admTransport(&calls, func(*http.Request) (admReply, error) {
			cancel()
			return admReply{}, context.Canceled
		})
		_, err := admPrepared(t, client).Batch(ctx, []Probe{mustProbe(t, validProbeSpec())})
		requireCategory(t, err, CategoryCanceled)
	})
	t.Run("deadline-in-flight", func(t *testing.T) {
		var calls atomic.Int64
		client := admTransport(&calls, func(*http.Request) (admReply, error) {
			return admReply{}, context.DeadlineExceeded
		})
		_, err := admPrepared(t, client).Batch(context.Background(), []Probe{mustProbe(t, validProbeSpec())})
		requireCategory(t, err, CategoryDeadlineExceeded)
	})
	t.Run("canceled-during-body-no-header", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: admHeader(), Request: request, Body: admCanceledBody{cancel: cancel}}, nil
		})}
		_, err := admPrepared(t, client).Batch(ctx, []Probe{mustProbe(t, validProbeSpec())})
		requireCategory(t, err, CategoryCanceled)
	})
	t.Run("short-read-no-header", func(t *testing.T) {
		client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: admHeader(), Request: request, Body: &faultBody{}}, nil
		})}
		_, err := admPrepared(t, client).Batch(context.Background(), []Probe{mustProbe(t, validProbeSpec())})
		requireCategory(t, err, CategoryTransportFailure)
	})
}

func TestPreparedFullEveryBatchAndNoPartialSuccess(t *testing.T) {
	// One client, sequential Batches: success is never cached and the
	// header is re-required on every call.
	var calls atomic.Int64
	script := []admReply{
		{header: admFullHeader(), body: admGood()},
		{header: admHeader("trusted"), body: admGood()},
		{header: admFullHeader(), body: admBody(admResult(testStamp, `[]`, `{"catalog-v1":"declared-base-id"}`))},
		{header: admFullHeader(), body: admGood()},
	}
	client := admPrepared(t, admTransport(&calls, func(*http.Request) (admReply, error) {
		return script[calls.Load()-1], nil
	}))
	for index, wantOK := range []bool{true, false, false, true} {
		_, err := admBatch(t, client)
		if wantOK && err != nil {
			t.Fatalf("batch %d refused: %v", index+1, err)
		}
		if !wantOK {
			requireVerificationRefusal(t, err)
		}
	}
	if calls.Load() != 4 {
		t.Fatalf("calls = %d", calls.Load())
	}

	// Two probes in one Batch: the first result is fully admitted, the
	// second is not. The whole Batch refuses; nothing partial is returned.
	probe := mustProbe(t, validProbeSpec())
	for name, second := range map[string]string{
		"second-declared":   admResult(testStamp, `[]`, `{"catalog-v1":"declared-base-id"}`),
		"second-no-bases":   admResult(testStamp, `[]`, ""),
		"second-other-base": admResult(strings.Repeat("d", 64)+"@2+j0+c"+testConfigHash, `[]`, admVerifiedBases),
	} {
		t.Run(name, func(t *testing.T) {
			var calls atomic.Int64
			body := admBody(admResult(testStamp, `[`+admCandidate+`]`, admVerifiedBases), second)
			result, err := admBatch(t, admPrepared(t, admFixed(&calls, 200, admFullHeader(), body)), probe, probe)
			requireVerificationRefusal(t, err)
			if len(result.Results()) != 0 || result.TookUS() != 0 {
				t.Fatal("partial batch result returned")
			}
		})
	}
}

func TestLegacyIgnoresPreparedEvidenceUnchanged(t *testing.T) {
	journal := testBaseHash + "@2+j1." + testJournalHash + "+c" + testConfigHash
	plain := admBody(admResult(testStamp, `[`+admCandidate+`]`, ""))
	for name, reply := range map[string]admReply{
		"plain":                     {header: admHeader(), body: plain},
		"prepared-looking-full":     {header: admFullHeader(), body: admBody(admResult(testStamp, `[`+admCandidate+`]`, admVerifiedBases))},
		"prepared-looking-trusted":  {header: admHeader("trusted", "full"), body: admBody(admResult(testStamp, `[`+admCandidate+`]`, `{"catalog-v1":"declared-base-id"}`))},
		"prepared-looking-garbage":  {header: admHeader("unknown"), body: admBody(admResult(testStamp, `[`+admCandidate+`]`, `{"x":1,"y":null}`))},
		"prepared-looking-null":     {header: admHeader(), body: admBody(admResult(testStamp, `[`+admCandidate+`]`, `null`))},
		"prepared-looking-other-id": {header: admHeader(), body: admBody(admResult(strings.Repeat("d", 64)+"@9+j0+c"+testConfigHash, `[`+admCandidate+`]`, ""))},
		"prepared-looking-journal":  {header: admHeader(), body: admBody(admResult(journal, `[`+admCandidate+`]`, ""))},
	} {
		t.Run(name, func(t *testing.T) {
			var calls atomic.Int64
			legacy := testClient(t, "http://backend.invalid", admFixed(&calls, 200, reply.header, reply.body), 0)
			result, err := admBatch(t, legacy)
			if err != nil {
				t.Fatalf("legacy refused a response it always admitted: %v", err)
			}
			got := result.Results()[0]
			if len(got.Candidates()) != 1 || got.Candidates()[0].EntryID() != "e1" || got.TookUS() != 2 || result.TookUS() != 3 {
				t.Fatal("legacy decode changed")
			}
			// The package-level legacy decoder agrees byte-for-byte on shape.
			direct, err := decodeBatch([]byte(reply.body), []Probe{mustProbe(t, validProbeSpec())})
			if err != nil || direct.Results()[0].Stamp() != got.Stamp() {
				t.Fatalf("legacy decodeBatch disagrees: %v", err)
			}
		})
	}

	// An explicit AdmissionLegacy is the same policy as the zero value.
	var calls atomic.Int64
	explicit, err := NewClient(ClientConfig{
		BaseURL: "http://backend.invalid", BearerToken: "test-token",
		HTTPClient:       admFixed(&calls, 200, admHeader("trusted"), admBody(admResult(testStamp, `[]`, `{"catalog-v1":"declared-base-id"}`))),
		MaxResponseBytes: 1 << 20, MaxRetryAfter: 5 * time.Second, Admission: AdmissionLegacy,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admBatch(t, explicit); err != nil {
		t.Fatalf("explicit legacy refused: %v", err)
	}
}

func TestArtifactVerificationCategoryIsDistinctAndOpaque(t *testing.T) {
	if CategoryArtifactVerificationMismatch != "artifact_verification_mismatch" {
		t.Fatal("frozen internal category spelling changed")
	}
	for _, existing := range []Category{
		CategoryInvalidRequest, CategoryTransportFailure, CategoryCanceled, CategoryDeadlineExceeded,
		CategoryResponseTooLarge, CategoryBackendProtocol, CategoryListIdentityMismatch, CategoryFilterMismatch,
		CategoryBackendInline, CategoryBackendRateLimited, CategoryBackendOverloaded, CategoryBackendUnauthorized,
		CategoryBackendForbidden, CategoryBackendNotFound, CategoryBackendConflict, CategoryBackendHTTP, CategoryBackendRedirect,
	} {
		if existing == CategoryArtifactVerificationMismatch {
			t.Fatalf("collides with %q", existing)
		}
	}
	var calls atomic.Int64
	_, err := admBatch(t, admPrepared(t, admFixed(&calls, 200, admHeader("trusted"), admGood())))
	for _, secret := range []string{"trusted", "declared", testBaseHash, "catalog-v1", "needle", "test-token", "backend.invalid"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("refusal leaked %q: %v", secret, err)
		}
	}
}
