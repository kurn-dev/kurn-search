package kurnhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type ClientConfig struct {
	BaseURL          string
	BearerToken      string
	HTTPClient       *http.Client
	MaxResponseBytes int64
	MaxRetryAfter    time.Duration

	// Admission selects the evidence a successful Batch must carry. The zero
	// value, AdmissionLegacy, requires both expectations to be zero.
	Admission          AdmissionPolicy
	ExpectedBaseSHA256 string
	ExpectedEntries    uint64
}

type Client struct {
	endpoint         string
	bearerToken      string
	httpClient       *http.Client
	maxResponseBytes int64
	maxRetryAfter    time.Duration
	admission        admission
	valid            bool
}

func NewClient(cfg ClientConfig) (*Client, error) {
	u, err := url.Parse(cfg.BaseURL)
	if err != nil || u == nil || u.Opaque != "" ||
		(u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" ||
		u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" ||
		(u.Path != "" && u.Path != "/") || cfg.BearerToken == "" ||
		strings.ContainsAny(cfg.BearerToken, "\r\n") || cfg.MaxResponseBytes <= 0 ||
		cfg.MaxRetryAfter <= 0 {
		return nil, classified(CategoryInvalidRequest, 0, nil)
	}
	policy, ok := newAdmission(cfg.Admission, cfg.ExpectedBaseSHA256, cfg.ExpectedEntries)
	if !ok {
		return nil, classified(CategoryInvalidRequest, 0, nil)
	}
	u.Path = "/v1/batch-query"
	base := cfg.HTTPClient
	if base == nil {
		base = &http.Client{}
	}
	hc := *base
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	hc.Jar = nil
	return &Client{endpoint: u.String(), bearerToken: cfg.BearerToken, httpClient: &hc,
		maxResponseBytes: cfg.MaxResponseBytes, maxRetryAfter: cfg.MaxRetryAfter,
		admission: policy, valid: true}, nil
}

func (c *Client) Batch(ctx context.Context, probes []Probe) (BatchResult, error) {
	if c == nil || !c.valid || ctx == nil || len(probes) < 1 || len(probes) > maxBatchProbes {
		return BatchResult{}, classified(CategoryInvalidRequest, 0, nil)
	}
	for i := range probes {
		if !probes[i].valid {
			return BatchResult{}, classified(CategoryInvalidRequest, 0, nil)
		}
	}
	if err := ctx.Err(); err != nil {
		return BatchResult{}, classified(CategoryTransportFailure, 0, err)
	}
	body := marshalBatch(probes)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return BatchResult{}, classified(CategoryInvalidRequest, 0, nil)
	}
	req.Header.Set("Authorization", "Bearer "+c.bearerToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return BatchResult{}, classified(CategoryTransportFailure, 0, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode <= 399 {
		return BatchResult{}, classified(CategoryBackendRedirect, resp.StatusCode, nil)
	}
	raw, readErr := readBounded(resp.Body, c.maxResponseBytes)
	if readErr != nil {
		if errors.Is(readErr, errResponseTooLarge) {
			return BatchResult{}, classified(CategoryResponseTooLarge, resp.StatusCode, nil)
		}
		return BatchResult{}, classified(CategoryTransportFailure, resp.StatusCode, ctx.Err())
	}
	contentTypes := resp.Header.Values("Content-Type")
	if len(contentTypes) != 1 || !isJSONContentType(contentTypes[0]) {
		return BatchResult{}, classified(CategoryBackendProtocol, resp.StatusCode, nil)
	}
	if resp.StatusCode != http.StatusOK {
		if err := validateErrorEnvelope(raw); err != nil {
			return BatchResult{}, classified(CategoryBackendProtocol, resp.StatusCode, nil)
		}
		return BatchResult{}, c.httpError(resp)
	}
	if err := c.admission.admitHeader(resp.Header); err != nil {
		return BatchResult{}, err
	}
	return decodeAdmittedBatch(raw, probes, c.admission)
}

var errResponseTooLarge = errors.New("response too large")

func readBounded(r io.Reader, limit int64) ([]byte, error) {
	reader := r
	if limit < int64(^uint64(0)>>1) {
		reader = io.LimitReader(r, limit+1)
	}
	raw, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > limit {
		return nil, errResponseTooLarge
	}
	return raw, nil
}

func isJSONContentType(value string) bool {
	mediaType, _, err := mime.ParseMediaType(value)
	return err == nil && mediaType == "application/json"
}

func validateErrorEnvelope(raw []byte) error {
	m, err := object(raw)
	if err != nil {
		return err
	}
	var message string
	if err := required(m, "error", &message); err != nil || message == "" {
		return io.ErrUnexpectedEOF
	}
	return nil
}

func (c *Client) httpError(resp *http.Response) *Error {
	category := CategoryBackendHTTP
	switch resp.StatusCode {
	case http.StatusUnauthorized:
		category = CategoryBackendUnauthorized
	case http.StatusForbidden:
		category = CategoryBackendForbidden
	case http.StatusNotFound:
		category = CategoryBackendNotFound
	case http.StatusConflict:
		category = CategoryBackendConflict
	case http.StatusTooManyRequests:
		category = CategoryBackendRateLimited
	case http.StatusServiceUnavailable:
		category = CategoryBackendOverloaded
	}
	e := classified(category, resp.StatusCode, nil)
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable {
		hints := resp.Header.Values("Retry-After")
		if len(hints) == 1 {
			if d, ok := parseRetryAfter(hints[0], c.maxRetryAfter); ok {
				e.Retryable, e.RetryAfter = true, d
			}
		}
	}
	return e
}

func parseRetryAfter(value string, max time.Duration) (time.Duration, bool) {
	if value == "" || strings.TrimSpace(value) != value {
		return 0, false
	}
	for i := 0; i < len(value); i++ {
		if value[i] < '0' || value[i] > '9' {
			return 0, false
		}
	}
	seconds, err := strconv.ParseUint(value, 10, 63)
	if err != nil || seconds == 0 || seconds > uint64(max/time.Second) {
		return 0, false
	}
	return time.Duration(seconds) * time.Second, true
}

func marshalBatch(probes []Probe) []byte {
	buf := []byte(`{"checks":[`)
	for i, probe := range probes {
		if i > 0 {
			buf = append(buf, ',')
		}
		buf = append(buf, '{')
		if len(probe.anyOf) > 0 {
			buf = append(buf, `"exact_any_of":[`...)
			for j, term := range probe.anyOf {
				if j > 0 {
					buf = append(buf, ',')
				}
				buf = appendCanonicalString(buf, term)
			}
			buf = append(buf, ']')
		} else {
			buf = append(buf, `"q":`...)
			buf = appendCanonicalString(buf, probe.query)
		}
		buf = append(buf, `,"lists":[`...)
		buf = appendCanonicalString(buf, probe.list)
		buf = append(buf, ']')
		if probe.hasThreshold {
			buf = append(buf, `,"threshold":`...)
			if probe.threshold == 0 {
				buf = append(buf, '0')
			} else {
				buf = strconv.AppendFloat(buf, probe.threshold, 'g', -1, 64)
			}
		}
		if probe.hasTopK {
			buf = append(buf, `,"topk":`...)
			buf = strconv.AppendInt(buf, int64(probe.topK), 10)
		}
		if probe.filterMode != probeUnfiltered {
			buf = append(buf, `,"filter":`...)
			buf = append(buf, probe.filterJSON...)
		}
		buf = append(buf, '}')
	}
	return append(buf, ']', '}')
}

// decodeBatch decodes under the legacy policy.
func decodeBatch(raw []byte, probes []Probe) (BatchResult, error) {
	return decodeAdmittedBatch(raw, probes, admission{})
}

func decodeAdmittedBatch(raw []byte, probes []Probe, policy admission) (BatchResult, error) {
	m, err := object(raw)
	if err != nil {
		return BatchResult{}, classified(CategoryBackendProtocol, http.StatusOK, nil)
	}
	var took int64
	if err := required(m, "took_us", &took); err != nil || took < 0 {
		return BatchResult{}, classified(CategoryBackendProtocol, http.StatusOK, nil)
	}
	resultsRaw, ok := m["results"]
	if !ok {
		return BatchResult{}, classified(CategoryBackendProtocol, http.StatusOK, nil)
	}
	items, err := array(resultsRaw)
	if err != nil || len(items) != len(probes) {
		return BatchResult{}, classified(CategoryBackendProtocol, http.StatusOK, nil)
	}
	results := make([]ProbeResult, len(items))
	seenStamp := make(map[string]string)
	for i := range items {
		result, err := decodeProbeResult(items[i], probes[i], policy)
		if err != nil {
			return BatchResult{}, err
		}
		if previous, exists := seenStamp[probes[i].list]; exists && previous != result.stamp.Raw {
			return BatchResult{}, classified(CategoryListIdentityMismatch, http.StatusOK, nil)
		}
		seenStamp[probes[i].list] = result.stamp.Raw
		results[i] = result
	}
	return BatchResult{results: results, tookUS: took}, nil
}

func decodeProbeResult(raw []byte, probe Probe, policy admission) (ProbeResult, error) {
	m, err := object(raw)
	if err != nil {
		return ProbeResult{}, classified(CategoryBackendProtocol, http.StatusOK, nil)
	}
	if errorRaw, inline := m["error"]; inline {
		for _, key := range []string{"candidates", "versions", "filter", "filter_stats", "took_us"} {
			if _, mixed := m[key]; mixed {
				return ProbeResult{}, classified(CategoryBackendProtocol, http.StatusOK, nil)
			}
		}
		var message string
		if err := json.Unmarshal(errorRaw, &message); err != nil || message == "" {
			return ProbeResult{}, classified(CategoryBackendProtocol, http.StatusOK, nil)
		}
		return ProbeResult{}, classified(CategoryBackendInline, http.StatusOK, nil)
	}

	var took int64
	if err := required(m, "took_us", &took); err != nil || took < 0 {
		return ProbeResult{}, classified(CategoryBackendProtocol, http.StatusOK, nil)
	}
	versionsRaw, ok := m["versions"]
	if !ok {
		return ProbeResult{}, classified(CategoryBackendProtocol, http.StatusOK, nil)
	}
	versions, err := object(versionsRaw)
	if err != nil || len(versions) != 1 {
		return ProbeResult{}, classified(CategoryListIdentityMismatch, http.StatusOK, nil)
	}
	var stampRaw string
	if err := required(versions, probe.list, &stampRaw); err != nil {
		return ProbeResult{}, classified(CategoryListIdentityMismatch, http.StatusOK, nil)
	}
	stamp, err := ParseStamp(stampRaw)
	if err != nil || stamp.ConfigSHA256 != probe.expectedConfig {
		return ProbeResult{}, classified(CategoryListIdentityMismatch, http.StatusOK, nil)
	}
	if err := policy.admitStamp(stamp); err != nil {
		return ProbeResult{}, err
	}
	if err := policy.admitVersionBases(m, probe.list); err != nil {
		return ProbeResult{}, err
	}

	candidatesRaw, ok := m["candidates"]
	if !ok {
		return ProbeResult{}, classified(CategoryBackendProtocol, http.StatusOK, nil)
	}
	items, err := array(candidatesRaw)
	if err != nil {
		return ProbeResult{}, classified(CategoryBackendProtocol, http.StatusOK, nil)
	}
	candidates := make([]Candidate, len(items))
	seenIDs := make(map[string]struct{}, len(items))
	for i := range items {
		candidate, err := decodeCandidate(items[i], probe.list)
		if err != nil {
			return ProbeResult{}, err
		}
		if _, duplicate := seenIDs[candidate.entryID]; duplicate {
			return ProbeResult{}, classified(CategoryBackendProtocol, http.StatusOK, nil)
		}
		seenIDs[candidate.entryID] = struct{}{}
		candidates[i] = candidate
	}

	result := ProbeResult{candidates: candidates, stamp: stamp, tookUS: took}
	filterRaw, hasFilter := m["filter"]
	statsRaw, hasStats := m["filter_stats"]
	if probe.filterMode == probeUnfiltered {
		if hasFilter || hasStats {
			return ProbeResult{}, classified(CategoryFilterMismatch, http.StatusOK, nil)
		}
		return result, nil
	}
	if !hasFilter || !hasStats || !bytes.Equal(filterRaw, probe.expectedEchoJSON) {
		return ProbeResult{}, classified(CategoryFilterMismatch, http.StatusOK, nil)
	}
	statsMap, err := object(statsRaw)
	if err != nil || len(statsMap) != 1 {
		return ProbeResult{}, classified(CategoryFilterMismatch, http.StatusOK, nil)
	}
	statsObjectRaw, ok := statsMap[probe.list]
	if !ok {
		return ProbeResult{}, classified(CategoryFilterMismatch, http.StatusOK, nil)
	}
	statsObject, err := object(statsObjectRaw)
	if err != nil {
		return ProbeResult{}, classified(CategoryBackendProtocol, http.StatusOK, nil)
	}
	var stats FilterStats
	if err := required(statsObject, "evaluated", &stats.Evaluated); err != nil {
		return ProbeResult{}, classified(CategoryBackendProtocol, http.StatusOK, nil)
	}
	if err := required(statsObject, "rejected", &stats.Rejected); err != nil ||
		stats.Evaluated < 0 || stats.Rejected < 0 || stats.Rejected > stats.Evaluated {
		return ProbeResult{}, classified(CategoryBackendProtocol, http.StatusOK, nil)
	}
	result.appliedFilter = bytes.Clone(filterRaw)
	result.filterStats = stats
	result.hasFilterStats = true
	return result, nil
}

func decodeCandidate(raw []byte, expectedList string) (Candidate, error) {
	m, err := object(raw)
	if err != nil {
		return Candidate{}, classified(CategoryBackendProtocol, http.StatusOK, nil)
	}
	var candidate Candidate
	if err := required(m, "list", &candidate.list); err != nil || candidate.list != expectedList {
		return Candidate{}, classified(CategoryListIdentityMismatch, http.StatusOK, nil)
	}
	if err := required(m, "entry_id", &candidate.entryID); err != nil || candidate.entryID == "" {
		return Candidate{}, classified(CategoryBackendProtocol, http.StatusOK, nil)
	}
	if err := required(m, "score", &candidate.score); err != nil {
		return Candidate{}, classified(CategoryBackendProtocol, http.StatusOK, nil)
	}
	if err := required(m, "key", &candidate.key); err != nil {
		return Candidate{}, classified(CategoryBackendProtocol, http.StatusOK, nil)
	}
	if payload, ok := m["payload"]; ok {
		candidate.payload = bytes.Clone(payload)
	}
	return candidate, nil
}
