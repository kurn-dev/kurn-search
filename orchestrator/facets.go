package orchestrator

import (
	"github.com/kurn-dev/kurn-search/adapter"
	"github.com/kurn-dev/kurn-search/profile"
)

// FacetRuntimeSemantics is the closed facet execution strategy: the ordinary
// plan compiler, membership-filter compiler and incompleteness classifier,
// plus the summary resolver. It is deliberately separate from
// RuntimeSemantics: a facet adapter has no ordinary Resolve method.
type FacetRuntimeSemantics interface {
	adapter.Compiler
	adapter.MembershipFilterCompiler
	adapter.IncompleteClassifier
	adapter.SummaryResolver
}

// NewWithFacets fixes the facet strategy once at construction, without
// optional interface probing or a non-summary fallback. The composition layer
// supplies the concrete facet adapter; this package imports none.
func NewWithFacets(descriptor profile.Descriptor, semantics FacetRuntimeSemantics, backend Backend) (*Executor, error) {
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
		resolution, summary, err := semantics.ResolveWithSummary(request, scope, plan, evidence)
		return resolution, summary, adapter.AlternativeSummary{}, err
	}
	return &Executor{
		descriptor: descriptor, semantics: semantics, compileFilter: strategy,
		resolve: resolve, backend: backend, facets: true, valid: true,
	}, nil
}

// Facets reports whether the executor was facet-constructed. The service uses
// it to bind facet runners explicitly; there is no way to toggle it after
// construction.
func (e *Executor) Facets() bool { return e != nil && e.facets }
