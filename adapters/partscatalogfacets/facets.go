package partscatalogfacets

import (
	"bytes"
	"encoding/json"
	"sort"
	"unicode/utf8"

	"github.com/kurn-dev/kurn-search/adapter"
	"github.com/kurn-dev/kurn-search/internal/jsorder"
)

// facetFields are the three admitted parts-catalog groups in canonical
// JavaScript UTF-16 order. No other field may ever appear in a summary.
var facetFields = []string{"category", "manufacturer", "status"}

var sourceMembers = map[string]struct{}{
	"category": {}, "id": {}, "identifier": {}, "manufacturer": {},
	"name": {}, "partition_id": {}, "status": {}, "tenant_id": {},
}

// facetRecord carries exactly the three payload-exact facet values of one
// validated hit.
type facetRecord struct {
	category, manufacturer, status string
}

// decodeFacetPayload strictly decodes one canonical source payload emitted by
// the delivered discover resolution: exactly the closed eight-member object,
// no duplicates, string values within the delivered source scalar bound.
func decodeFacetPayload(raw []byte) (facetRecord, error) {
	var record facetRecord
	if len(raw) == 0 || len(raw) > 1<<20 {
		return record, invalidSummary()
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	open, err := decoder.Token()
	if err != nil || open != json.Delim('{') {
		return record, invalidSummary()
	}
	seen := map[string]struct{}{}
	targets := map[string]*string{
		"category": &record.category, "manufacturer": &record.manufacturer, "status": &record.status,
	}
	for decoder.More() {
		nameToken, err := decoder.Token()
		if err != nil {
			return record, invalidSummary()
		}
		name, ok := nameToken.(string)
		if !ok {
			return record, invalidSummary()
		}
		if _, admitted := sourceMembers[name]; !admitted {
			return record, invalidSummary()
		}
		if _, duplicate := seen[name]; duplicate {
			return record, invalidSummary()
		}
		seen[name] = struct{}{}
		if target, ok := targets[name]; ok {
			var value string
			if err := decoder.Decode(&value); err != nil {
				return record, invalidSummary()
			}
			if value == "" || utf8.RuneCountInString(value) > 512 {
				return record, invalidSummary()
			}
			*target = value
			continue
		}
		var skip json.RawMessage
		if err := decoder.Decode(&skip); err != nil {
			return record, invalidSummary()
		}
	}
	closing, err := decoder.Token()
	if err != nil || closing != json.Delim('}') {
		return record, invalidSummary()
	}
	for _, required := range []string{"category", "id", "identifier", "manufacturer", "name", "status", "tenant_id"} {
		if _, ok := seen[required]; !ok {
			return record, invalidSummary()
		}
	}
	return record, nil
}

// summarizeHits tallies the complete validated matched set into the closed
// three-group parts summary. Buckets are ordered by count descending, then by
// value in JavaScript UTF-16 code-unit order; every group's counts sum exactly
// to the total.
func summarizeHits(hits []adapter.ResolvedHit) (adapter.Summary, error) {
	if len(hits) > adapter.MaxSummaryTotal {
		return adapter.Summary{}, invalidSummary()
	}
	tallies := make([]map[string]int, len(facetFields))
	for index := range tallies {
		tallies[index] = map[string]int{}
	}
	for _, hit := range hits {
		if !hit.Valid() {
			return adapter.Summary{}, invalidSummary()
		}
		record, err := decodeFacetPayload(hit.Result())
		if err != nil {
			return adapter.Summary{}, err
		}
		tallies[0][record.category]++
		tallies[1][record.manufacturer]++
		tallies[2][record.status]++
	}
	groups := make([]adapter.SummaryGroupSpec, len(facetFields))
	for index, field := range facetFields {
		counts := tallies[index]
		values := make([]string, 0, len(counts))
		for value := range counts {
			values = append(values, value)
		}
		sort.Slice(values, func(i, j int) bool {
			left, right := counts[values[i]], counts[values[j]]
			if left != right {
				return left > right
			}
			return jsorder.Less(values[i], values[j])
		})
		buckets := make([]adapter.SummaryBucketSpec, len(values))
		for position, value := range values {
			buckets[position] = adapter.SummaryBucketSpec{Count: counts[value], Value: value}
		}
		groups[index] = adapter.SummaryGroupSpec{Name: field, Buckets: buckets}
	}
	summary, err := adapter.NewSummary(adapter.SummarySpec{TotalMatches: len(hits), Groups: groups})
	if err != nil {
		return adapter.Summary{}, invalidSummary()
	}
	return summary, nil
}
