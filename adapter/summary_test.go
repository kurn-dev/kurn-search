package adapter

import (
	"strings"
	"testing"
)

func validSummarySpec() SummarySpec {
	return SummarySpec{TotalMatches: 3, Groups: []SummaryGroupSpec{
		{Name: "category", Buckets: []SummaryBucketSpec{{Count: 2, Value: "gasket"}, {Count: 1, Value: "bearing"}}},
		{Name: "manufacturer", Buckets: []SummaryBucketSpec{{Count: 2, Value: "Halden"}, {Count: 1, Value: "Vega"}}},
		{Name: "status", Buckets: []SummaryBucketSpec{{Count: 3, Value: "active"}}},
	}}
}

func TestNewSummaryValid(t *testing.T) {
	summary, err := NewSummary(validSummarySpec())
	if err != nil || !summary.Valid() {
		t.Fatalf("valid summary refused: %v", err)
	}
	if summary.TotalMatches() != 3 || len(summary.Groups()) != 3 {
		t.Fatalf("unexpected shape: %d %d", summary.TotalMatches(), len(summary.Groups()))
	}
}

func TestNewSummaryZeroMatch(t *testing.T) {
	summary, err := NewSummary(SummarySpec{TotalMatches: 0, Groups: []SummaryGroupSpec{
		{Name: "category"}, {Name: "manufacturer"}, {Name: "status"},
	}})
	if err != nil || !summary.Valid() {
		t.Fatalf("zero-match summary refused: %v", err)
	}
	for _, group := range summary.Groups() {
		if len(group.Buckets()) != 0 {
			t.Fatalf("zero-match group %s carries buckets", group.Name())
		}
	}
}

func TestNewSummaryRefusals(t *testing.T) {
	cases := map[string]func(*SummarySpec){
		"total-negative":   func(s *SummarySpec) { s.TotalMatches = -1 },
		"total-at-ceiling": func(s *SummarySpec) { s.TotalMatches = MaxHits },
		"no-groups":        func(s *SummarySpec) { s.Groups = nil },
		"too-many-groups": func(s *SummarySpec) {
			s.TotalMatches = 9
			s.Groups = nil
			for i := 0; i < MaxSummaryGroups+1; i++ {
				s.Groups = append(s.Groups, SummaryGroupSpec{
					Name:    string(rune('a' + i)),
					Buckets: []SummaryBucketSpec{{Count: 1, Value: string(rune('a' + i))}},
				})
			}
		},
		"group-name-order": func(s *SummarySpec) { s.Groups[0], s.Groups[1] = s.Groups[1], s.Groups[0] },
		"group-name-empty": func(s *SummarySpec) { s.Groups[0].Name = "" },
		"group-name-long":  func(s *SummarySpec) { s.Groups[0].Name = strings.Repeat("g", MaxSummaryGroupRunes+1) },
		"bucket-zero-count": func(s *SummarySpec) {
			s.Groups[0].Buckets[0].Count = 0
			s.Groups[0].Buckets[1].Count = 3
		},
		"bucket-value-long": func(s *SummarySpec) { s.Groups[0].Buckets[0].Value = strings.Repeat("v", MaxSummaryValueRunes+1) },
		"bucket-duplicate": func(s *SummarySpec) {
			s.Groups[0].Buckets = append(s.Groups[0].Buckets, s.Groups[0].Buckets[1])
			s.Groups[0].Buckets[1].Count = 1
		},
		"bucket-order-count": func(s *SummarySpec) {
			s.Groups[0].Buckets[0], s.Groups[0].Buckets[1] = s.Groups[0].Buckets[1], s.Groups[0].Buckets[0]
		},
		"bucket-order-value": func(s *SummarySpec) {
			s.Groups[0].Buckets = []SummaryBucketSpec{{Count: 1, Value: "gasket"}, {Count: 1, Value: "bearing"}}
			s.Groups[0].Buckets[0].Count, s.Groups[2].Buckets[0].Count = 1, 2
		},
		"count-sum": func(s *SummarySpec) { s.Groups[0].Buckets[0].Count = 3 },
	}
	for name, mutate := range cases {
		spec := validSummarySpec()
		mutate(&spec)
		if summary, err := NewSummary(spec); err == nil || summary.Valid() {
			t.Errorf("%s: invalid summary accepted", name)
		}
	}
}

func TestNewSummaryUtf16Order(t *testing.T) {
	// UTF-16 code-unit order: "Z" (U+005A) precedes "a" (U+0061); equal-count
	// buckets must already be in that order.
	spec := SummarySpec{TotalMatches: 2, Groups: []SummaryGroupSpec{{
		Name:    "category",
		Buckets: []SummaryBucketSpec{{Count: 1, Value: "Zulu"}, {Count: 1, Value: "alpha"}},
	}}}
	if _, err := NewSummary(spec); err != nil {
		t.Fatalf("canonical UTF-16 order refused: %v", err)
	}
	spec.Groups[0].Buckets[0], spec.Groups[0].Buckets[1] = spec.Groups[0].Buckets[1], spec.Groups[0].Buckets[0]
	if _, err := NewSummary(spec); err == nil {
		t.Fatal("reversed UTF-16 order accepted")
	}
}

func TestSummaryDefensiveCopies(t *testing.T) {
	spec := validSummarySpec()
	summary, err := NewSummary(spec)
	if err != nil {
		t.Fatal(err)
	}
	spec.Groups[0].Buckets[0].Value = "mutated"
	groups := summary.Groups()
	groups[0].buckets[0] = SummaryBucket{}
	buckets := summary.Groups()[0].Buckets()
	buckets[0] = SummaryBucket{}
	if summary.Groups()[0].Buckets()[0].Value() != "gasket" {
		t.Fatal("summary shares mutable state")
	}
}

func TestSummaryAtCeilingSentinel(t *testing.T) {
	if !IsSummaryAtCeiling(ErrSummaryAtCeiling) {
		t.Fatal("sentinel not classified")
	}
	if IsSummaryAtCeiling(invalidContract()) || IsSummaryAtCeiling(nil) {
		t.Fatal("unrelated error classified as ceiling")
	}
	var wrapped error = &wrappingSentinel{}
	if !IsSummaryAtCeiling(wrapped) {
		t.Fatal("wrapped sentinel not classified")
	}
}

type wrappingSentinel struct{}

func (*wrappingSentinel) Error() string { return "wrapped" }
func (*wrappingSentinel) Unwrap() error { return ErrSummaryAtCeiling }
