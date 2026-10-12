package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kurn-dev/kurn-search/adapter"
	"github.com/kurn-dev/kurn-search/backend/kurnhttp"
	"github.com/kurn-dev/kurn-search/orchestrator"
	"github.com/kurn-dev/kurn-search/profile"
)

var (
	errInvalidRequest       = errors.New("service: invalid request")
	errAdmissionUnavailable = errors.New("service: admission unavailable")
)

type Service struct {
	registry      registry
	authenticator Authenticator
	observer      Observer
	admission     admission
	goldens       []Golden
	now           func() time.Time
	maxRequest    int
	maxResponse   int
	maxTimeout    time.Duration
}

// Compose is the executable composition boundary. A nil configuration is an
// explicit fail-closed refusal; callers must supply every startup authority.
func Compose(config *Config) (*Service, error) {
	if config == nil {
		return nil, errInvalidService
	}
	return New(*config)
}

func New(config Config) (*Service, error) {
	registry, err := newRegistry(config.Bindings, config.MaxQueue)
	if err != nil || nilInterface(config.Authenticator) || validateRate(config.Rate, config.Bindings) != nil ||
		validateCredentialFile(config.CredentialFile) != nil || validateEndpoint(config.KurnEndpoint) != nil ||
		len(config.ReadinessGoldens) == 0 {
		return nil, errInvalidService
	}
	for _, binding := range config.Bindings {
		if !descriptorAdmitsEngine(binding.Descriptor, config.AttestedEngine) {
			return nil, errInvalidService
		}
	}
	admission, err := newAdmission(registry, config.AdmissionWait)
	if err != nil {
		return nil, err
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	service := &Service{
		registry: registry, authenticator: config.Authenticator, observer: config.Observer,
		admission: admission, goldens: cloneGoldens(config.ReadinessGoldens), now: now,
	}
	for _, binding := range config.Bindings {
		spec := binding.Descriptor.Spec()
		if spec.Bounds.MaxRequestBytes > service.maxRequest {
			service.maxRequest = spec.Bounds.MaxRequestBytes
		}
		if spec.Bounds.MaxResponseBytes > service.maxResponse {
			service.maxResponse = spec.Bounds.MaxResponseBytes
		}
		timeout := time.Duration(spec.Bounds.TimeoutMillis) * time.Millisecond
		if timeout <= 0 || timeout > time.Duration(600_000)*time.Millisecond {
			return nil, errInvalidService
		}
		if timeout > service.maxTimeout {
			service.maxTimeout = timeout
		}
	}
	if validateGoldens(service.registry, service.goldens) != nil {
		return nil, errInvalidService
	}
	if validateGoldenPreflights(service.registry, service.goldens) != nil {
		return nil, errInvalidService
	}
	return service, nil
}

func validateGoldenPreflights(registry registry, goldens []Golden) error {
	for _, golden := range goldens {
		binding, ok := registry.binding(golden.Route)
		if !ok {
			return errInvalidService
		}
		request := searchRequest{query: golden.Query, partitionID: golden.PartitionID,
			hasPartition: golden.PartitionID != "", limit: golden.Limit}
		requestBytes, err := request.adapterJSON()
		if err != nil {
			return errInvalidService
		}
		scope, err := trustedScopeJSON(golden.SentinelTenant)
		if err != nil {
			return errInvalidService
		}
		err = binding.Runner.Preflight(context.Background(), requestBytes, scope)
		if err != nil {
			return errInvalidService
		}
	}
	return nil
}

func validateEndpoint(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errInvalidService
	}
	return nil
}

func validateRate(rate RateAttestation, bindings []Binding) error {
	if rate.Unlimited {
		if rate.RatePerSecond != 0 {
			return errInvalidService
		}
		return nil
	}
	if rate.RatePerSecond < 1 {
		return errInvalidService
	}
	for _, binding := range bindings {
		minimum := (binding.Descriptor.Spec().Bounds.MaxProbes + 1) / 2
		if rate.RatePerSecond < minimum {
			return errInvalidService
		}
	}
	return nil
}

func validateCredentialFile(path string) error {
	if path == "" {
		return errInvalidService
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 || info.Size() < 1 || info.Size() > 64<<10 {
		return errInvalidService
	}
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) == 0 || len(bytes.TrimSpace(raw)) != len(raw) {
		return errInvalidService
	}
	return nil
}

func cloneGoldens(input []Golden) []Golden {
	result := append([]Golden(nil), input...)
	for index := range result {
		result[index].ExpectedIDs = append([]string(nil), input[index].ExpectedIDs...)
	}
	return result
}

func validateGoldens(registry registry, goldens []Golden) error {
	seen := map[string]struct{}{}
	for _, golden := range goldens {
		if golden.Name == "" || golden.AuthHandle == "" || golden.SentinelTenant == "" || golden.Query == "" ||
			golden.Limit < 1 || !golden.ExpectedComplete || !validRoute(golden.Route) {
			return errInvalidService
		}
		binding, ok := registry.binding(golden.Route)
		if !ok {
			return errInvalidService
		}
		bounds := binding.Descriptor.Spec().Bounds
		if !utf8.ValidString(golden.Query) || utf8.RuneCountInString(golden.Query) > bounds.MaxQueryRunes ||
			golden.Limit > bounds.MaxResultLimit || len(golden.ExpectedIDs) > golden.Limit ||
			!utf8.ValidString(golden.SentinelTenant) || (golden.PartitionID != "" && !utf8.ValidString(golden.PartitionID)) {
			return errInvalidService
		}
		ids := make(map[string]struct{}, len(golden.ExpectedIDs))
		for _, id := range golden.ExpectedIDs {
			if id == "" || !utf8.ValidString(id) || len(id) > adapter.MaxEntryIDBytes {
				return errInvalidService
			}
			if _, duplicate := ids[id]; duplicate {
				return errInvalidService
			}
			ids[id] = struct{}{}
		}
		if _, exists := seen[golden.Name]; exists {
			return errInvalidService
		}
		seen[golden.Name] = struct{}{}
	}
	return nil
}

func (s *Service) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	receivedWall := time.Now()
	received := s.now()
	if request == nil || request.Context() == nil {
		s.refuse(writer, http.StatusBadRequest, "invalid_request", 0, received, "", "")
		return
	}
	principal, err := s.authenticator.Authenticate(request.Context(), request.Header.Clone())
	if err != nil {
		s.observe(Event{Kind: "authentication", Outcome: "refused", Category: "unauthenticated"})
		writer.Header().Set("WWW-Authenticate", "Bearer")
		s.refuse(writer, http.StatusUnauthorized, "unauthenticated", 0, received, "", "")
		return
	}
	if !principal.Valid() {
		s.observe(Event{Kind: "authentication", Outcome: "refused", Category: "scope_refused"})
		s.refuse(writer, http.StatusForbidden, "scope_refused", 0, received, "", "")
		return
	}
	s.observe(Event{Kind: "authentication", Outcome: "success"})
	if request.Method != http.MethodPost {
		writer.Header().Set("Allow", http.MethodPost)
		s.refuse(writer, http.StatusMethodNotAllowed, "method_not_allowed", 0, received, "", "")
		return
	}
	if values := request.Header.Values("Content-Type"); len(values) != 1 || values[0] != "application/json" {
		s.refuse(writer, http.StatusUnsupportedMediaType, "unsupported_media_type", 0, received, "", "")
		return
	}
	route, ok := parseRoute(request.URL.Path)
	if !ok {
		s.refuse(writer, http.StatusNotFound, "adapter_not_found", 0, received, "", "")
		return
	}
	binding, ok := s.registry.binding(route)
	if !ok {
		s.refuse(writer, http.StatusNotFound, "adapter_not_found", 0, received, "", "")
		return
	}
	spec := binding.Descriptor.Spec()
	if request.Body == nil {
		s.refuse(writer, http.StatusBadRequest, "invalid_request", 0, received, spec.Name, spec.Version)
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, int64(spec.Bounds.MaxRequestBytes))
	raw, err := io.ReadAll(request.Body)
	if err != nil {
		s.refuse(writer, http.StatusBadRequest, "invalid_request", 0, received, spec.Name, spec.Version)
		return
	}
	search, err := parseSearchRequest(raw, spec.Bounds)
	if err != nil {
		s.refuse(writer, http.StatusBadRequest, "invalid_request", 0, received, spec.Name, spec.Version)
		return
	}
	if search.adapterVersion != "" && search.adapterVersion != spec.Version {
		s.refuse(writer, http.StatusConflict, "adapter_version_mismatch", 0, received, spec.Name, spec.Version)
		return
	}
	adapterRequest, err := search.adapterJSON()
	if err != nil {
		s.refuse(writer, http.StatusBadRequest, "invalid_request", 0, received, spec.Name, spec.Version)
		return
	}
	scope, err := trustedScopeJSON(principal.Tenant())
	if err != nil || len(scope) > spec.Bounds.MaxTrustedScopeBytes {
		s.refuse(writer, http.StatusForbidden, "scope_refused", 0, received, spec.Name, spec.Version)
		return
	}
	lists := make([]string, len(spec.Lists))
	for index, list := range spec.Lists {
		lists[index] = list.Name
	}
	// The queue bound is taken before preparation: a request that cannot
	// even wait is refused at once, and the memory of a waiting request
	// is bounded by the queue, not by the backend token channel alone.
	dequeue, err := s.admission.enqueue(lists)
	if err != nil {
		if errors.Is(err, errQueueFull) {
			s.observe(Event{Kind: "admission", Adapter: spec.Name, Version: spec.Version, Outcome: "refused", Category: "service_queue_full", WaitUS: nonnegativeUS(s.now().Sub(received))})
			s.refuse(writer, http.StatusServiceUnavailable, "service_queue_full", time.Second, received, spec.Name, spec.Version)
		} else {
			s.refuse(writer, http.StatusInternalServerError, "internal_failure", 0, received, spec.Name, spec.Version)
		}
		return
	}
	defer dequeue()
	deadline := receivedWall.Add(time.Duration(spec.Bounds.TimeoutMillis) * time.Millisecond)
	operation, cancel := context.WithDeadline(request.Context(), deadline)
	defer cancel()
	preparing, canPrepare := binding.Runner.(preparingRunner)
	var prepared orchestrator.Prepared
	var preflightErr error
	if canPrepare {
		prepared, preflightErr = preparing.Prepare(operation, adapterRequest, scope)
	} else {
		preflightErr = binding.Runner.Preflight(operation, adapterRequest, scope)
	}
	if err := preflightErr; err != nil {
		status, category, _ := mapExecutionError(err)
		if category != "invalid_request" && category != "operation_deadline" {
			status, category = http.StatusInternalServerError, "internal_failure"
		}
		s.refuse(writer, status, category, 0, received, spec.Name, spec.Version)
		return
	}
	admissionStarted := s.now()
	release, hint, err := s.admission.acquire(operation, lists)
	if err != nil {
		if errors.Is(err, errAdmissionUnavailable) {
			s.observe(Event{Kind: "admission", Adapter: spec.Name, Version: spec.Version, Outcome: "refused", Category: "service_admission_unavailable", WaitUS: nonnegativeUS(s.now().Sub(received))})
			s.refuse(writer, http.StatusServiceUnavailable, "service_admission_unavailable", hint, received, spec.Name, spec.Version)
		} else {
			s.refuse(writer, http.StatusGatewayTimeout, "operation_deadline", 0, received, spec.Name, spec.Version)
		}
		return
	}
	defer release()
	s.observe(Event{Kind: "admission", Adapter: spec.Name, Version: spec.Version, Outcome: "admitted",
		WaitUS: nonnegativeUS(s.now().Sub(admissionStarted))})
	var execution Execution
	if canPrepare {
		execution, err = preparing.RunPrepared(operation, prepared)
	} else {
		execution, err = binding.Runner.Run(operation, adapterRequest, scope)
	}
	if err != nil {
		status, category, retry := mapExecutionError(err)
		s.refuse(writer, status, category, retry, received, spec.Name, spec.Version)
		return
	}
	if err := operation.Err(); err != nil {
		s.refuse(writer, http.StatusGatewayTimeout, "operation_deadline", 0, received, spec.Name, spec.Version)
		return
	}
	if !execution.Valid() || execution.adapterName != spec.Name || execution.adapterVersion != spec.Version ||
		execution.behaviorSHA256 != binding.Descriptor.Identities().Behavior.Hex() ||
		execution.HasSummary() != binding.facets || execution.HasAlternativeSummary() != binding.alternatives ||
		validateExecutionMetadata(spec, search.limit, execution) != nil ||
		validateProducedLists(spec.Lists, execution.versions) != nil {
		s.refuse(writer, http.StatusBadGateway, string(orchestrator.CategoryListIdentityMismatch), 0, received, spec.Name, spec.Version)
		return
	}
	var body []byte
	if binding.alternatives {
		body, err = encodeAlternativeSuccess(execution, nonnegativeUS(s.now().Sub(received)), spec.Bounds.MaxResponseBytes)
	} else {
		body, err = encodeSuccess(execution, nonnegativeUS(s.now().Sub(received)), spec.Bounds.MaxResponseBytes)
	}
	if err != nil {
		s.refuse(writer, http.StatusBadGateway, string(orchestrator.CategoryInvalidEvidence), 0, received, spec.Name, spec.Version)
		return
	}
	if err := operation.Err(); err != nil {
		s.refuse(writer, http.StatusGatewayTimeout, "operation_deadline", 0, received, spec.Name, spec.Version)
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write(body)
	totalUS := nonnegativeUS(s.now().Sub(received))
	residualUS := totalUS - execution.backendTookUS
	if residualUS < 0 {
		residualUS = 0
	}
	s.observe(Event{Kind: "request", Adapter: spec.Name, Version: spec.Version, Outcome: "success",
		Attempt: execution.versionAttempt, PoolStep: execution.poolStep, DurationUS: totalUS,
		BackendUS: execution.backendTookUS, ResidualUS: residualUS,
		FallbackTaken: execution.fallbackTaken, FallbackHits: execution.fallbackHits, FallbackCalls: execution.fallbackCalls})
	for _, list := range execution.versions {
		s.observe(Event{Kind: "list", Adapter: spec.Name, Version: spec.Version, ListRole: list.role,
			Outcome: "success", Attempt: execution.versionAttempt, PoolStep: execution.poolStep})
	}
}

func validateExecutionMetadata(spec profile.Spec, limit int, execution Execution) error {
	if execution.versionAttempt < 1 || execution.versionAttempt > spec.Bounds.VersionAttempts ||
		execution.poolStep < 1 || execution.poolStep > len(spec.Bounds.PoolLadder) ||
		execution.pool != spec.Bounds.PoolLadder[execution.poolStep-1] ||
		len(execution.resolution.Hits()) > limit {
		return errInvalidService
	}
	return nil
}

func validateProducedLists(expected []profile.PhysicalListSpec, actual []producedList) error {
	if len(expected) != len(actual) {
		return errInvalidService
	}
	want := make(map[string]profile.PhysicalListSpec, len(expected))
	for _, list := range expected {
		want[string(list.Role)+"\x00"+list.Name] = list
	}
	for _, list := range actual {
		key := list.role + "\x00" + list.name
		expectedList, ok := want[key]
		if !ok {
			return errInvalidService
		}
		stamp, err := kurnhttp.ParseStamp(list.stamp)
		if err != nil || stamp.ConfigSHA256 != expectedList.ConfigSHA256 {
			return errInvalidService
		}
		delete(want, key)
	}
	if len(want) != 0 {
		return errInvalidService
	}
	return nil
}

func parseRoute(path string) (string, bool) {
	const prefix = "/v1/search/"
	if !strings.HasPrefix(path, prefix) {
		return "", false
	}
	route := strings.TrimPrefix(path, prefix)
	if strings.Contains(route, "/") || !validRoute(route) {
		return "", false
	}
	return route, true
}

type hitResponse struct {
	EntryID string          `json:"entry_id"`
	Result  json.RawMessage `json:"result"`
}

type listResponse struct {
	Role  string `json:"role"`
	Name  string `json:"name"`
	Stamp string `json:"stamp"`
}

type facetBucketResponse struct {
	Count int    `json:"count"`
	Value string `json:"value"`
}

// facetResponse is the closed route-specific summary shape. Member order is
// the canonical key order; bucket array order is the semantic contract.
type facetResponse struct {
	Category     []facetBucketResponse `json:"category"`
	Manufacturer []facetBucketResponse `json:"manufacturer"`
	Status       []facetBucketResponse `json:"status"`
}

type successResponse struct {
	Adapter struct {
		Name           string `json:"name"`
		Version        string `json:"version"`
		BehaviorSHA256 string `json:"behavior_sha256"`
	} `json:"adapter"`
	Complete       bool           `json:"complete"`
	TotalMatches   *int           `json:"total_matches,omitempty"`
	Hits           []hitResponse  `json:"hits"`
	Facets         *facetResponse `json:"facets,omitempty"`
	Lists          []listResponse `json:"lists"`
	VersionAttempt int            `json:"version_attempt"`
	PoolStep       int            `json:"pool_step"`
	Pool           int            `json:"pool"`
	TimingUS       struct {
		Backend  int64 `json:"backend"`
		Service  int64 `json:"service"`
		Residual int64 `json:"residual"`
	} `json:"timing_us"`
}

// alternativeSuccessResponse is deliberately separate from successResponse:
// old ordinary/facet encoders and their response bytes remain untouched.
type alternativeSuccessResponse struct {
	Adapter struct {
		Name           string `json:"name"`
		Version        string `json:"version"`
		BehaviorSHA256 string `json:"behavior_sha256"`
	} `json:"adapter"`
	Complete     bool          `json:"complete"`
	TotalMatches int           `json:"total_matches"`
	Hits         []hitResponse `json:"hits"`
	Facets       struct {
		Category     alternativeFacetResponse `json:"category"`
		Manufacturer alternativeFacetResponse `json:"manufacturer"`
		Status       alternativeFacetResponse `json:"status"`
	} `json:"facets"`
	Lists          []listResponse `json:"lists"`
	VersionAttempt int            `json:"version_attempt"`
	PoolStep       int            `json:"pool_step"`
	Pool           int            `json:"pool"`
	TimingUS       struct {
		Backend  int64 `json:"backend"`
		Service  int64 `json:"service"`
		Residual int64 `json:"residual"`
	} `json:"timing_us"`
}

type alternativeFacetResponse struct {
	BasisTotal int                   `json:"basis_total"`
	Buckets    []facetBucketResponse `json:"buckets"`
}

func encodeAlternativeSuccess(execution Execution, serviceUS int64, maximum int) ([]byte, error) {
	if !execution.HasAlternativeSummary() || execution.HasSummary() {
		return nil, errInvalidService
	}
	summary := execution.AlternativeSummary()
	groups := summary.Groups()
	if len(groups) != 3 || groups[0].Name() != "category" ||
		groups[1].Name() != "manufacturer" || groups[2].Name() != "status" {
		return nil, errInvalidService
	}
	response := alternativeSuccessResponse{
		Complete: true, TotalMatches: summary.TotalMatches(), Hits: make([]hitResponse, 0),
		VersionAttempt: execution.versionAttempt, PoolStep: execution.poolStep, Pool: execution.pool,
	}
	response.Adapter.Name = execution.adapterName
	response.Adapter.Version = execution.adapterVersion
	response.Adapter.BehaviorSHA256 = execution.behaviorSHA256
	for _, hit := range execution.resolution.Hits() {
		if !hit.Valid() || !json.Valid(hit.Result()) {
			return nil, errInvalidService
		}
		response.Hits = append(response.Hits, hitResponse{EntryID: hit.EntryID(), Result: hit.Result()})
	}
	groupsOut := []*alternativeFacetResponse{
		&response.Facets.Category, &response.Facets.Manufacturer, &response.Facets.Status,
	}
	for index, group := range groups {
		groupsOut[index].BasisTotal = group.BasisTotal()
		groupsOut[index].Buckets = make([]facetBucketResponse, 0, len(group.Buckets()))
		for _, bucket := range group.Buckets() {
			groupsOut[index].Buckets = append(groupsOut[index].Buckets,
				facetBucketResponse{Count: bucket.Count(), Value: bucket.Value()})
		}
	}
	for _, version := range execution.versions {
		response.Lists = append(response.Lists, listResponse{Role: version.role, Name: version.name, Stamp: version.stamp})
	}
	response.TimingUS.Backend = execution.backendTookUS
	response.TimingUS.Service = serviceUS
	response.TimingUS.Residual = serviceUS - execution.backendTookUS
	if response.TimingUS.Residual < 0 {
		response.TimingUS.Residual = 0
	}
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(response); err != nil || output.Len() > maximum {
		return nil, errInvalidService
	}
	return output.Bytes(), nil
}

func encodeSuccess(execution Execution, serviceUS int64, maximum int) ([]byte, error) {
	response := successResponse{
		Complete: true, Hits: make([]hitResponse, 0),
		VersionAttempt: execution.versionAttempt, PoolStep: execution.poolStep, Pool: execution.pool,
	}
	response.Adapter.Name = execution.adapterName
	response.Adapter.Version = execution.adapterVersion
	response.Adapter.BehaviorSHA256 = execution.behaviorSHA256
	for _, hit := range execution.resolution.Hits() {
		if !hit.Valid() || !json.Valid(hit.Result()) {
			return nil, errInvalidService
		}
		response.Hits = append(response.Hits, hitResponse{EntryID: hit.EntryID(), Result: hit.Result()})
	}
	if execution.HasSummary() {
		summary := execution.Summary()
		groups := summary.Groups()
		if len(groups) != 3 {
			return nil, errInvalidService
		}
		facets := &facetResponse{}
		total := summary.TotalMatches()
		response.TotalMatches = &total
		for _, group := range groups {
			buckets := make([]facetBucketResponse, 0, len(group.Buckets()))
			for _, bucket := range group.Buckets() {
				buckets = append(buckets, facetBucketResponse{Count: bucket.Count(), Value: bucket.Value()})
			}
			switch group.Name() {
			case "category":
				facets.Category = buckets
			case "manufacturer":
				facets.Manufacturer = buckets
			case "status":
				facets.Status = buckets
			default:
				return nil, errInvalidService
			}
		}
		if facets.Category == nil || facets.Manufacturer == nil || facets.Status == nil {
			return nil, errInvalidService
		}
		response.Facets = facets
	}
	for _, version := range execution.versions {
		response.Lists = append(response.Lists, listResponse{Role: version.role, Name: version.name, Stamp: version.stamp})
	}
	response.TimingUS.Backend = execution.backendTookUS
	response.TimingUS.Service = serviceUS
	response.TimingUS.Residual = serviceUS - execution.backendTookUS
	if response.TimingUS.Residual < 0 {
		response.TimingUS.Residual = 0
	}
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(response); err != nil || output.Len() > maximum {
		return nil, errInvalidService
	}
	return output.Bytes(), nil
}

func mapExecutionError(err error) (int, string, time.Duration) {
	var execution *orchestrator.Error
	if !errors.As(err, &execution) {
		return http.StatusInternalServerError, "internal_failure", 0
	}
	retry := boundedBackendHint(execution.RetryAfter)
	switch execution.Category {
	case orchestrator.CategoryInvalidInput, orchestrator.CategoryInvalidPlan, orchestrator.CategoryInvalidFilter:
		return http.StatusBadRequest, "invalid_request", 0
	case orchestrator.CategoryIncompleteAtPoolCeiling:
		return http.StatusUnprocessableEntity, "incomplete_at_pool_ceiling", 0
	case orchestrator.CategoryBackendRateLimited:
		return http.StatusTooManyRequests, "backend_rate_limited", retry
	case orchestrator.CategoryBackendOverloaded:
		return http.StatusServiceUnavailable, "backend_overloaded", retry
	case orchestrator.CategoryDeadlineExceeded, orchestrator.CategoryCanceled:
		return http.StatusGatewayTimeout, "operation_deadline", 0
	case orchestrator.CategoryResponseTooLarge:
		return http.StatusBadGateway, string(execution.Category), 0
	case orchestrator.CategoryListIdentityMismatch:
		return http.StatusBadGateway, string(execution.Category), 0
	case orchestrator.CategoryInvalidEvidence, orchestrator.CategoryAdapterResolution, orchestrator.CategoryBackendProtocol,
		orchestrator.CategoryFilterMismatch, orchestrator.CategoryBackendInline:
		return http.StatusBadGateway, string(execution.Category), 0
	case orchestrator.CategoryTransportFailure, orchestrator.CategoryBackendUnauthorized, orchestrator.CategoryBackendForbidden,
		orchestrator.CategoryBackendNotFound, orchestrator.CategoryBackendConflict, orchestrator.CategoryBackendHTTP,
		orchestrator.CategoryBackendRedirect:
		return http.StatusBadGateway, string(execution.Category), 0
	default:
		return http.StatusInternalServerError, "internal_failure", 0
	}
}

func boundedBackendHint(value time.Duration) time.Duration {
	if value < time.Second || value > MaxServiceRetryAfter || value%time.Second != 0 {
		return 0
	}
	return value
}

type errorResponse struct {
	Category          string `json:"category"`
	RetryAfterSeconds *int   `json:"retry_after_seconds,omitempty"`
}

func writeError(writer http.ResponseWriter, status int, category string, retry time.Duration) {
	response := errorResponse{Category: category}
	if retry >= time.Second && retry <= MaxServiceRetryAfter && retry%time.Second == 0 {
		seconds := int(retry / time.Second)
		response.RetryAfterSeconds = &seconds
		writer.Header().Set("Retry-After", strconv.Itoa(seconds))
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.WriteHeader(status)
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(response)
}

func (s *Service) refuse(writer http.ResponseWriter, status int, category string, retry time.Duration, received time.Time, adapterName, adapterVersion string) {
	writeError(writer, status, category, retry)
	s.observe(Event{Kind: "request", Adapter: adapterName, Version: adapterVersion, Outcome: "refused",
		Category: category, DurationUS: nonnegativeUS(s.now().Sub(received))})
}

func nonnegativeUS(value time.Duration) int64 {
	if value < 0 {
		return 0
	}
	return value.Microseconds()
}

func (s *Service) observe(event Event) {
	if !nilInterface(s.observer) {
		s.observer.Observe(event)
	}
}

// ConstantTimeBearerAuthenticator is a private-test authenticator. It makes
// no anonymous fallback and performs fixed-width digest comparisons.
type ConstantTimeBearerAuthenticator struct {
	entries []bearerEntry
}

type bearerEntry struct {
	digest [32]byte
	tenant string
}

func NewConstantTimeBearerAuthenticator(credentials map[string]string) (*ConstantTimeBearerAuthenticator, error) {
	if len(credentials) == 0 {
		return nil, errInvalidService
	}
	result := &ConstantTimeBearerAuthenticator{}
	tokens := make([]string, 0, len(credentials))
	for token := range credentials {
		tokens = append(tokens, token)
	}
	sort.Strings(tokens)
	for _, token := range tokens {
		tenant := credentials[token]
		if token == "" || tenant == "" {
			return nil, errInvalidService
		}
		result.entries = append(result.entries, bearerEntry{digest: sha256Bytes(token), tenant: tenant})
	}
	return result, nil
}

func (a *ConstantTimeBearerAuthenticator) Authenticate(_ context.Context, headers http.Header) (Principal, error) {
	if a == nil {
		return Principal{}, errInvalidService
	}
	value := headers.Get("Authorization")
	if values := headers.Values("Authorization"); len(values) != 1 || values[0] != value ||
		!strings.HasPrefix(value, "Bearer ") || len(value) <= len("Bearer ") || len(value) > 4096 {
		return Principal{}, errInvalidService
	}
	digest := sha256Bytes(strings.TrimPrefix(value, "Bearer "))
	match := -1
	for index := range a.entries {
		if subtle.ConstantTimeCompare(digest[:], a.entries[index].digest[:]) == 1 {
			match = index
		}
	}
	if match < 0 {
		return Principal{}, errInvalidService
	}
	return NewPrincipal([]string{a.entries[match].tenant})
}

func sha256Bytes(value string) [32]byte { return sha256.Sum256([]byte(value)) }
