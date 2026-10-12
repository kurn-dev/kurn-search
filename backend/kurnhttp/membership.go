package kurnhttp

import (
	"bytes"
	"slices"

	"github.com/kurn-dev/kurn-search/internal/membershipjson"
)

const (
	MaxMembershipAlternatives = membershipjson.MaxAlternatives
	MaxMembershipFilterBytes  = membershipjson.MaxCanonicalBytes
)

// MembershipTerm is supplied string-membership input. NewMembershipFilter
// validates and copies it; callers cannot mutate a constructed predicate.
type MembershipTerm struct {
	Name   string
	Values []string
}

// MembershipFilter is a bounded immutable expression, separate from the
// delivered StringFilter. Its zero value means no applied filter.
type MembershipFilter struct {
	terms     []MembershipTerm
	canonical []byte
}

func NewMembershipFilter(input []MembershipTerm) (MembershipFilter, error) {
	if len(input) > maxFilterTerms {
		return MembershipFilter{}, classified(CategoryInvalidRequest, 0, nil)
	}
	terms := make([]membershipjson.Term, len(input))
	for i, term := range input {
		terms[i] = membershipjson.Term{Name: term.Name, Values: term.Values}
	}
	normalized, canonical, err := membershipjson.Normalize(terms)
	if err != nil {
		return MembershipFilter{}, classified(CategoryInvalidRequest, 0, nil)
	}
	owned := make([]MembershipTerm, len(normalized))
	for i, term := range normalized {
		owned[i] = MembershipTerm{Name: term.Name, Values: term.Values}
	}
	return MembershipFilter{terms: owned, canonical: canonical}, nil
}

func (f MembershipFilter) Empty() bool           { return len(f.terms) == 0 }
func (f MembershipFilter) CanonicalJSON() []byte { return bytes.Clone(f.canonical) }
func (f MembershipFilter) Terms() []MembershipTerm {
	terms := slices.Clone(f.terms)
	for i := range terms {
		terms[i].Values = slices.Clone(terms[i].Values)
	}
	return terms
}
