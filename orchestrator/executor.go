package orchestrator

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"sort"
	"time"

	"github.com/kurn-dev/kurn-search/adapter"
	"github.com/kurn-dev/kurn-search/backend/kurnhttp"
	"github.com/kurn-dev/kurn-search/profile"
)

type Backend interface {
	Batch(context.Context, []kurnhttp.Probe) (kurnhttp.BatchResult, error)
}

type RuntimeSemantics interface {
	adapter.Compiler
	adapter.Resolver
	adapter.FilterCompiler
	adapter.IncompleteClassifier
}

// MembershipRuntimeSemantics is deliberately separate from RuntimeSemantics:
// an implementation does not need a dummy legacy filter compiler.
type MembershipRuntimeSemantics interface {
	adapter.Compiler
	adapter.Resolver
	adapter.MembershipFilterCompiler
	adapter.IncompleteClassifier
}

// querySemantics is the compile/classify seam every executor needs. Resolution
// is constructor-fixed through resolveStrategy instead, so an executor built
// for a summary adapter never holds a live ordinary resolver.
type querySemantics interface {
	adapter.Compiler
	adapter.IncompleteClassifier
}

type appliedFilter struct {
	exact      kurnhttp.StringFilter
	membership kurnhttp.MembershipFilter
}

type filterStrategy func(profile.Spec, []byte, []byte) (appliedFilter, error)

// resolveStrategy is fixed once by each constructor. Ordinary constructors
// resolve without a summary and can never produce the summary-at-ceiling
// sentinel; the facet constructor's strategy is the only one that can.
type resolveStrategy func(request, trustedScope []byte, plan adapter.CompiledPlan, evidence []adapter.ProbeEvidence) (adapter.Resolution, adapter.Summary, adapter.AlternativeSummary, error)

// MembershipFallbackRuntimeSemantics is MembershipRuntimeSemantics for an
// adapter whose plan has a second step (adapter.FallbackCompiler).
type MembershipFallbackRuntimeSemantics interface {
	MembershipRuntimeSemantics
	adapter.FallbackCompiler
}

type Executor struct {
	descriptor    profile.Descriptor
	semantics     querySemantics
	compileFilter filterStrategy
	resolve       resolveStrategy
	fallback      adapter.FallbackCompiler // nil: single-step plans only
	backend       Backend
	facets        bool
	alternatives  bool
	valid         bool
}

func New(descriptor profile.Descriptor, semantics RuntimeSemantics, backend Backend) (*Executor, error) {
	if !descriptor.Valid() || nilInterface(semantics) || nilInterface(backend) {
		return nil, failure(CategoryInvalidProfile)
	}
	strategy := func(spec profile.Spec, request, scope []byte) (appliedFilter, error) {
		plan, err := semantics.CompileFilter(request, scope)
		if err != nil {
			return appliedFilter{}, failure(CategoryInvalidFilter)
		}
		filter, err := validatedFilter(spec, plan)
		return appliedFilter{exact: filter}, err
	}
	resolve := func(request, scope []byte, plan adapter.CompiledPlan, evidence []adapter.ProbeEvidence) (adapter.Resolution, adapter.Summary, adapter.AlternativeSummary, error) {
		resolution, err := semantics.Resolve(request, scope, plan, evidence)
		return resolution, adapter.Summary{}, adapter.AlternativeSummary{}, err
	}
	return &Executor{descriptor: descriptor, semantics: semantics, compileFilter: strategy, resolve: resolve, backend: backend, valid: true}, nil
}

// NewWithMembership fixes the membership strategy once, without optional
// interface probing or a scalar/filterless fallback on any failure.
func NewWithMembership(descriptor profile.Descriptor, semantics MembershipRuntimeSemantics, backend Backend) (*Executor, error) {
	if !descriptor.Valid() || nilInterface(semantics) || nilInterface(backend) {
		return nil, failure(CategoryInvalidProfile)
	}
	strategy := func(spec profile.Spec, request, scope []byte) (appliedFilter, error) {
		plan, err := semantics.CompileMembershipFilter(request, scope)
		if err != nil {
			return appliedFilter{}, failure(CategoryInvalidFilter)
		}
		filter, err := validatedMembershipFilter(spec, plan)
		return appliedFilter{membership: filter}, err
	}
	resolve := func(request, scope []byte, plan adapter.CompiledPlan, evidence []adapter.ProbeEvidence) (adapter.Resolution, adapter.Summary, adapter.AlternativeSummary, error) {
		resolution, err := semantics.Resolve(request, scope, plan, evidence)
		return resolution, adapter.Summary{}, adapter.AlternativeSummary{}, err
	}
	return &Executor{descriptor: descriptor, semantics: semantics, compileFilter: strategy, resolve: resolve, backend: backend, valid: true}, nil
}

// NewWithMembershipFallback is NewWithMembership for an adapter with a
// fallback step: after the first batch, the adapter may compile a second plan
// that runs in the same attempt (same pool rung, same filter, same list
// versions), and resolution sees both steps' plan and evidence.
func NewWithMembershipFallback(descriptor profile.Descriptor, semantics MembershipFallbackRuntimeSemantics, backend Backend) (*Executor, error) {
	executor, err := NewWithMembership(descriptor, semantics, backend)
	if err != nil {
		return nil, err
	}
	executor.fallback = semantics
	return executor, nil
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

type probeBinding struct {
	probe adapter.Probe
	list  profile.PhysicalListSpec
}

// Prepared is one request after the complete pure validation path: private
// copies of the exact validated request and trusted-scope bytes, and the plan,
// filter and probe bindings compiled from them. SearchPrepared runs it without
// compiling the same bytes again, so validation and execution cannot see
// different input. It is bound to the executor that prepared it.
type Prepared struct {
	executor              *Executor
	request, trustedScope []byte
	plan                  adapter.CompiledPlan
	filter                appliedFilter
	bindings              []probeBinding
	valid                 bool
}

// Prepare runs the complete pure request, trusted-scope, plan, and filter
// validation path without performing backend I/O and returns what it
// compiled. The service runtime uses it before admission.
func (e *Executor) Prepare(ctx context.Context, request, trustedScope []byte) (Prepared, error) {
	if e == nil || !e.valid || ctx == nil {
		return Prepared{}, failure(CategoryInvalidInput)
	}
	spec := e.descriptor.Spec()
	if len(request) == 0 || len(request) > spec.Bounds.MaxRequestBytes ||
		len(trustedScope) == 0 || len(trustedScope) > spec.Bounds.MaxTrustedScopeBytes {
		return Prepared{}, failure(CategoryInvalidInput)
	}
	if err := ctx.Err(); err != nil {
		return Prepared{}, contextFailure(err)
	}
	plan, err := e.semantics.Compile(request, trustedScope)
	if err != nil {
		return Prepared{}, failure(CategoryInvalidInput)
	}
	if err := e.descriptor.ValidatePlan(plan); err != nil {
		return Prepared{}, failure(CategoryInvalidPlan)
	}
	filter, err := e.compileFilter(spec, request, trustedScope)
	if err != nil {
		return Prepared{}, err
	}
	bindings, err := bindPlan(spec, plan)
	if err != nil {
		return Prepared{}, err
	}
	return Prepared{executor: e, request: bytes.Clone(request), trustedScope: bytes.Clone(trustedScope),
		plan: plan, filter: filter, bindings: bindings, valid: true}, nil
}

// Preflight is Prepare with the compiled result discarded. Search, unlike
// SearchPrepared, deliberately revalidates the bytes it is given.
func (e *Executor) Preflight(ctx context.Context, request, trustedScope []byte) error {
	_, err := e.Prepare(ctx, request, trustedScope)
	return err
}

// SearchPrepared runs the backend ladder for a request this executor's
// Prepare validated and compiled, under the same operation deadline as Search.
func (e *Executor) SearchPrepared(ctx context.Context, prepared Prepared) (Result, error) {
	if e == nil || !e.valid || ctx == nil || !prepared.valid || prepared.executor != e {
		return Result{}, failure(CategoryInvalidInput)
	}
	spec := e.descriptor.Spec()
	if err := ctx.Err(); err != nil {
		return Result{}, contextFailure(err)
	}
	operation, cancel := context.WithTimeout(ctx, time.Duration(spec.Bounds.TimeoutMillis)*time.Millisecond)
	defer cancel()
	return e.execute(operation, spec, prepared.request, prepared.trustedScope, prepared.plan, prepared.filter, prepared.bindings)
}

func (e *Executor) Search(ctx context.Context, request, trustedScope []byte) (Result, error) {
	if e == nil || !e.valid || ctx == nil {
		return Result{}, failure(CategoryInvalidInput)
	}
	spec := e.descriptor.Spec()
	if len(request) == 0 || len(request) > spec.Bounds.MaxRequestBytes ||
		len(trustedScope) == 0 || len(trustedScope) > spec.Bounds.MaxTrustedScopeBytes {
		return Result{}, failure(CategoryInvalidInput)
	}
	if err := ctx.Err(); err != nil {
		return Result{}, contextFailure(err)
	}
	operation, cancel := context.WithTimeout(ctx, time.Duration(spec.Bounds.TimeoutMillis)*time.Millisecond)
	defer cancel()

	plan, err := e.semantics.Compile(request, trustedScope)
	if err != nil {
		return Result{}, failure(CategoryInvalidInput)
	}
	if err := e.descriptor.ValidatePlan(plan); err != nil {
		return Result{}, failure(CategoryInvalidPlan)
	}
	filter, err := e.compileFilter(spec, request, trustedScope)
	if err != nil {
		return Result{}, err
	}
	bindings, err := bindPlan(spec, plan)
	if err != nil {
		return Result{}, err
	}
	return e.execute(operation, spec, request, trustedScope, plan, filter, bindings)
}

// execute runs the version-attempt and pool ladder against the backend for an
// already validated and compiled request.
func (e *Executor) execute(operation context.Context, spec profile.Spec, request, trustedScope []byte, plan adapter.CompiledPlan,
	filter appliedFilter, bindings []probeBinding) (Result, error) {
	// callBackend runs one batch; restart reports a list-identity mismatch
	// (the attempt starts over), err every other failure.
	callBackend := func(wire []kurnhttp.Probe) (batch kurnhttp.BatchResult, restart bool, err error) {
		batch, batchErr := e.backend.Batch(operation, wire)
		if batchErr == nil {
			return batch, false, nil
		}
		if artifactVerificationRefusal(batchErr) {
			return kurnhttp.BatchResult{}, false, failure(CategoryListIdentityMismatch)
		}
		mapped := backendFailure(batchErr)
		if mapped.Category == CategoryListIdentityMismatch {
			return kurnhttp.BatchResult{}, true, nil
		}
		return kurnhttp.BatchResult{}, false, mapped
	}
	fallbackCalls := 0
	for attempt := 1; attempt <= spec.Bounds.VersionAttempts; attempt++ {
		restart := false
		for step, pool := range spec.Bounds.PoolLadder {
			if err := operation.Err(); err != nil {
				return Result{}, contextFailure(err)
			}
			wire, err := buildWireProbes(bindings, filter, pool)
			if err != nil {
				return Result{}, err
			}
			batch, again, err := callBackend(wire)
			if again {
				restart = true
				break
			}
			if err != nil {
				return Result{}, err
			}
			evidence, versions, identityMismatch, err := convertBatch(bindings, plan, batch, pool)
			if identityMismatch {
				restart = true
				break
			}
			if err != nil {
				return Result{}, err
			}
			if err := e.descriptor.ValidateEvidence(plan, evidence); err != nil {
				return Result{}, failure(CategoryInvalidEvidence)
			}
			// The fallback step: a second plan the adapter compiles from the
			// first step's evidence, run under the same pool and filter. Its
			// list versions must be the ones the first step saw, or the
			// attempt restarts exactly as a mid-batch swap would.
			runPlan, runEvidence, backendTook := plan, evidence, batch.TookUS()
			fallbackTaken, fallbackHits := false, 0
			if e.fallback != nil {
				fallbackPlan, taken, ferr := e.fallback.CompileFallback(request, trustedScope, plan, evidence)
				if ferr != nil {
					return Result{}, failure(CategoryAdapterResolution)
				}
				if taken {
					if err := e.descriptor.ValidatePlan(fallbackPlan); err != nil {
						return Result{}, failure(CategoryInvalidPlan)
					}
					fallbackBindings, err := bindPlan(spec, fallbackPlan)
					if err != nil {
						return Result{}, err
					}
					fallbackWire, err := buildWireProbes(fallbackBindings, filter, pool)
					if err != nil {
						return Result{}, err
					}
					fallbackCalls++
					fallbackBatch, again, err := callBackend(fallbackWire)
					if again {
						restart = true
						break
					}
					if err != nil {
						return Result{}, err
					}
					fallbackEvidence, fallbackVersions, identityMismatch, err := convertBatch(fallbackBindings, fallbackPlan, fallbackBatch, pool)
					if identityMismatch || !sameVersions(versions, fallbackVersions) {
						restart = true
						break
					}
					if err != nil {
						return Result{}, err
					}
					if err := e.descriptor.ValidateEvidence(fallbackPlan, fallbackEvidence); err != nil {
						return Result{}, failure(CategoryInvalidEvidence)
					}
					combined, err := adapter.NewCompiledPlan(append(plan.Probes(), fallbackPlan.Probes()...))
					if err != nil {
						return Result{}, failure(CategoryInvalidPlan)
					}
					runPlan = combined
					runEvidence = append(append([]adapter.ProbeEvidence(nil), evidence...), fallbackEvidence...)
					backendTook += fallbackBatch.TookUS()
					fallbackTaken = true
					for _, item := range fallbackEvidence {
						fallbackHits += len(item.Candidates())
					}
				}
			}
			resolution, summary, alternativeSummary, resolveErr := e.resolve(request, trustedScope, runPlan, runEvidence)
			if resolveErr == nil {
				if !resolution.Valid() || !resolution.Complete() {
					return Result{}, failure(CategoryAdapterResolution)
				}
				if summary.Valid() != e.facets || alternativeSummary.Valid() != e.alternatives ||
					(e.facets && e.alternatives) {
					return Result{}, failure(CategoryAdapterResolution)
				}
				identities := e.descriptor.Identities()
				return Result{
					resolution: resolution, summary: summary, alternativeSummary: alternativeSummary, versions: versions,
					adapterName: spec.Name, adapterVersion: spec.Version,
					behaviorSHA256: identities.Behavior.Hex(), versionAttempt: attempt,
					poolStep: step + 1, pool: pool, backendTookUS: backendTook,
					fallbackTaken: fallbackTaken, fallbackHits: fallbackHits, fallbackCalls: fallbackCalls, valid: true,
				}, nil
			}
			if adapter.IsSummaryAtCeiling(resolveErr) {
				// Only the facet constructor's strategy can produce this
				// sentinel, and it maps immediately to the delivered ceiling
				// category: the matched set is already ceiling-sized, so no
				// further rung, batch, retry or partial success is possible.
				return Result{}, failure(CategoryIncompleteAtPoolCeiling)
			}
			if !e.semantics.IsIncomplete(resolveErr) {
				return Result{}, failure(CategoryAdapterResolution)
			}
			if step == len(spec.Bounds.PoolLadder)-1 {
				return Result{}, failure(CategoryIncompleteAtPoolCeiling)
			}
		}
		if !restart {
			break
		}
	}
	return Result{}, failure(CategoryListIdentityMismatch)
}

func validatedFilter(spec profile.Spec, plan adapter.ExactFilterPlan) (kurnhttp.StringFilter, error) {
	if !plan.Valid() {
		return kurnhttp.StringFilter{}, failure(CategoryInvalidFilter)
	}
	trusted := plan.Trusted()
	caller := plan.Caller()
	wantTrusted := make(map[string]struct{}, len(spec.TrustedFilters))
	for _, name := range spec.TrustedFilters {
		wantTrusted[name] = struct{}{}
	}
	seenTrusted := make(map[string]struct{}, len(trusted))
	terms := make([]kurnhttp.FilterTerm, 0, len(trusted)+len(caller))
	for _, term := range trusted {
		if !term.Valid() {
			return kurnhttp.StringFilter{}, failure(CategoryInvalidFilter)
		}
		if _, ok := wantTrusted[term.Name()]; !ok {
			return kurnhttp.StringFilter{}, failure(CategoryInvalidFilter)
		}
		if _, exists := seenTrusted[term.Name()]; exists {
			return kurnhttp.StringFilter{}, failure(CategoryInvalidFilter)
		}
		seenTrusted[term.Name()] = struct{}{}
		terms = append(terms, kurnhttp.FilterTerm{Name: term.Name(), Value: term.Value()})
	}
	if len(seenTrusted) != len(wantTrusted) {
		return kurnhttp.StringFilter{}, failure(CategoryInvalidFilter)
	}
	wantCaller := make(map[string]struct{}, len(spec.CallerFilters))
	for _, name := range spec.CallerFilters {
		wantCaller[name] = struct{}{}
	}
	seenCaller := make(map[string]struct{}, len(caller))
	for _, term := range caller {
		if !term.Valid() {
			return kurnhttp.StringFilter{}, failure(CategoryInvalidFilter)
		}
		if _, ok := wantCaller[term.Name()]; !ok {
			return kurnhttp.StringFilter{}, failure(CategoryInvalidFilter)
		}
		if _, trustedCollision := seenTrusted[term.Name()]; trustedCollision {
			return kurnhttp.StringFilter{}, failure(CategoryInvalidFilter)
		}
		if _, exists := seenCaller[term.Name()]; exists {
			return kurnhttp.StringFilter{}, failure(CategoryInvalidFilter)
		}
		seenCaller[term.Name()] = struct{}{}
		terms = append(terms, kurnhttp.FilterTerm{Name: term.Name(), Value: term.Value()})
	}
	filter, err := kurnhttp.NewStringFilter(terms)
	if err != nil {
		return kurnhttp.StringFilter{}, failure(CategoryInvalidFilter)
	}
	return filter, nil
}

func validatedMembershipFilter(spec profile.Spec, plan adapter.MembershipFilterPlan) (kurnhttp.MembershipFilter, error) {
	if !plan.Valid() {
		return kurnhttp.MembershipFilter{}, failure(CategoryInvalidFilter)
	}
	wantTrusted := make(map[string]bool, len(spec.TrustedFilters))
	for _, name := range spec.TrustedFilters {
		wantTrusted[name] = true
	}
	wantCaller := make(map[string]bool, len(spec.CallerFilters))
	for _, name := range spec.CallerFilters {
		wantCaller[name] = true
	}
	seen := make(map[string]bool)
	trusted := plan.Trusted()
	caller := plan.Caller()
	terms := make([]kurnhttp.MembershipTerm, 0, len(trusted)+len(caller))
	for _, term := range trusted {
		if !term.Valid() || !wantTrusted[term.Name()] || seen[term.Name()] {
			return kurnhttp.MembershipFilter{}, failure(CategoryInvalidFilter)
		}
		seen[term.Name()] = true
		terms = append(terms, kurnhttp.MembershipTerm{Name: term.Name(), Values: []string{term.Value()}})
	}
	if len(seen) != len(wantTrusted) {
		return kurnhttp.MembershipFilter{}, failure(CategoryInvalidFilter)
	}
	for _, term := range caller {
		if !term.Valid() || !wantCaller[term.Name()] || seen[term.Name()] {
			return kurnhttp.MembershipFilter{}, failure(CategoryInvalidFilter)
		}
		seen[term.Name()] = true
		terms = append(terms, kurnhttp.MembershipTerm{Name: term.Name(), Values: term.Values()})
	}
	filter, err := kurnhttp.NewMembershipFilter(terms)
	if err != nil {
		return kurnhttp.MembershipFilter{}, failure(CategoryInvalidFilter)
	}
	return filter, nil
}

func bindPlan(spec profile.Spec, plan adapter.CompiledPlan) ([]probeBinding, error) {
	byRole := make(map[adapter.ListRole]profile.PhysicalListSpec, len(spec.Lists))
	for _, list := range spec.Lists {
		byRole[list.Role] = list
	}
	probes := plan.Probes()
	bindings := make([]probeBinding, len(probes))
	for index, probe := range probes {
		list, ok := byRole[probe.ListRole()]
		if !ok {
			return nil, failure(CategoryInvalidPlan)
		}
		bindings[index] = probeBinding{probe: probe, list: list}
	}
	return bindings, nil
}

// sameVersions reports whether two batches saw the same list versions (by
// role, name and stamp).
func sameVersions(a, b []ListVersion) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func buildWireProbes(bindings []probeBinding, filter appliedFilter, pool int) ([]kurnhttp.Probe, error) {
	wire := make([]kurnhttp.Probe, len(bindings))
	for index, binding := range bindings {
		var threshold *float64
		keys := binding.probe.PhysicalKeys()
		if binding.list.ThresholdMillionths != nil && len(keys) == 0 {
			value := float64(*binding.list.ThresholdMillionths) / 1_000_000
			threshold = &value
		}
		probe, err := kurnhttp.NewProbe(kurnhttp.ProbeSpec{
			Query: binding.probe.PhysicalKey(), ExactAnyOf: keys, List: binding.list.Name,
			Threshold: threshold, TopK: &pool, Filter: filter.exact, Membership: filter.membership,
			ExpectedConfigSHA256: binding.list.ConfigSHA256,
		})
		if err != nil {
			return nil, failure(CategoryInvalidPlan)
		}
		wire[index] = probe
	}
	return wire, nil
}

func convertBatch(bindings []probeBinding, plan adapter.CompiledPlan, batch kurnhttp.BatchResult, pool int) ([]adapter.ProbeEvidence, []ListVersion, bool, error) {
	results := batch.Results()
	probes := plan.Probes()
	if len(results) != len(bindings) || len(probes) != len(bindings) || batch.TookUS() < 0 {
		return nil, nil, false, failure(CategoryInvalidEvidence)
	}
	evidence := make([]adapter.ProbeEvidence, len(results))
	stampByRole := make(map[adapter.ListRole]string)
	listByRole := make(map[adapter.ListRole]string)
	for index, result := range results {
		stamp := result.Stamp()
		parsed, err := kurnhttp.ParseStamp(stamp.Raw)
		if err != nil || parsed.ConfigSHA256 != bindings[index].list.ConfigSHA256 {
			return nil, nil, true, nil
		}
		role := bindings[index].probe.ListRole()
		if previous, exists := stampByRole[role]; exists && previous != stamp.Raw {
			return nil, nil, true, nil
		}
		stampByRole[role] = stamp.Raw
		listByRole[role] = bindings[index].list.Name
		candidates := result.Candidates()
		converted := make([]adapter.Candidate, len(candidates))
		for candidateIndex, candidate := range candidates {
			item, err := adapter.NewCandidate(adapter.CandidateSpec{
				EntryID: candidate.EntryID(), Score: candidate.Score(),
				EchoedKey: candidate.Key(), Payload: candidate.Payload(),
			})
			if err != nil {
				return nil, nil, false, failure(CategoryInvalidEvidence)
			}
			converted[candidateIndex] = item
		}
		item, err := adapter.NewProbeEvidence(adapter.ProbeEvidenceSpec{
			Probe: probes[index], RequestedPool: pool, Candidates: converted,
		})
		if err != nil {
			return nil, nil, false, failure(CategoryInvalidEvidence)
		}
		evidence[index] = item
	}
	roles := make([]adapter.ListRole, 0, len(stampByRole))
	for role := range stampByRole {
		roles = append(roles, role)
	}
	sort.Slice(roles, func(i, j int) bool { return string(roles[i]) < string(roles[j]) })
	versions := make([]ListVersion, len(roles))
	for index, role := range roles {
		versions[index] = ListVersion{role: role, name: listByRole[role], stamp: stampByRole[role]}
	}
	return evidence, versions, false, nil
}

func isCategory(err error, category Category) bool {
	var target *Error
	return errors.As(err, &target) && target.Category == category
}
