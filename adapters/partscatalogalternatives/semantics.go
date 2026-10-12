// Package partscatalogalternatives implements one complete, self-excluding
// facet view over the delivered parts-discovery query and physical list.
package partscatalogalternatives

import (
	"encoding/json"
	"errors"
	"slices"
	"sort"

	"github.com/kurn-dev/kurn-search/adapter"
	"github.com/kurn-dev/kurn-search/adapters/partscatalogdiscover"
	"github.com/kurn-dev/kurn-search/internal/jsorder"
)

type Semantics struct {
	delegate partscatalogdiscover.Semantics
}

func New() Semantics { return Semantics{delegate: partscatalogdiscover.New()} }

func (s Semantics) Project(source []byte) ([]adapter.ProjectedEntry, error) {
	return s.delegate.Project(source)
}

// Compile validates the original closed request and leaves query probes
// unchanged; only the backend membership filter is broadened for this route.
func (s Semantics) Compile(request, scope []byte) (adapter.CompiledPlan, error) {
	return s.delegate.Compile(request, scope)
}

func (s Semantics) CompileMembershipFilter(request, scope []byte) (adapter.MembershipFilterPlan, error) {
	if _, err := s.delegate.Compile(request, scope); err != nil {
		return adapter.MembershipFilterPlan{}, err
	}
	view, err := parseRequestView(request)
	if err != nil {
		return adapter.MembershipFilterPlan{}, err
	}
	return s.delegate.CompileMembershipFilter(view.superset, scope)
}

func (s Semantics) IsIncomplete(err error) bool { return s.delegate.IsIncomplete(err) }

// ResolveWithAlternativeSummary validates and orders one complete unfiltered
// superset, then applies the caller's selections to hits and each group's
// two-other-field basis before limiting the hit prefix.
func (s Semantics) ResolveWithAlternativeSummary(request, scope []byte, plan adapter.CompiledPlan, observed []adapter.ProbeEvidence) (adapter.Resolution, adapter.AlternativeSummary, error) {
	original, err := s.delegate.Compile(request, scope)
	if err != nil || !samePlan(original, plan) {
		return adapter.Resolution{}, adapter.AlternativeSummary{}, invalidInput()
	}
	view, err := parseRequestView(request)
	if err != nil {
		return adapter.Resolution{}, adapter.AlternativeSummary{}, err
	}
	supersetPlan, err := s.delegate.Compile(view.superset, scope)
	if err != nil || !samePlan(plan, supersetPlan) {
		return adapter.Resolution{}, adapter.AlternativeSummary{}, invalidInput()
	}
	complete, err := s.delegate.Resolve(view.superset, scope, plan, observed)
	if err != nil {
		return adapter.Resolution{}, adapter.AlternativeSummary{}, err
	}
	if !complete.Valid() || !complete.Complete() {
		return adapter.Resolution{}, adapter.AlternativeSummary{}, invalidSummary()
	}
	hits := complete.Hits()
	if len(hits) == adapter.MaxHits {
		return adapter.Resolution{}, adapter.AlternativeSummary{}, adapter.ErrSummaryAtCeiling
	}
	fields := []string{"category", "manufacturer", "status"}
	counts := []map[string]int{{}, {}, {}}
	bases := make([]int, 3)
	selected := make([]adapter.ResolvedHit, 0, len(hits))
	for _, hit := range hits {
		if !hit.Valid() {
			return adapter.Resolution{}, adapter.AlternativeSummary{}, invalidSummary()
		}
		var record struct {
			Category     string `json:"category"`
			Manufacturer string `json:"manufacturer"`
			Status       string `json:"status"`
		}
		if err := json.Unmarshal(hit.Result(), &record); err != nil || record.Category == "" ||
			record.Manufacturer == "" || (record.Status != "active" && record.Status != "discontinued") {
			return adapter.Resolution{}, adapter.AlternativeSummary{}, invalidSummary()
		}
		values := []string{record.Category, record.Manufacturer, record.Status}
		matches := [3]bool{}
		for index, field := range fields {
			selection, present := view.selections[field]
			matches[index] = !present || slices.Contains(selection, values[index])
		}
		if matches[0] && matches[1] && matches[2] {
			selected = append(selected, hit)
		}
		for index := range fields {
			if matches[(index+1)%3] && matches[(index+2)%3] {
				bases[index]++
				counts[index][values[index]]++
			}
		}
	}
	groups := make([]adapter.AlternativeSummaryGroupSpec, 3)
	for index, field := range fields {
		values := make([]string, 0, len(counts[index]))
		for value := range counts[index] {
			values = append(values, value)
		}
		sort.Slice(values, func(i, j int) bool {
			left, right := counts[index][values[i]], counts[index][values[j]]
			if left != right {
				return left > right
			}
			return jsorder.Less(values[i], values[j])
		})
		buckets := make([]adapter.SummaryBucketSpec, len(values))
		for position, value := range values {
			buckets[position] = adapter.SummaryBucketSpec{Count: counts[index][value], Value: value}
		}
		groups[index] = adapter.AlternativeSummaryGroupSpec{
			Name: field, BasisTotal: bases[index], Buckets: buckets,
		}
	}
	summary, err := adapter.NewAlternativeSummary(adapter.AlternativeSummarySpec{
		TotalMatches: len(selected), Groups: groups,
	})
	if err != nil {
		return adapter.Resolution{}, adapter.AlternativeSummary{}, invalidSummary()
	}
	if view.limit < len(selected) {
		selected = selected[:view.limit]
	}
	result, err := adapter.NewResolution(adapter.ResolutionSpec{Complete: true, Hits: selected})
	if err != nil {
		return adapter.Resolution{}, adapter.AlternativeSummary{}, invalidSummary()
	}
	return result, summary, nil
}

func samePlan(left, right adapter.CompiledPlan) bool {
	if !left.Valid() || !right.Valid() {
		return false
	}
	a, b := left.Probes(), right.Probes()
	return slices.EqualFunc(a, b, func(x, y adapter.Probe) bool {
		return x.Valid() && y.Valid() && x.Kind() == y.Kind() &&
			x.LogicalTerm() == y.LogicalTerm() && x.PhysicalKey() == y.PhysicalKey() && x.ListRole() == y.ListRole()
	})
}

func invalidInput() error   { return errors.New("partscatalogalternatives: invalid input") }
func invalidSummary() error { return errors.New("partscatalogalternatives: invalid summary") }
