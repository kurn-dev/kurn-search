package partscatalogalternatives

import (
	"bytes"
	"encoding/json"
	"strconv"

	"github.com/kurn-dev/kurn-search/adapter"
)

// parseRequestView is called only after the delivered discover compiler has
// accepted the original strict request. The compiler rejects duplicate,
// unknown, null and malformed members (including lone surrogates); this view
// extracts its already-validated selections and canonically re-encodes the
// superset request without facet filters.
type requestView struct {
	limit      int
	selections map[string][]string
	superset   []byte
}

func parseRequestView(raw []byte) (requestView, error) {
	var members map[string]json.RawMessage
	if err := json.Unmarshal(raw, &members); err != nil || members == nil {
		return requestView{}, invalidInput()
	}
	view := requestView{limit: 25, selections: map[string][]string{}}
	if value, present := members["limit"]; present {
		if err := json.Unmarshal(value, &view.limit); err != nil || view.limit < 1 || view.limit > adapter.MaxHits {
			return requestView{}, invalidInput()
		}
	}
	if rawFilters, present := members["filters"]; present {
		var filters map[string]json.RawMessage
		if err := json.Unmarshal(rawFilters, &filters); err != nil || filters == nil {
			return requestView{}, invalidInput()
		}
		for name, value := range filters {
			var choices []string
			if bytes.HasPrefix(bytes.TrimSpace(value), []byte("[")) {
				if err := json.Unmarshal(value, &choices); err != nil {
					return requestView{}, invalidInput()
				}
			} else {
				var choice string
				if err := json.Unmarshal(value, &choice); err != nil {
					return requestView{}, invalidInput()
				}
				choices = []string{choice}
			}
			term, err := adapter.NewMembershipFilterTerm(name, choices)
			if err != nil {
				return requestView{}, invalidInput()
			}
			view.selections[name] = term.Values()
		}
	}
	delete(members, "filters")
	members["limit"] = json.RawMessage(strconv.Itoa(adapter.MaxHits))
	encoded, err := json.Marshal(members)
	if err != nil {
		return requestView{}, invalidInput()
	}
	view.superset = encoded
	return view, nil
}
