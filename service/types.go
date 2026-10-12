// Package service exposes bounded adapter search semantics over HTTP without
// changing the Kurn engine or the pure adapter contracts.
package service

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"regexp"
	"sort"
	"time"
	"unicode/utf8"

	"github.com/kurn-dev/kurn-search/adapter"
	"github.com/kurn-dev/kurn-search/orchestrator"
	"github.com/kurn-dev/kurn-search/profile"
)

const (
	MaxServiceRetryAfter = 5 * time.Second
	MaxAdmissionWait     = 5 * time.Second
	// MaxServiceQueue bounds Config.MaxQueue, the number of requests that
	// may wait for a list's admission slots; DefaultQueueFactor × the
	// list's max_inflight applies when the configuration names no bound.
	MaxServiceQueue    = 4096
	DefaultQueueFactor = 4
	ReadHeaderTimeout  = 2 * time.Second
	ReadTimeout        = 5 * time.Second
	IdleTimeout        = 30 * time.Second
	ShutdownTimeout    = 10 * time.Second
	MaxHeaderBytes     = 16 << 10
	writeGrace         = 5 * time.Second
)

var errInvalidService = errors.New("service: invalid configuration")

// Principal is the immutable authenticated scope input. Exactly one tenant is
// admitted; callers cannot construct a valid value without that invariant.
type Principal struct {
	tenant string
	valid  bool
}

func NewPrincipal(tenants []string) (Principal, error) {
	if len(tenants) != 1 || tenants[0] == "" || !utf8.ValidString(tenants[0]) || len(tenants[0]) > 4096 {
		return Principal{}, errInvalidService
	}
	return Principal{tenant: tenants[0], valid: true}, nil
}

func (p Principal) Tenant() string { return p.tenant }
func (p Principal) Valid() bool    { return p.valid }

// Authenticator maps request headers to one authenticated principal. Service
// code never supplies an anonymous or development fallback.
type Authenticator interface {
	Authenticate(context.Context, http.Header) (Principal, error)
}

// Runner is the service-facing execution seam. WrapExecutor binds the real
// orchestrator without exposing transport concerns to adapters.
type Runner interface {
	Preflight(context.Context, []byte, []byte) error
	Run(context.Context, []byte, []byte) (Execution, error)
}

type Execution struct {
	resolution         adapter.Resolution
	summary            adapter.Summary
	alternativeSummary adapter.AlternativeSummary
	versions           []producedList
	adapterName        string
	adapterVersion     string
	behaviorSHA256     string
	versionAttempt     int
	poolStep           int
	pool               int
	backendTookUS      int64
	fallbackTaken      bool
	fallbackHits       int
	fallbackCalls      int
	valid              bool
}

type ProducedListSpec struct {
	Role  string
	Name  string
	Stamp string
}

type producedList struct {
	role  string
	name  string
	stamp string
}

type ExecutionSpec struct {
	Resolution         adapter.Resolution
	Summary            adapter.Summary
	AlternativeSummary adapter.AlternativeSummary
	Lists              []ProducedListSpec
	AdapterName        string
	AdapterVersion     string
	BehaviorSHA256     string
	VersionAttempt     int
	PoolStep           int
	Pool               int
	BackendTookUS      int64
	FallbackTaken      bool
	FallbackHits       int
	FallbackCalls      int
}

var digestRE = regexp.MustCompile(`^[0-9a-f]{64}$`)

func NewExecution(spec ExecutionSpec) (Execution, error) {
	if !spec.Resolution.Valid() || !spec.Resolution.Complete() || !validRoute(spec.AdapterName) ||
		spec.AdapterVersion == "" || !digestRE.MatchString(spec.BehaviorSHA256) ||
		spec.VersionAttempt < 1 || spec.PoolStep < 1 || spec.Pool < 1 || spec.BackendTookUS < 0 || len(spec.Lists) == 0 ||
		spec.FallbackHits < 0 || (spec.FallbackHits > 0 && !spec.FallbackTaken) || spec.FallbackCalls < 0 || (spec.FallbackTaken && spec.FallbackCalls < 1) {
		return Execution{}, errInvalidService
	}
	if spec.Summary.Valid() && spec.AlternativeSummary.Valid() {
		return Execution{}, errInvalidService
	}
	versions := make([]producedList, len(spec.Lists))
	seen := make(map[string]struct{}, len(spec.Lists))
	for index, list := range spec.Lists {
		if list.Role == "" || list.Name == "" || list.Stamp == "" {
			return Execution{}, errInvalidService
		}
		key := list.Role + "\x00" + list.Name
		if _, exists := seen[key]; exists {
			return Execution{}, errInvalidService
		}
		seen[key] = struct{}{}
		versions[index] = producedList{role: list.Role, name: list.Name, stamp: list.Stamp}
	}
	sort.Slice(versions, func(i, j int) bool {
		if versions[i].role != versions[j].role {
			return versions[i].role < versions[j].role
		}
		return versions[i].name < versions[j].name
	})
	return Execution{
		resolution: spec.Resolution, summary: spec.Summary, alternativeSummary: spec.AlternativeSummary,
		versions: versions, adapterName: spec.AdapterName,
		adapterVersion: spec.AdapterVersion, behaviorSHA256: spec.BehaviorSHA256,
		versionAttempt: spec.VersionAttempt, poolStep: spec.PoolStep, pool: spec.Pool,
		backendTookUS: spec.BackendTookUS, valid: true,
		fallbackTaken: spec.FallbackTaken, fallbackHits: spec.FallbackHits, fallbackCalls: spec.FallbackCalls,
	}, nil
}

func (e Execution) Valid() bool { return e.valid && e.resolution.Valid() && e.resolution.Complete() }

// Summary returns the matched-set summary; HasSummary is the explicit
// presence discriminator, never a zero-value guess.
func (e Execution) Summary() adapter.Summary                       { return e.summary }
func (e Execution) HasSummary() bool                               { return e.summary.Valid() }
func (e Execution) AlternativeSummary() adapter.AlternativeSummary { return e.alternativeSummary }
func (e Execution) HasAlternativeSummary() bool                    { return e.alternativeSummary.Valid() }

type executorRunner struct{ executor *orchestrator.Executor }

func WrapExecutor(executor *orchestrator.Executor) (Runner, error) {
	if executor == nil || executor.Facets() || executor.Alternatives() {
		return nil, errInvalidService
	}
	return executorRunner{executor: executor}, nil
}

// preparingRunner is implemented by runners whose run can reuse the
// preflight's compiled request instead of compiling the same bytes again.
type preparingRunner interface {
	Prepare(context.Context, []byte, []byte) (orchestrator.Prepared, error)
	RunPrepared(context.Context, orchestrator.Prepared) (Execution, error)
}

func (r executorRunner) Prepare(ctx context.Context, request, scope []byte) (orchestrator.Prepared, error) {
	return r.executor.Prepare(ctx, request, scope)
}

func (r executorRunner) RunPrepared(ctx context.Context, prepared orchestrator.Prepared) (Execution, error) {
	result, err := r.executor.SearchPrepared(ctx, prepared)
	return executionFromResult(result, err)
}

func (r executorRunner) Run(ctx context.Context, request, scope []byte) (Execution, error) {
	result, err := r.executor.Search(ctx, request, scope)
	return executionFromResult(result, err)
}

func executionFromResult(result orchestrator.Result, err error) (Execution, error) {
	if err != nil {
		return Execution{}, err
	}
	if !result.Valid() {
		return Execution{}, errInvalidService
	}
	lists := result.ListVersions()
	produced := make([]ProducedListSpec, len(lists))
	for index, list := range lists {
		produced[index] = ProducedListSpec{Role: string(list.Role()), Name: list.Name(), Stamp: list.Stamp()}
	}
	return NewExecution(ExecutionSpec{
		Resolution: result.Resolution(), Lists: produced,
		AdapterName: result.AdapterName(), AdapterVersion: result.AdapterVersion(),
		BehaviorSHA256: result.BehaviorSHA256(), VersionAttempt: result.VersionAttempt(),
		PoolStep: result.PoolStep(), Pool: result.Pool(), BackendTookUS: result.BackendTookUS(),
		FallbackTaken: result.FallbackTaken(), FallbackHits: result.FallbackHits(), FallbackCalls: result.FallbackCalls(),
	})
}

func (r executorRunner) Preflight(ctx context.Context, request, scope []byte) error {
	return r.executor.Preflight(ctx, request, scope)
}

type Binding struct {
	Descriptor   profile.Descriptor
	Runner       Runner
	facets       bool
	alternatives bool
}

// FacetRunner is the closed runner produced only by WrapFacetExecutor from a
// facet-constructed executor. It carries matched-set summaries.
type FacetRunner struct{ executor *orchestrator.Executor }

// WrapFacetExecutor binds a facet-constructed executor and refuses anything
// else, including ordinary executors.
func WrapFacetExecutor(executor *orchestrator.Executor) (FacetRunner, error) {
	if executor == nil || !executor.Facets() || executor.Alternatives() {
		return FacetRunner{}, errInvalidService
	}
	return FacetRunner{executor: executor}, nil
}

func (r FacetRunner) Preflight(ctx context.Context, request, scope []byte) error {
	return r.executor.Preflight(ctx, request, scope)
}

func (r FacetRunner) Run(ctx context.Context, request, scope []byte) (Execution, error) {
	result, err := r.executor.Search(ctx, request, scope)
	if err != nil {
		return Execution{}, err
	}
	if !result.Valid() || !result.Summary().Valid() {
		return Execution{}, errInvalidService
	}
	lists := result.ListVersions()
	produced := make([]ProducedListSpec, len(lists))
	for index, list := range lists {
		produced[index] = ProducedListSpec{Role: string(list.Role()), Name: list.Name(), Stamp: list.Stamp()}
	}
	return NewExecution(ExecutionSpec{
		Resolution: result.Resolution(), Summary: result.Summary(), Lists: produced,
		AdapterName: result.AdapterName(), AdapterVersion: result.AdapterVersion(),
		BehaviorSHA256: result.BehaviorSHA256(), VersionAttempt: result.VersionAttempt(),
		PoolStep: result.PoolStep(), Pool: result.Pool(), BackendTookUS: result.BackendTookUS(),
		FallbackTaken: result.FallbackTaken(), FallbackHits: result.FallbackHits(), FallbackCalls: result.FallbackCalls(),
	})
}

// NewFacetBinding is the only constructor that marks a binding
// facet-expected; the pairing cannot be toggled independently, and the
// registry rejects any mismatch before serving.
func NewFacetBinding(descriptor profile.Descriptor, runner FacetRunner) (Binding, error) {
	if !descriptor.Valid() || runner.executor == nil {
		return Binding{}, errInvalidService
	}
	return Binding{Descriptor: descriptor, Runner: runner, facets: true}, nil
}

// AlternativeRunner is the only service runner for the self-excluding
// alternative-summary strategy.
type AlternativeRunner struct{ executor *orchestrator.Executor }

func WrapAlternativeExecutor(executor *orchestrator.Executor) (AlternativeRunner, error) {
	if executor == nil || !executor.Alternatives() || executor.Facets() {
		return AlternativeRunner{}, errInvalidService
	}
	return AlternativeRunner{executor: executor}, nil
}

func (r AlternativeRunner) Preflight(ctx context.Context, request, scope []byte) error {
	return r.executor.Preflight(ctx, request, scope)
}

func (r AlternativeRunner) Run(ctx context.Context, request, scope []byte) (Execution, error) {
	result, err := r.executor.Search(ctx, request, scope)
	if err != nil {
		return Execution{}, err
	}
	if !result.Valid() || !result.AlternativeSummary().Valid() || result.Summary().Valid() {
		return Execution{}, errInvalidService
	}
	lists := result.ListVersions()
	produced := make([]ProducedListSpec, len(lists))
	for index, list := range lists {
		produced[index] = ProducedListSpec{Role: string(list.Role()), Name: list.Name(), Stamp: list.Stamp()}
	}
	return NewExecution(ExecutionSpec{
		Resolution: result.Resolution(), AlternativeSummary: result.AlternativeSummary(), Lists: produced,
		AdapterName: result.AdapterName(), AdapterVersion: result.AdapterVersion(),
		BehaviorSHA256: result.BehaviorSHA256(), VersionAttempt: result.VersionAttempt(),
		PoolStep: result.PoolStep(), Pool: result.Pool(), BackendTookUS: result.BackendTookUS(),
		FallbackTaken: result.FallbackTaken(), FallbackHits: result.FallbackHits(), FallbackCalls: result.FallbackCalls(),
	})
}

func NewAlternativeBinding(descriptor profile.Descriptor, runner AlternativeRunner) (Binding, error) {
	if !descriptor.Valid() || runner.executor == nil {
		return Binding{}, errInvalidService
	}
	return Binding{Descriptor: descriptor, Runner: runner, alternatives: true}, nil
}

type RateAttestation struct {
	RatePerSecond int
	Unlimited     bool
}

// Golden is an operational readiness probe. AuthHandle is opaque to the
// service and is only materialized into the readiness Authorization header.
type Golden struct {
	Name             string
	Route            string
	AuthHandle       string
	SentinelTenant   string
	Query            string
	PartitionID      string
	Limit            int
	ExpectedIDs      []string
	ExpectedComplete bool
}

type Event struct {
	Kind       string
	Adapter    string
	Version    string
	ListRole   string
	Outcome    string
	Category   string
	Attempt    int
	PoolStep   int
	DurationUS int64
	BackendUS  int64
	ResidualUS int64
	WaitUS     int64
	// FallbackTaken and FallbackHits describe the adapter's fallback step
	// in a successful request (see orchestrator.Result).
	FallbackTaken bool
	FallbackHits  int
	FallbackCalls int
}

type Observer interface{ Observe(Event) }

type Config struct {
	Bindings       []Binding
	Authenticator  Authenticator
	Observer       Observer
	AttestedEngine string
	Rate           RateAttestation
	AdmissionWait  time.Duration
	// MaxQueue bounds the requests waiting for admission per physical
	// list (0 selects DefaultQueueFactor × max_inflight); a request that
	// finds the queue full is refused at once with 503
	// service_queue_full and Retry-After, before any preparation.
	MaxQueue         int
	CredentialFile   string
	KurnEndpoint     string
	ReadinessGoldens []Golden
	Now              func() time.Time
}

func nilInterface(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}
