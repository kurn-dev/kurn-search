package partscatalogdiscover

import (
	"github.com/kurn-dev/kurn-search/adapter"
	"github.com/kurn-dev/kurn-search/internal/jsorder"
)

type evidence map[adapter.EvidenceKind]map[string]struct{}

func newEvidence() evidence {
	return evidence{
		kindExact: {}, kindIdentifierPrefix: {}, kindIdentifierWord: {}, kindNamePrefix: {}, kindNameWord: {},
	}
}

func (value evidence) add(kind adapter.EvidenceKind, term string) {
	if terms := value[kind]; terms != nil {
		terms[term] = struct{}{}
	}
}

func (value evidence) has(kind adapter.EvidenceKind, term string) bool {
	_, ok := value[kind][term]
	return ok
}

func (value evidence) hasAll(kind adapter.EvidenceKind, terms []string) bool {
	if len(terms) == 0 {
		return false
	}
	for _, term := range terms {
		if !value.has(kind, term) {
			return false
		}
	}
	return true
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
	exact    bool
	allWords bool
}

func compareRanked(left, right *ranked) int {
	if left.exact != right.exact {
		if left.exact {
			return -1
		}
		return 1
	}
	if left.allWords != right.allWords {
		if left.allWords {
			return -1
		}
		return 1
	}
	if value := jsorder.Compare(left.record.Identifier, right.record.Identifier); value != 0 {
		return value
	}
	if value := jsorder.Compare(left.record.Name, right.record.Name); value != 0 {
		return value
	}
	return jsorder.Compare(left.id, right.id)
}
