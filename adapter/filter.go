package adapter

import (
	"sort"
	"unicode/utf8"
)

const (
	MaxExactFilterTerms      = 8
	MaxExactFilterNameRunes  = 128
	MaxExactFilterValueRunes = 512
)

// ExactFilterTerm is one immutable transport-neutral exact-string filter.
type ExactFilterTerm struct {
	name  string
	value string
	valid bool
}

func NewExactFilterTerm(name, value string) (ExactFilterTerm, error) {
	if !utf8.ValidString(name) || !utf8.ValidString(value) || name == "" || value == "" ||
		utf8.RuneCountInString(name) > MaxExactFilterNameRunes ||
		utf8.RuneCountInString(value) > MaxExactFilterValueRunes {
		return ExactFilterTerm{}, invalidContract()
	}
	return ExactFilterTerm{name: name, value: value, valid: true}, nil
}

func (t ExactFilterTerm) Name() string  { return t.name }
func (t ExactFilterTerm) Value() string { return t.value }
func (t ExactFilterTerm) Valid() bool   { return t.valid }

type ExactFilterPlanSpec struct {
	Trusted []ExactFilterTerm
	Caller  []ExactFilterTerm
}

// ExactFilterPlan keeps trusted and caller-controlled filter provenance
// separate until an orchestrator validates it against a closed profile.
type ExactFilterPlan struct {
	trusted []ExactFilterTerm
	caller  []ExactFilterTerm
	valid   bool
}

func NewExactFilterPlan(spec ExactFilterPlanSpec) (ExactFilterPlan, error) {
	if len(spec.Trusted)+len(spec.Caller) > MaxExactFilterTerms {
		return ExactFilterPlan{}, invalidContract()
	}
	trusted := append([]ExactFilterTerm(nil), spec.Trusted...)
	caller := append([]ExactFilterTerm(nil), spec.Caller...)
	seen := make(map[string]struct{}, len(trusted)+len(caller))
	for _, group := range [][]ExactFilterTerm{trusted, caller} {
		for _, term := range group {
			if !term.valid {
				return ExactFilterPlan{}, invalidContract()
			}
			if _, exists := seen[term.name]; exists {
				return ExactFilterPlan{}, invalidContract()
			}
			seen[term.name] = struct{}{}
		}
	}
	sort.Slice(trusted, func(i, j int) bool { return trusted[i].name < trusted[j].name })
	sort.Slice(caller, func(i, j int) bool { return caller[i].name < caller[j].name })
	return ExactFilterPlan{trusted: trusted, caller: caller, valid: true}, nil
}

func (p ExactFilterPlan) Trusted() []ExactFilterTerm {
	return append([]ExactFilterTerm(nil), p.trusted...)
}

func (p ExactFilterPlan) Caller() []ExactFilterTerm {
	return append([]ExactFilterTerm(nil), p.caller...)
}

func (p ExactFilterPlan) Valid() bool { return p.valid }

// FilterCompiler is the pure adapter-owned trusted/caller filter seam.
type FilterCompiler interface {
	CompileFilter(request, trustedScope []byte) (ExactFilterPlan, error)
}

// IncompleteClassifier identifies the adapter's sole pool-escalation signal.
type IncompleteClassifier interface {
	IsIncomplete(error) bool
}
