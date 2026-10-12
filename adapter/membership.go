package adapter

import (
	"bytes"
	"slices"
	"sort"

	"github.com/kurn-dev/kurn-search/internal/membershipjson"
)

const (
	MaxMembershipAlternatives = membershipjson.MaxAlternatives
	MaxMembershipFilterBytes  = membershipjson.MaxCanonicalBytes
)

// MembershipFilterTerm is an immutable string set. A scalar is represented
// by one member; supplied count and member bounds are checked before dedupe.
type MembershipFilterTerm struct {
	name   string
	values []string
	valid  bool
}

func NewMembershipFilterTerm(name string, values []string) (MembershipFilterTerm, error) {
	terms, _, err := membershipjson.Normalize([]membershipjson.Term{{Name: name, Values: values}})
	if err != nil {
		return MembershipFilterTerm{}, invalidContract()
	}
	return MembershipFilterTerm{name: terms[0].Name, values: terms[0].Values, valid: true}, nil
}

func (t MembershipFilterTerm) Name() string     { return t.name }
func (t MembershipFilterTerm) Values() []string { return slices.Clone(t.values) }
func (t MembershipFilterTerm) Valid() bool      { return t.valid }

type MembershipFilterPlanSpec struct {
	Trusted []ExactFilterTerm
	Caller  []MembershipFilterTerm
}

// MembershipFilterPlan preserves provenance until the executor validates the
// descriptor vocabulary. Trusted terms cannot become membership alternatives.
// A constructed empty plan is valid; its zero value is invalid.
type MembershipFilterPlan struct {
	trusted   []ExactFilterTerm
	caller    []MembershipFilterTerm
	canonical []byte
	valid     bool
}

func NewMembershipFilterPlan(spec MembershipFilterPlanSpec) (MembershipFilterPlan, error) {
	if len(spec.Trusted)+len(spec.Caller) > MaxExactFilterTerms {
		return MembershipFilterPlan{}, invalidContract()
	}
	trusted := slices.Clone(spec.Trusted)
	caller := cloneMembershipTerms(spec.Caller)
	terms := make([]membershipjson.Term, 0, len(trusted)+len(caller))
	for _, term := range trusted {
		if !term.valid {
			return MembershipFilterPlan{}, invalidContract()
		}
		terms = append(terms, membershipjson.Term{Name: term.name, Values: []string{term.value}})
	}
	for _, term := range caller {
		if !term.valid {
			return MembershipFilterPlan{}, invalidContract()
		}
		terms = append(terms, membershipjson.Term{Name: term.name, Values: term.values})
	}
	_, canonical, err := membershipjson.Normalize(terms)
	if err != nil {
		return MembershipFilterPlan{}, invalidContract()
	}
	sort.Slice(trusted, func(i, j int) bool { return trusted[i].name < trusted[j].name })
	sort.Slice(caller, func(i, j int) bool { return caller[i].name < caller[j].name })
	return MembershipFilterPlan{trusted: trusted, caller: caller, canonical: canonical, valid: true}, nil
}

func (p MembershipFilterPlan) Trusted() []ExactFilterTerm { return slices.Clone(p.trusted) }
func (p MembershipFilterPlan) Caller() []MembershipFilterTerm {
	return cloneMembershipTerms(p.caller)
}
func (p MembershipFilterPlan) CanonicalJSON() []byte { return bytes.Clone(p.canonical) }
func (p MembershipFilterPlan) Valid() bool           { return p.valid }

func cloneMembershipTerms(terms []MembershipFilterTerm) []MembershipFilterTerm {
	copyTerms := slices.Clone(terms)
	for i := range copyTerms {
		copyTerms[i].values = slices.Clone(copyTerms[i].values)
	}
	return copyTerms
}

// MembershipFilterCompiler is separate from the legacy exact-string seam.
// New executors explicitly select one compiler strategy at construction.
type MembershipFilterCompiler interface {
	CompileMembershipFilter(request, trustedScope []byte) (MembershipFilterPlan, error)
}
