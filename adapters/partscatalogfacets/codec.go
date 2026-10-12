package partscatalogfacets

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"

	"github.com/kurn-dev/kurn-search/adapter"
)

// The request grammar is the delivered parts-catalog-discover grammar. Only
// the limit member is ever substituted; every other member's raw bytes pass
// through verbatim, and nested content is validated by the delegate's own
// strict parser when it compiles and resolves the substituted document.

var requestMembers = map[string]struct{}{
	"filters": {}, "limit": {}, "partition_id": {}, "query": {},
}

// requestView is the strictly decoded caller request: every member's raw
// bytes plus the caller's effective limit.
type requestView struct {
	members map[string]json.RawMessage
	limit   int
}

// parseRequestView strictly decodes the top-level request object, refuses
// unknown, duplicate or missing-required members and trailing data, and
// validates the caller limit against the delivered grammar (default 25,
// 1..1000). Nested member content passes through as raw bytes for the
// delegate to validate.
func parseRequestView(raw []byte) (requestView, error) {
	if len(raw) == 0 || len(raw) > 1<<20 {
		return requestView{}, invalidInput()
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	open, err := decoder.Token()
	if err != nil || open != json.Delim('{') {
		return requestView{}, invalidInput()
	}
	view := requestView{members: map[string]json.RawMessage{}, limit: 25}
	seen := map[string]struct{}{}
	for decoder.More() {
		nameToken, err := decoder.Token()
		if err != nil {
			return requestView{}, invalidInput()
		}
		name, ok := nameToken.(string)
		if !ok {
			return requestView{}, invalidInput()
		}
		if _, admitted := requestMembers[name]; !admitted {
			return requestView{}, invalidInput()
		}
		if _, duplicate := seen[name]; duplicate {
			return requestView{}, invalidInput()
		}
		seen[name] = struct{}{}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return requestView{}, invalidInput()
		}
		view.members[name] = value
	}
	if _, ok := seen["query"]; !ok {
		return requestView{}, invalidInput()
	}
	closing, err := decoder.Token()
	if err != nil || closing != json.Delim('}') {
		return requestView{}, invalidInput()
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return requestView{}, invalidInput()
	}
	if rawLimit, ok := view.members["limit"]; ok {
		var limit int
		valueDecoder := json.NewDecoder(bytes.NewReader(rawLimit))
		if err := valueDecoder.Decode(&limit); err != nil {
			return requestView{}, invalidInput()
		}
		if err := valueDecoder.Decode(&extra); !errors.Is(err, io.EOF) {
			return requestView{}, invalidInput()
		}
		if limit < 1 || limit > adapter.MaxHits {
			return requestView{}, invalidInput()
		}
		view.limit = limit
	}
	return view, nil
}

// substituteLimit re-encodes the request with the limit member set to
// adapter.MaxHits and every other member's raw bytes preserved. Map encoding
// emits members in bytewise key order; the delegate's strict parser accepts
// any member order.
func substituteLimit(view requestView) []byte {
	members := make(map[string]json.RawMessage, len(view.members))
	for name, value := range view.members {
		members[name] = value
	}
	members["limit"] = json.RawMessage("1000")
	out, err := json.Marshal(members)
	if err != nil {
		return nil
	}
	return out
}
