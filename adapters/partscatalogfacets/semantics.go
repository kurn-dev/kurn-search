// Package partscatalogfacets implements the closed W7-S1 faceted-discovery
// semantics: the delivered parts-catalog-discover matched set, unchanged, plus
// exact manufacturer/category/status counts computed over that complete set
// before caller limiting. The package delegates all plan, filter and
// incompleteness behavior to the byte-unchanged discover adapter and adds only
// the summary resolution seam.
package partscatalogfacets

import (
	"github.com/kurn-dev/kurn-search/adapter"
	"github.com/kurn-dev/kurn-search/adapters/partscatalogdiscover"
)

// Semantics is the closed facet adapter. It deliberately has no Resolve
// method, so it cannot be wired into the delivered ordinary or membership
// executors; only the facet constructor accepts it.
type Semantics struct {
	delegate partscatalogdiscover.Semantics
}

// New returns the facet semantics bound to the delivered discover delegate.
func New() Semantics { return Semantics{delegate: partscatalogdiscover.New()} }

// Project delegates source projection unchanged: the physical list built for
// this route is byte-identical to the delivered discover list.
func (s Semantics) Project(source []byte) ([]adapter.ProjectedEntry, error) {
	return s.delegate.Project(source)
}

// Compile delegates on the caller's original bytes, unchanged.
func (s Semantics) Compile(requestBytes, scopeBytes []byte) (adapter.CompiledPlan, error) {
	return s.delegate.Compile(requestBytes, scopeBytes)
}

// CompileMembershipFilter delegates on the caller's original bytes, unchanged.
func (s Semantics) CompileMembershipFilter(requestBytes, scopeBytes []byte) (adapter.MembershipFilterPlan, error) {
	return s.delegate.CompileMembershipFilter(requestBytes, scopeBytes)
}

// IsIncomplete delegates the delivered pool-escalation classification.
func (s Semantics) IsIncomplete(err error) bool { return s.delegate.IsIncomplete(err) }

// ResolveWithSummary resolves the caller's request through the delivered
// discover resolution with the limit substituted to the hits ceiling, computes
// the exact summary over the complete validated ordered matched set, and only
// then applies the caller's limit to the hit prefix. The delegate's plan and
// evidence are passed through unchanged; its errors pass through with their
// delivered classification. A delegate result of exactly MaxHits hits cannot
// prove the exact total and fails with adapter.ErrSummaryAtCeiling.
func (s Semantics) ResolveWithSummary(requestBytes, scopeBytes []byte, plan adapter.CompiledPlan, observed []adapter.ProbeEvidence) (adapter.Resolution, adapter.Summary, error) {
	view, err := parseRequestView(requestBytes)
	if err != nil {
		return adapter.Resolution{}, adapter.Summary{}, err
	}
	resolution, err := s.delegate.Resolve(substituteLimit(view), scopeBytes, plan, observed)
	if err != nil {
		return adapter.Resolution{}, adapter.Summary{}, err
	}
	if !resolution.Valid() || !resolution.Complete() {
		return adapter.Resolution{}, adapter.Summary{}, invalidSummary()
	}
	hits := resolution.Hits()
	if len(hits) == adapter.MaxHits {
		return adapter.Resolution{}, adapter.Summary{}, adapter.ErrSummaryAtCeiling
	}
	summary, err := summarizeHits(hits)
	if err != nil {
		return adapter.Resolution{}, adapter.Summary{}, err
	}
	if view.limit < len(hits) {
		hits = hits[:view.limit]
	}
	limited, err := adapter.NewResolution(adapter.ResolutionSpec{Complete: true, Hits: hits})
	if err != nil {
		return adapter.Resolution{}, adapter.Summary{}, invalidSummary()
	}
	return limited, summary, nil
}
