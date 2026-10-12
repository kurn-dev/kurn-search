package kurnhttp

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

func requireCategory(t *testing.T, err error, want Category) *Error {
	t.Helper()
	var got *Error
	if !errors.As(err, &got) {
		t.Fatalf("error %T is not *Error", err)
	}
	if got.Category != want {
		t.Fatalf("category = %q, want %q", got.Category, want)
	}
	return got
}

func mustFilter(t *testing.T, terms ...FilterTerm) StringFilter {
	t.Helper()
	filter, err := NewStringFilter(terms)
	if err != nil {
		t.Fatal(err)
	}
	return filter
}

func TestStringFilterCanonicalNeutralFixtures(t *testing.T) {
	tests := []struct {
		id    string
		value string
		want  string
	}{
		{id: "quote", value: `"`, want: `{"n":"\""}`},
		{id: "backslash", value: `\`, want: `{"n":"\\"}`},
		{id: "slash", value: `/`, want: `{"n":"/"}`},
		{id: "backspace", value: "\b", want: `{"n":"\b"}`},
		{id: "form-feed", value: "\f", want: `{"n":"\f"}`},
		{id: "newline", value: "\n", want: `{"n":"\n"}`},
		{id: "return", value: "\r", want: `{"n":"\r"}`},
		{id: "tab", value: "\t", want: `{"n":"\t"}`},
		{id: "nul", value: "\x00", want: `{"n":"\u0000"}`},
		{id: "bell", value: "\x07", want: `{"n":"\u0007"}`},
		{id: "vertical-tab", value: "\x0b", want: `{"n":"\u000b"}`},
		{id: "unit-separator", value: "\x1f", want: `{"n":"\u001f"}`},
		{id: "html", value: `<>&`, want: `{"n":"<>&"}`},
		{id: "line-separators", value: "\u2028\u2029", want: "{\"n\":\"\u2028\u2029\"}"},
		{id: "unicode", value: "é😀", want: `{"n":"é😀"}`},
	}
	for _, test := range tests {
		t.Run(test.id, func(t *testing.T) {
			filter := mustFilter(t, FilterTerm{Name: "n", Value: test.value})
			if got := string(filter.CanonicalJSON()); got != test.want {
				t.Fatalf("canonical = %q, want %q", got, test.want)
			}
		})
	}
}

func TestStringFilterSortsUTF8BytesAndIsImmutable(t *testing.T) {
	input := []FilterTerm{
		{Name: "😀", Value: "astral"},
		{Name: "é", Value: "accent"},
		{Name: "z", Value: "latin"},
		{Name: "a", Value: "first"},
	}
	filter := mustFilter(t, input...)
	want := `{"a":"first","z":"latin","é":"accent","😀":"astral"}`
	if got := string(filter.CanonicalJSON()); got != want {
		t.Fatalf("canonical = %s, want %s", got, want)
	}
	input[0].Name, input[0].Value = "changed", "changed"
	terms := filter.Terms()
	terms[0].Name, terms[0].Value = "changed", "changed"
	bytes := filter.CanonicalJSON()
	bytes[0] = '['
	if got := string(filter.CanonicalJSON()); got != want {
		t.Fatalf("filter was mutated: %s", got)
	}
}

func TestStringFilterBoundsAndInvalidUTF8(t *testing.T) {
	maxName := strings.Repeat("n", maxFilterNameRunes)
	maxValue := strings.Repeat("😀", maxFilterValueRunes)
	filter := mustFilter(t, FilterTerm{Name: maxName, Value: maxValue})
	if filter.Empty() || utf8.RuneCount(filter.CanonicalJSON()) == 0 {
		t.Fatal("maximum filter was not retained")
	}

	invalid := []struct {
		id    string
		terms []FilterTerm
	}{
		{id: "too-many", terms: []FilterTerm{{Name: "0"}, {Name: "1"}, {Name: "2"}, {Name: "3"}, {Name: "4"}, {Name: "5"}, {Name: "6"}, {Name: "7"}, {Name: "8"}}},
		{id: "empty-name", terms: []FilterTerm{{Name: "", Value: "v"}}},
		{id: "long-name", terms: []FilterTerm{{Name: strings.Repeat("n", maxFilterNameRunes+1), Value: "v"}}},
		{id: "long-value", terms: []FilterTerm{{Name: "n", Value: strings.Repeat("v", maxFilterValueRunes+1)}}},
		{id: "duplicate", terms: []FilterTerm{{Name: "n", Value: "one"}, {Name: "n", Value: "two"}}},
		{id: "invalid-name", terms: []FilterTerm{{Name: string([]byte{0xff}), Value: "v"}}},
		{id: "invalid-value", terms: []FilterTerm{{Name: "n", Value: string([]byte{0xff})}}},
	}
	for _, test := range invalid {
		t.Run(test.id, func(t *testing.T) {
			_, err := NewStringFilter(test.terms)
			requireCategory(t, err, CategoryInvalidRequest)
		})
	}

	empty, err := NewStringFilter(nil)
	if err != nil || !empty.Empty() || empty.CanonicalJSON() != nil || empty.Terms() != nil {
		t.Fatalf("zero filter = %#v, %v", empty, err)
	}
}
