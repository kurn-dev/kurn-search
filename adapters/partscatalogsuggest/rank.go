package partscatalogsuggest

import "github.com/kurn-dev/kurn-search/internal/jsorder"

type ranked struct {
	id            string
	record        sourceRecord
	score         int
	exact         bool
	transposition bool
	key           string
}

func compareRanked(left, right *ranked) int {
	if left.score != right.score {
		if left.score > right.score {
			return -1
		}
		return 1
	}
	if left.exact != right.exact {
		if left.exact {
			return -1
		}
		return 1
	}
	if order := jsorder.Compare(left.record.Identifier, right.record.Identifier); order != 0 {
		return order
	}
	return jsorder.Compare(left.id, right.id)
}
