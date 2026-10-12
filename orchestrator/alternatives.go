package orchestrator

import (
	"github.com/kurn-dev/kurn-search/adapter"
	"github.com/kurn-dev/kurn-search/profile"
)

// AlternativeRuntimeSemantics is the explicit third execution strategy. The
// compiler validates the original request; the membership compiler derives
// the one complete superset plan from those same bytes.
type AlternativeRuntimeSemantics interface {
	adapter.Compiler
	adapter.MembershipFilterCompiler
	adapter.IncompleteClassifier
	adapter.AlternativeSummaryResolver
}

// NewWithAlternatives fixes the superset filter and alternative-summary
// resolver once, with no optional interface or ordinary/facet fallback.
func NewWithAlternatives(descriptor profile.Descriptor, semantics AlternativeRuntimeSemantics, backend Backend) (*Executor, error) {
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
		resolution, summary, err := semantics.ResolveWithAlternativeSummary(request, scope, plan, evidence)
		return resolution, adapter.Summary{}, summary, err
	}
	return &Executor{
		descriptor: descriptor, semantics: semantics, compileFilter: strategy,
		resolve: resolve, backend: backend, alternatives: true, valid: true,
	}, nil
}

// Alternatives reports construction-time strategy, not a runtime guess.
func (e *Executor) Alternatives() bool { return e != nil && e.alternatives }
