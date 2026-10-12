package kurnhttp

import (
	"bytes"
	"encoding/json"
	"io"
	"maps"
	"slices"
	"testing"
	"unicode/utf8"
)

// referenceObject and referenceArray are the former token-based decoders,
// kept as the oracle for the single-pass versions.
func referenceObject(raw []byte) (map[string]json.RawMessage, bool) {
	if !utf8.Valid(raw) {
		return nil, false
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil {
		return nil, false
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		return nil, false
	}
	out := make(map[string]json.RawMessage)
	for dec.More() {
		keyToken, err := dec.Token()
		if err != nil {
			return nil, false
		}
		key, ok := keyToken.(string)
		if !ok {
			return nil, false
		}
		if _, duplicate := out[key]; duplicate {
			return nil, false
		}
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return nil, false
		}
		out[key] = bytes.Clone(value)
	}
	if tok, err = dec.Token(); err != nil {
		return nil, false
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '}' {
		return nil, false
	}
	if _, err = dec.Token(); err != io.EOF {
		return nil, false
	}
	return out, true
}

func referenceArray(raw []byte) ([]json.RawMessage, bool) {
	if !utf8.Valid(raw) {
		return nil, false
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil {
		return nil, false
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '[' {
		return nil, false
	}
	out := make([]json.RawMessage, 0)
	for dec.More() {
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return nil, false
		}
		out = append(out, bytes.Clone(value))
	}
	if tok, err = dec.Token(); err != nil {
		return nil, false
	}
	if delim, ok := tok.(json.Delim); !ok || delim != ']' {
		return nil, false
	}
	if _, err = dec.Token(); err != io.EOF {
		return nil, false
	}
	return out, true
}

var jsonSeeds = []string{
	`{}`, `[]`, `{"a":1}`, `[1,"x",{"a":1,"a":2}]`, `{"a":1,"a":2}`, `{"a":1,"a":2}`, `{"a":{"b":1,"b":2}}`,
	`{"\ud800":1,"\udbff":2}`, `{"a":"\ud800"}`, `{"a":1e400}`, `[1e400]`, `{"a":1} {}`, `[1] x`, `{"a":1,}`, `[1,]`,
	`null`, `"s"`, ` { "a" : [ 1 , 2 ] } `, `{"results":[{"candidates":[{"entry_id":"A","score":100}]}]}`, "{\"a\":\" \"}",
	`{"a":tru}`, `[{"x":null}]`, `{"a":"\"q\""}`, `[[[[]]]]`, "\t[\n]\n",
}

func checkObjectArrayEquivalent(t *testing.T, raw []byte) {
	t.Helper()
	gotObject, err := object(raw)
	wantObject, ok := referenceObject(raw)
	if (err == nil) != ok || (ok && !maps.EqualFunc(gotObject, wantObject, func(a, b json.RawMessage) bool { return bytes.Equal(a, b) })) {
		t.Fatalf("object(%q) = %q, %v; former %q, %v", raw, gotObject, err, wantObject, ok)
	}
	gotArray, err := array(raw)
	wantArray, ok := referenceArray(raw)
	if (err == nil) != ok || (ok && !slices.EqualFunc(gotArray, wantArray, func(a, b json.RawMessage) bool { return bytes.Equal(a, b) })) {
		t.Fatalf("array(%q) = %q, %v; former %q, %v", raw, gotArray, err, wantArray, ok)
	}
}

func TestObjectArrayMatchFormerDecoders(t *testing.T) {
	for _, seed := range jsonSeeds {
		checkObjectArrayEquivalent(t, []byte(seed))
	}
}

func FuzzObjectArrayMatchFormerDecoders(f *testing.F) {
	for _, seed := range jsonSeeds {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, raw []byte) { checkObjectArrayEquivalent(t, raw) })
}
