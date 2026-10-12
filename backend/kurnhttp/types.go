package kurnhttp

import (
	"encoding/json"
	"math"
	"regexp"
	"unicode/utf8"
)

const (
	maxBatchProbes     = 100
	maxQueryRunes      = 512
	maxTopK            = 1000
	maxExactAnyOfTerms = 64
)

var (
	listNameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	hex64RE    = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

type ProbeSpec struct {
	Query string
	// ExactAnyOf makes the probe an exact any-of lookup (the backend's
	// exact_any_of check member): 1–64 terms, each 1–512 runes, looked up
	// by whole-key equality in the list's exact index. Exactly one of
	// Query and ExactAnyOf is set, and Threshold must be nil with it.
	ExactAnyOf           []string
	List                 string
	Threshold            *float64
	TopK                 *int
	Filter               StringFilter
	Membership           MembershipFilter
	ExpectedConfigSHA256 string
}

type probeFilterMode uint8

const (
	probeUnfiltered probeFilterMode = iota
	probeStringFilter
	probeMembershipFilter
)

// Probe is an immutable validated one-list query check.
type Probe struct {
	query            string
	anyOf            []string
	list             string
	threshold        float64
	hasThreshold     bool
	topK             int
	hasTopK          bool
	filterMode       probeFilterMode
	filterJSON       []byte
	expectedEchoJSON []byte
	expectedConfig   string
	valid            bool
}

func NewProbe(spec ProbeSpec) (Probe, error) {
	if !listNameRE.MatchString(spec.List) || !hex64RE.MatchString(spec.ExpectedConfigSHA256) {
		return Probe{}, classified(CategoryInvalidRequest, 0, nil)
	}
	if len(spec.ExactAnyOf) > 0 {
		if spec.Query != "" || spec.Threshold != nil || len(spec.ExactAnyOf) > maxExactAnyOfTerms {
			return Probe{}, classified(CategoryInvalidRequest, 0, nil)
		}
		for _, term := range spec.ExactAnyOf {
			if !utf8.ValidString(term) || term == "" || utf8.RuneCountInString(term) > maxQueryRunes {
				return Probe{}, classified(CategoryInvalidRequest, 0, nil)
			}
		}
	} else if !utf8.ValidString(spec.Query) || spec.Query == "" ||
		utf8.RuneCountInString(spec.Query) > maxQueryRunes {
		return Probe{}, classified(CategoryInvalidRequest, 0, nil)
	}
	if !spec.Filter.Empty() && !spec.Membership.Empty() {
		return Probe{}, classified(CategoryInvalidRequest, 0, nil)
	}
	p := Probe{query: spec.Query, anyOf: append([]string(nil), spec.ExactAnyOf...), list: spec.List,
		expectedConfig: spec.ExpectedConfigSHA256, valid: true}
	if !spec.Filter.Empty() {
		p.filterMode, p.filterJSON = probeStringFilter, spec.Filter.CanonicalJSON()
	} else if !spec.Membership.Empty() {
		p.filterMode, p.filterJSON = probeMembershipFilter, spec.Membership.CanonicalJSON()
	}
	if p.filterMode != probeUnfiltered {
		// The fixed public response encoder serializes engine-canonical P as
		// a RawMessage with its default escaping. Snapshot that exact E(P)
		// from trusted P, separately from the unchanged request bytes.
		var err error
		p.expectedEchoJSON, err = json.Marshal(json.RawMessage(p.filterJSON))
		if err != nil {
			return Probe{}, classified(CategoryInvalidRequest, 0, nil)
		}
	}
	if spec.Threshold != nil {
		if math.IsNaN(*spec.Threshold) || math.IsInf(*spec.Threshold, 0) ||
			*spec.Threshold < 0 || *spec.Threshold > 1 {
			return Probe{}, classified(CategoryInvalidRequest, 0, nil)
		}
		p.threshold, p.hasThreshold = *spec.Threshold, true
	}
	if spec.TopK != nil {
		if *spec.TopK < 1 || *spec.TopK > maxTopK {
			return Probe{}, classified(CategoryInvalidRequest, 0, nil)
		}
		p.topK, p.hasTopK = *spec.TopK, true
	}
	return p, nil
}

// ExactAnyOf returns the probe's any-of terms (nil for an ordinary query).
func (p Probe) ExactAnyOf() []string { return append([]string(nil), p.anyOf...) }

type Candidate struct {
	list    string
	entryID string
	score   float64
	key     string
	payload json.RawMessage
}

func (c Candidate) List() string             { return c.list }
func (c Candidate) EntryID() string          { return c.entryID }
func (c Candidate) Score() float64           { return c.score }
func (c Candidate) Key() string              { return c.key }
func (c Candidate) Payload() json.RawMessage { return append(json.RawMessage(nil), c.payload...) }

type FilterStats struct {
	Evaluated int64
	Rejected  int64
}

type ProbeResult struct {
	candidates     []Candidate
	stamp          Stamp
	appliedFilter  json.RawMessage
	filterStats    FilterStats
	hasFilterStats bool
	tookUS         int64
}

func (r ProbeResult) Candidates() []Candidate {
	result := make([]Candidate, len(r.candidates))
	copy(result, r.candidates)
	return result
}
func (r ProbeResult) Stamp() Stamp { return r.stamp }

// AppliedFilter returns a copy of the actual raw transport echo, not a
// normalized predicate. In either filtered mode this may differ from the
// engine-canonical request spelling because of the response JSON encoder.
func (r ProbeResult) AppliedFilter() json.RawMessage {
	return append(json.RawMessage(nil), r.appliedFilter...)
}
func (r ProbeResult) FilterStats() (FilterStats, bool) {
	return r.filterStats, r.hasFilterStats
}
func (r ProbeResult) TookUS() int64 { return r.tookUS }

type BatchResult struct {
	results []ProbeResult
	tookUS  int64
}

func (r BatchResult) Results() []ProbeResult {
	result := make([]ProbeResult, len(r.results))
	copy(result, r.results)
	return result
}
func (r BatchResult) TookUS() int64 { return r.tookUS }
