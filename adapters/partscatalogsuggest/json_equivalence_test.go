package partscatalogsuggest

import (
	"bytes"
	"encoding/json"
	"maps"
	"strings"
	"testing"
	"unicode/utf8"
)

// referenceMembers is the former parseMembers decode (token walk for
// duplicate names at every depth, then a map decode and EOF check), kept
// here as the oracle for the single-pass splitObject.
func referenceMembers(raw []byte) (map[string]json.RawMessage, bool) {
	if len(raw) == 0 || !utf8.Valid(raw) || !json.Valid(raw) || referenceRejectDuplicates(raw) != nil {
		return nil, false
	}
	var members map[string]json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&members); err != nil || members == nil || requireEOF(decoder) != nil {
		return nil, false
	}
	return members, true
}

func referenceRejectDuplicates(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := referenceWalk(decoder); err != nil {
		return err
	}
	return requireEOF(decoder)
}

func referenceWalk(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := map[string]struct{}{}
		for decoder.More() {
			nameToken, err := decoder.Token()
			if err != nil {
				return err
			}
			name, ok := nameToken.(string)
			if !ok {
				return invalidInput()
			}
			if _, duplicate := seen[name]; duplicate {
				return invalidInput()
			}
			seen[name] = struct{}{}
			if err := referenceWalk(decoder); err != nil {
				return err
			}
		}
		if end, err := decoder.Token(); err != nil || end != json.Delim('}') {
			return invalidInput()
		}
	case '[':
		for decoder.More() {
			if err := referenceWalk(decoder); err != nil {
				return err
			}
		}
		if end, err := decoder.Token(); err != nil || end != json.Delim(']') {
			return invalidInput()
		}
	default:
		return invalidInput()
	}
	return nil
}

// referenceString is the former decodeString decode step.
func referenceString(raw []byte) (string, bool) {
	var value string
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(&value); err != nil || requireEOF(decoder) != nil || !utf8.ValidString(value) {
		return "", false
	}
	return value, true
}

var equivalenceSeeds = []string{
	`{}`, `{"a":1}`, ` {"a" : "x" } `, `{"a":1,"a":2}`, `{"a":1,"a":2}`, `{"a":{"b":1,"b":2}}`,
	`{"a":[{"b":1},{"b":1,"b":2}]}`, `{"a":[{"b":1},{"b":2}]}`, `{"\ud800":1}`, `{"\ud800":1,"\udbff":2}`,
	`{"a":"\ud800"}`, `{"a":"x"} {}`, `{"a":"x"}x`, `[]`, `null`, `"s"`, `1`, `{"a":null}`, `{"a":1e400}`,
	`{"a":-0.0,"b":true,"c":false}`, `{"a":"é","b":"é"}`, "{\"a\":\" \"}", `{"a":"\"q\""}`, `{"":1}`,
	`{"a":[1,2,[3,{"x":{"y":[]}}]]}`, `{"a":1,}`, `{"a" 1}`, `{a:1}`, "{\"a\":\"x\"}\n\t ", `{"query":"ZS-901","limit":25}`,
	`{"query":"ab","filters":{"status":["active","active"]}}`, `{"query":"ab","filters":{"status":"x","status":"y"}}`,
	`{"a":"1e400"}`, `{"1e400":1}`, `{"a":true,"b":1e400}`, `{"a":false,"e":[1E309]}`, `{"a":-1e-400}`, `{"a":1.5e3}`,
	`{"a":"x\"1e400"}`, `{"a":"\\","b":1e400}`, `{"a":0.` + strings.Repeat("9", 400) + `}`, `{"a":` + strings.Repeat("9", 400) + `}`,
	`{"a":` + strings.Repeat("9", 300) + `}`, `{"a":[1,2e2,true,null,"e"]}`,
}

func TestSplitObjectMatchesFormerDecode(t *testing.T) {
	for _, seed := range equivalenceSeeds {
		checkMembersEquivalent(t, []byte(seed))
	}
}

func TestPlainStringMatchesDecoder(t *testing.T) {
	for _, raw := range []string{`"abc"`, ` "abc" `, `""`, `"a\"b"`, `"a\\b"`, `"A"`, `"é"`, "\" \"", "\"a\tb\"",
		`"a/b"`, `"\/"`, `"x`, `x"`, `"`, ` "" `, " \"x\"", "\"\xff\"", `"日本"`} {
		got, ok := plainString([]byte(raw))
		if !ok {
			continue
		}
		want, wantOK := referenceString([]byte(raw))
		if !wantOK || got != want {
			t.Fatalf("plainString(%q) = %q, decoder gives %q ok=%v", raw, got, want, wantOK)
		}
	}
}

func FuzzSplitObjectMatchesFormerDecode(f *testing.F) {
	for _, seed := range equivalenceSeeds {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		checkMembersEquivalent(t, raw)
		if got, ok := plainString(raw); ok {
			if want, wantOK := referenceString(raw); !wantOK || got != want {
				t.Fatalf("plainString(%q) = %q, decoder gives %q ok=%v", raw, got, want, wantOK)
			}
		}
	})
}

func checkMembersEquivalent(t *testing.T, raw []byte) {
	t.Helper()
	want, wantOK := referenceMembers(raw)
	var got map[string]json.RawMessage
	gotOK := len(raw) != 0 && utf8.Valid(raw) && json.Valid(raw)
	if gotOK {
		var err error
		got, err = splitObject(raw)
		gotOK = err == nil
	}
	if gotOK != wantOK {
		t.Fatalf("%q: splitObject ok=%v, former decode ok=%v", raw, gotOK, wantOK)
	}
	if gotOK && !maps.EqualFunc(got, want, func(a, b json.RawMessage) bool { return bytes.Equal(a, b) }) {
		t.Fatalf("%q: members differ: %q vs %q", raw, got, want)
	}
}
