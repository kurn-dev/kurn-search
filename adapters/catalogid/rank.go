package catalogid

import (
	"github.com/kurn-dev/kurn-search/adapter"
	"github.com/kurn-dev/kurn-search/internal/jsorder"
)

type evidence map[adapter.EvidenceKind]map[string]struct{}

func newEvidence() evidence {
	return evidence{
		kindExact: {}, kindStarts: {}, kindEnds: {}, kindWord: {},
	}
}

func (value evidence) add(kind adapter.EvidenceKind, term string) {
	if terms := value[kind]; terms != nil {
		terms[term] = struct{}{}
	}
}

func (value evidence) sorted(kind adapter.EvidenceKind) []string {
	terms := value[kind]
	result := make([]string, 0, len(terms))
	for term := range terms {
		result = append(result, term)
	}
	jsorder.Sort(result)
	return result
}

type ranked struct {
	id       string
	record   sourceRecord
	payload  []byte
	evidence evidence
}

func rankTuple(value evidence) [7]int {
	return [7]int{
		boolInt(len(value[kindExact]) > 0),
		boolInt(len(value[kindStarts]) > 0),
		boolInt(len(value[kindEnds]) > 0),
		boolInt(len(value[kindWord]) > 0),
		len(value[kindStarts]), len(value[kindEnds]), len(value[kindWord]),
	}
}

func compareRanked(left, right *ranked) int {
	a, b := rankTuple(left.evidence), rankTuple(right.evidence)
	for index := range a {
		if a[index] > b[index] {
			return -1
		}
		if a[index] < b[index] {
			return 1
		}
	}
	return jsorder.Compare(left.id, right.id)
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
