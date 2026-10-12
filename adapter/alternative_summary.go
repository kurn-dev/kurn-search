package adapter

import (
	"unicode/utf8"

	"github.com/kurn-dev/kurn-search/internal/jsorder"
)

// MaxAlternativeSummaryEncodedBytes is the closed three-group compact JSON
// ceiling. It is the accepted W7-S1 bound plus 30 bytes of basis metadata for
// each of the three groups. The complete HTTP response has a separate cap.
const MaxAlternativeSummaryEncodedBytes = 9_284_922

// AlternativeSummaryResolver returns a complete filtered resolution together
// with group-specific, self-excluding facet bases. It is deliberately distinct
// from SummaryResolver: a Summary requires every group to share one total.
type AlternativeSummaryResolver interface {
	ResolveWithAlternativeSummary(request, trustedScope []byte, plan CompiledPlan, evidence []ProbeEvidence) (Resolution, AlternativeSummary, error)
}

type AlternativeSummaryGroupSpec struct {
	Name       string
	BasisTotal int
	Buckets    []SummaryBucketSpec
}

type AlternativeSummarySpec struct {
	TotalMatches int
	Groups       []AlternativeSummaryGroupSpec
}

// AlternativeSummaryGroup is an immutable named, canonically ordered bucket
// sequence with its own complete basis total.
type AlternativeSummaryGroup struct {
	name       string
	basisTotal int
	buckets    []SummaryBucket
}

func (g AlternativeSummaryGroup) Name() string    { return g.name }
func (g AlternativeSummaryGroup) BasisTotal() int { return g.basisTotal }
func (g AlternativeSummaryGroup) Buckets() []SummaryBucket {
	return append([]SummaryBucket(nil), g.buckets...)
}

// AlternativeSummary is a closed three-group, domain-neutral summary. Route
// semantics additionally bind the exact three field names and source values.
type AlternativeSummary struct {
	total  int
	groups []AlternativeSummaryGroup
	valid  bool
}

func NewAlternativeSummary(spec AlternativeSummarySpec) (AlternativeSummary, error) {
	if spec.TotalMatches < 0 || spec.TotalMatches > MaxSummaryTotal || len(spec.Groups) != 3 {
		return AlternativeSummary{}, invalidContract()
	}
	groups := make([]AlternativeSummaryGroup, len(spec.Groups))
	size := len(`{"total_matches":999,"facets":{}}`) + len(spec.Groups) - 1
	previous := ""
	for index, groupSpec := range spec.Groups {
		if !validBoundedString(groupSpec.Name, MaxSummaryGroupRunes) ||
			groupSpec.BasisTotal < spec.TotalMatches || groupSpec.BasisTotal > MaxSummaryTotal ||
			(index > 0 && !jsorder.Less(previous, groupSpec.Name)) ||
			len(groupSpec.Buckets) > MaxSummaryBuckets {
			return AlternativeSummary{}, invalidContract()
		}
		previous = groupSpec.Name
		buckets := make([]SummaryBucket, len(groupSpec.Buckets))
		sum := 0
		lastCount, lastValue := MaxSummaryTotal+1, ""
		seen := make(map[string]struct{}, len(groupSpec.Buckets))
		for bucketIndex, bucketSpec := range groupSpec.Buckets {
			if bucketSpec.Count < 1 || bucketSpec.Count > MaxSummaryTotal ||
				!validBoundedString(bucketSpec.Value, MaxSummaryValueRunes) {
				return AlternativeSummary{}, invalidContract()
			}
			if _, exists := seen[bucketSpec.Value]; exists {
				return AlternativeSummary{}, invalidContract()
			}
			seen[bucketSpec.Value] = struct{}{}
			if bucketIndex > 0 && (bucketSpec.Count > lastCount ||
				(bucketSpec.Count == lastCount && !jsorder.Less(lastValue, bucketSpec.Value))) {
				return AlternativeSummary{}, invalidContract()
			}
			lastCount, lastValue = bucketSpec.Count, bucketSpec.Value
			sum += bucketSpec.Count
			if sum > MaxSummaryTotal {
				return AlternativeSummary{}, invalidContract()
			}
			buckets[bucketIndex] = SummaryBucket{count: bucketSpec.Count, value: bucketSpec.Value}
		}
		if sum != groupSpec.BasisTotal {
			return AlternativeSummary{}, invalidContract()
		}
		// The delivered Summary bound counts a bare group array. Add the
		// 30-byte {"basis_total":999,"buckets":...} wrapper exactly once.
		size += 6*utf8.RuneCountInString(groupSpec.Name) + 5 + 30
		for _, bucket := range buckets {
			size += 24 + 1 + 6*utf8.RuneCountInString(bucket.value)
		}
		groups[index] = AlternativeSummaryGroup{
			name: groupSpec.Name, basisTotal: groupSpec.BasisTotal, buckets: buckets,
		}
	}
	if size > MaxAlternativeSummaryEncodedBytes {
		return AlternativeSummary{}, invalidContract()
	}
	return AlternativeSummary{total: spec.TotalMatches, groups: groups, valid: true}, nil
}

func (s AlternativeSummary) TotalMatches() int { return s.total }

func (s AlternativeSummary) Groups() []AlternativeSummaryGroup {
	out := make([]AlternativeSummaryGroup, len(s.groups))
	for index, group := range s.groups {
		out[index] = AlternativeSummaryGroup{
			name: group.name, basisTotal: group.basisTotal,
			buckets: append([]SummaryBucket(nil), group.buckets...),
		}
	}
	return out
}

func (s AlternativeSummary) Valid() bool { return s.valid }
