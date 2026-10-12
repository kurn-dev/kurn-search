package adapter

import (
	"errors"
	"unicode/utf8"

	"github.com/kurn-dev/kurn-search/internal/jsorder"
)

// Summary bounds. A summary describes a complete matched set whose size is
// already bounded below MaxHits by the calling route's ceiling refusal, so
// every value here is a consequence of that domain, not a caller input.
const (
	// MaxSummaryTotal is the largest exact matched-set size a summary can
	// describe: one below the hits ceiling.
	MaxSummaryTotal = MaxHits - 1
	// MaxSummaryGroups bounds the named groups in one summary.
	MaxSummaryGroups = 8
	// MaxSummaryGroupRunes bounds one group name in Unicode scalar values.
	MaxSummaryGroupRunes = 64
	// MaxSummaryBuckets bounds the distinct values within one group.
	MaxSummaryBuckets = MaxSummaryTotal
	// MaxSummaryValueRunes bounds one bucket value in Unicode scalar values.
	MaxSummaryValueRunes = 512
	// MaxSummaryEncodedBytes is the conservative ceiling on one compact JSON
	// encoding of a summary. Derivation under the worst six-bytes-per-scalar
	// escape rule: a value costs at most 6*512+2 = 3,074 bytes; a
	// {"count":999,"value":…} bucket at most 9+3+9+3074+1 = 3,096; a group
	// with a 64-scalar name at most 388+999*3096+998+1 = 3,094,291; eight
	// groups with separators plus the total_matches/facets envelope cost at
	// most 8*3094291+7+32 = 24,754,367.
	MaxSummaryEncodedBytes = 24_754_367
)

// ErrSummaryAtCeiling is the typed sentinel a summary resolver returns exactly
// when the complete matched set reaches the hits ceiling, making the exact
// total unprovable. It is never produced by ordinary resolution and carries no
// caller or artifact material.
var ErrSummaryAtCeiling = errors.New("adapter: summary_at_hits_ceiling")

// IsSummaryAtCeiling reports whether err is the summary-at-ceiling sentinel.
func IsSummaryAtCeiling(err error) bool { return errors.Is(err, ErrSummaryAtCeiling) }

// SummaryResolver is the closed summary-resolution seam. Only facet-style
// adapters implement it; it is deliberately separate from Resolver.
type SummaryResolver interface {
	ResolveWithSummary(request, trustedScope []byte, plan CompiledPlan, evidence []ProbeEvidence) (Resolution, Summary, error)
}

// SummaryBucket is one immutable value/count pair.
type SummaryBucket struct {
	count int
	value string
}

func (b SummaryBucket) Count() int    { return b.count }
func (b SummaryBucket) Value() string { return b.value }

// SummaryGroup is one immutable named, canonically ordered bucket sequence.
type SummaryGroup struct {
	name    string
	buckets []SummaryBucket
}

func (g SummaryGroup) Name() string { return g.name }

// Buckets returns a defensive copy of the group's ordered buckets.
func (g SummaryGroup) Buckets() []SummaryBucket { return append([]SummaryBucket(nil), g.buckets...) }

// SummarySpec is the construction input for a Summary. Groups must already be
// in canonical JavaScript UTF-16 name order, and each group's buckets must
// already be ordered by count descending then value in UTF-16 order.
type SummarySpec struct {
	TotalMatches int
	Groups       []SummaryGroupSpec
}

// SummaryGroupSpec is the construction input for one group.
type SummaryGroupSpec struct {
	Name    string
	Buckets []SummaryBucketSpec
}

// SummaryBucketSpec is the construction input for one bucket.
type SummaryBucketSpec struct {
	Count int
	Value string
}

// Summary is an immutable, domain-neutral, validated matched-set summary.
type Summary struct {
	total  int
	groups []SummaryGroup
	valid  bool
}

// NewSummary validates and freezes a matched-set summary. The contract is
// domain-neutral: it enforces finite bounds, uniqueness, canonical group-name
// order, bucket order, the per-group count-sum invariant and a conservative
// encoded-size ceiling without interpreting any group name.
func NewSummary(spec SummarySpec) (Summary, error) {
	if spec.TotalMatches < 0 || spec.TotalMatches > MaxSummaryTotal ||
		len(spec.Groups) < 1 || len(spec.Groups) > MaxSummaryGroups {
		return Summary{}, invalidContract()
	}
	groups := make([]SummaryGroup, len(spec.Groups))
	size := len(`{"total_matches":999,"facets":{}}`) + len(spec.Groups) - 1
	previous := ""
	for index, groupSpec := range spec.Groups {
		if !validBoundedString(groupSpec.Name, MaxSummaryGroupRunes) {
			return Summary{}, invalidContract()
		}
		if index > 0 && !jsorder.Less(previous, groupSpec.Name) {
			return Summary{}, invalidContract()
		}
		previous = groupSpec.Name
		if len(groupSpec.Buckets) > MaxSummaryBuckets {
			return Summary{}, invalidContract()
		}
		buckets := make([]SummaryBucket, len(groupSpec.Buckets))
		sum := 0
		lastCount, lastValue := MaxSummaryTotal+1, ""
		seen := make(map[string]struct{}, len(groupSpec.Buckets))
		for bucketIndex, bucketSpec := range groupSpec.Buckets {
			if bucketSpec.Count < 1 || bucketSpec.Count > MaxSummaryTotal ||
				!validBoundedString(bucketSpec.Value, MaxSummaryValueRunes) {
				return Summary{}, invalidContract()
			}
			if _, exists := seen[bucketSpec.Value]; exists {
				return Summary{}, invalidContract()
			}
			seen[bucketSpec.Value] = struct{}{}
			if bucketIndex > 0 && (bucketSpec.Count > lastCount ||
				(bucketSpec.Count == lastCount && !jsorder.Less(lastValue, bucketSpec.Value))) {
				return Summary{}, invalidContract()
			}
			lastCount, lastValue = bucketSpec.Count, bucketSpec.Value
			sum += bucketSpec.Count
			buckets[bucketIndex] = SummaryBucket{count: bucketSpec.Count, value: bucketSpec.Value}
		}
		if sum != spec.TotalMatches {
			return Summary{}, invalidContract()
		}
		size += 6*utf8.RuneCountInString(groupSpec.Name) + 5 // escaped `"name":[` and `]`
		for _, bucket := range buckets {
			// Conservative upper bound under the compact encoder: six bytes per
			// scalar for the value, plus the fixed {"count":999,"value":""}
			// bucket members and one separator.
			size += 24 + 1 + 6*utf8.RuneCountInString(bucket.value)
		}
		groups[index] = SummaryGroup{name: groupSpec.Name, buckets: buckets}
	}
	if size > MaxSummaryEncodedBytes {
		return Summary{}, invalidContract()
	}
	return Summary{total: spec.TotalMatches, groups: groups, valid: true}, nil
}

// TotalMatches returns the exact complete matched-set size.
func (s Summary) TotalMatches() int { return s.total }

// Groups returns a deep defensive copy of the canonically ordered groups.
func (s Summary) Groups() []SummaryGroup {
	out := make([]SummaryGroup, len(s.groups))
	for index, group := range s.groups {
		out[index] = SummaryGroup{name: group.name, buckets: append([]SummaryBucket(nil), group.buckets...)}
	}
	return out
}

// Valid reports whether the summary passed construction validation.
func (s Summary) Valid() bool { return s.valid }
