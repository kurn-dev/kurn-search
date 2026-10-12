package adapter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func validAlternativeSummarySpec() AlternativeSummarySpec {
	return AlternativeSummarySpec{TotalMatches: 1, Groups: []AlternativeSummaryGroupSpec{
		{Name: "category", BasisTotal: 1, Buckets: []SummaryBucketSpec{{Count: 1, Value: "bearing"}}},
		{Name: "manufacturer", BasisTotal: 2, Buckets: []SummaryBucketSpec{{Count: 1, Value: "Halden"}, {Count: 1, Value: "Northstar"}}},
		{Name: "status", BasisTotal: 1, Buckets: []SummaryBucketSpec{{Count: 1, Value: "active"}}},
	}}
}

func TestAlternativeSummaryValidAndDefensive(t *testing.T) {
	spec := validAlternativeSummarySpec()
	summary, err := NewAlternativeSummary(spec)
	if err != nil || !summary.Valid() {
		t.Fatalf("valid alternative summary refused: %v", err)
	}
	if summary.TotalMatches() != 1 || summary.Groups()[1].BasisTotal() != 2 {
		t.Fatal("distinct group basis was lost")
	}
	spec.Groups[1].Buckets[0].Value = "changed"
	groups := summary.Groups()
	groups[1].buckets[0] = SummaryBucket{}
	buckets := summary.Groups()[1].Buckets()
	buckets[0] = SummaryBucket{}
	if got := summary.Groups()[1].Buckets()[0].Value(); got != "Halden" {
		t.Fatalf("mutable state escaped: %s", got)
	}
}

func TestAlternativeSummaryZeroAndRefusals(t *testing.T) {
	zero := AlternativeSummarySpec{Groups: []AlternativeSummaryGroupSpec{
		{Name: "category"}, {Name: "manufacturer"}, {Name: "status"},
	}}
	if summary, err := NewAlternativeSummary(zero); err != nil || !summary.Valid() {
		t.Fatalf("zero summary refused: %v", err)
	}
	cases := map[string]func(*AlternativeSummarySpec){
		"negative-total":    func(s *AlternativeSummarySpec) { s.TotalMatches = -1 },
		"ceiling-total":     func(s *AlternativeSummarySpec) { s.TotalMatches = MaxHits },
		"wrong-group-count": func(s *AlternativeSummarySpec) { s.Groups = s.Groups[:2] },
		"name-order":        func(s *AlternativeSummarySpec) { s.Groups[0], s.Groups[1] = s.Groups[1], s.Groups[0] },
		"basis-below-total": func(s *AlternativeSummarySpec) { s.Groups[0].BasisTotal = 0; s.Groups[0].Buckets = nil },
		"basis-at-ceiling":  func(s *AlternativeSummarySpec) { s.Groups[0].BasisTotal = MaxHits },
		"basis-sum":         func(s *AlternativeSummarySpec) { s.Groups[1].BasisTotal = 3 },
		"duplicate-bucket": func(s *AlternativeSummarySpec) {
			s.Groups[1].Buckets[1].Value = s.Groups[1].Buckets[0].Value
		},
		"bucket-order": func(s *AlternativeSummarySpec) {
			s.Groups[1].Buckets[0], s.Groups[1].Buckets[1] = s.Groups[1].Buckets[1], s.Groups[1].Buckets[0]
		},
		"zero-bucket": func(s *AlternativeSummarySpec) { s.Groups[0].Buckets[0].Count = 0 },
	}
	for name, mutate := range cases {
		spec := validAlternativeSummarySpec()
		mutate(&spec)
		if summary, err := NewAlternativeSummary(spec); err == nil || summary.Valid() {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestAlternativeSummaryMaximumDistinctEncodedBound(t *testing.T) {
	buckets := make([]SummaryBucketSpec, MaxSummaryTotal)
	for index := range buckets {
		buckets[index] = SummaryBucketSpec{
			Count: 1, Value: strings.Repeat("\x01", 509) + fmt.Sprintf("%03d", index),
		}
	}
	summary, err := NewAlternativeSummary(AlternativeSummarySpec{TotalMatches: 999,
		Groups: []AlternativeSummaryGroupSpec{
			{Name: "category", BasisTotal: 999, Buckets: buckets},
			{Name: "manufacturer", BasisTotal: 999, Buckets: buckets},
			{Name: "status", BasisTotal: 999, Buckets: []SummaryBucketSpec{
				{Count: 500, Value: "active"}, {Count: 499, Value: "discontinued"},
			}},
		},
	})
	if err != nil || !summary.Valid() {
		t.Fatalf("maximum summary refused: %v", err)
	}
	facets := map[string]any{}
	for _, group := range summary.Groups() {
		values := make([]map[string]any, 0, len(group.Buckets()))
		for _, bucket := range group.Buckets() {
			values = append(values, map[string]any{"count": bucket.Count(), "value": bucket.Value()})
		}
		facets[group.Name()] = map[string]any{"basis_total": group.BasisTotal(), "buckets": values}
	}
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(map[string]any{"total_matches": summary.TotalMatches(), "facets": facets}); err != nil {
		t.Fatal(err)
	}
	if output.Len() > MaxAlternativeSummaryEncodedBytes {
		t.Fatalf("encoded summary %d exceeds accepted bound %d", output.Len(), MaxAlternativeSummaryEncodedBytes)
	}
}
