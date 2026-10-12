package membershipjson

import (
	"bytes"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestNormalizeCanonicalVectors(t *testing.T) {
	tests := []struct {
		name string
		in   []Term
		want string
	}{
		{"empty", nil, ""},
		{"scalar", []Term{{"category", []string{"bearing"}}}, `{"category":"bearing"}`},
		{"duplicate-singleton", []Term{{"category", []string{"bearing", "bearing"}}}, `{"category":"bearing"}`},
		{"set-and-scope", []Term{{"tenant_id", []string{"tenant-a"}}, {"category", []string{"filter", "bearing", "bearing"}}}, `{"category":{"in":["bearing","filter"]},"tenant_id":"tenant-a"}`},
		{"byte-order", []Term{{"category", []string{"é", "e", "bearing", "Bearing"}}}, `{"category":{"in":["Bearing","bearing","e","é"]}}`},
		{"full-status", []Term{{"status", []string{"discontinued", "active"}}}, `{"status":{"in":["active","discontinued"]}}`},
		{"literal-spaces", []Term{{"manufacturer", []string{" Northstar", "Northstar "}}}, `{"manufacturer":{"in":[" Northstar","Northstar "]}}`},
		{"mandatory-escapes", []Term{{"category", []string{"\"\\\b\f\n\r\t\x00\x01\x1f"}}}, `{"category":"\"\\\b\f\n\r\t\u0000\u0001\u001f"}`},
		{"raw-unicode-html-slash", []Term{{"category", []string{"<>&/é😀\u2028\u2029"}}}, "{\"category\":\"<>&/é😀\u2028\u2029\"}"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			terms, got, err := Normalize(tt.in)
			if err != nil || string(got) != tt.want {
				t.Fatalf("canonical mismatch: got %q, want %q, err %v", got, tt.want, err)
			}
			_, second, err := Normalize(terms)
			if err != nil || !bytes.Equal(got, second) {
				t.Fatal("normalization is not idempotent")
			}
		})
	}
}

func TestNormalizeRefusesSuppliedShape(t *testing.T) {
	seventeen := make([]string, 17)
	for i := range seventeen {
		seventeen[i] = "bearing"
	}
	tooManyNames := make([]Term, 9)
	for i := range tooManyNames {
		tooManyNames[i] = Term{fmt.Sprintf("field%d", i), []string{"x"}}
	}
	tests := []struct {
		name string
		in   []Term
	}{
		{"no-values", []Term{{"category", nil}}},
		{"empty-value", []Term{{"category", []string{""}}}},
		{"invalid-member-not-laundered", []Term{{"category", []string{"x", "", "x"}}}},
		{"supplied-count-before-dedup", []Term{{"category", seventeen}}},
		{"empty-name", []Term{{"", []string{"x"}}}},
		{"long-name", []Term{{strings.Repeat("a", 129), []string{"x"}}}},
		{"long-value", []Term{{"category", []string{strings.Repeat("a", 513)}}}},
		{"invalid-name-utf8", []Term{{string([]byte{0xff}), []string{"x"}}}},
		{"invalid-value-utf8", []Term{{"category", []string{string([]byte{0xff})}}}},
		{"surrogate-utf8", []Term{{"category", []string{string([]byte{0xed, 0xa0, 0x80})}}}},
		{"duplicate-name", []Term{{"category", []string{"x"}}, {"category", []string{"y"}}}},
		{"too-many-names", tooManyNames},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			terms, raw, err := Normalize(tt.in)
			if err == nil || terms != nil || raw != nil || err.Error() != "membership filter: invalid expression" {
				t.Fatal("invalid shape was admitted or returned partial/private material")
			}
		})
	}
}

func TestNormalizeScalarAndNameRuneBounds(t *testing.T) {
	for _, scalar := range []string{"x", "é", "😀", "\x01"} {
		value := strings.Repeat(scalar, 512)
		if _, _, err := Normalize([]Term{{strings.Repeat("😀", 128), []string{value}}}); err != nil {
			t.Fatal("at-bound valid Unicode was refused")
		}
		if _, _, err := Normalize([]Term{{"category", []string{value + scalar}}}); err == nil {
			t.Fatal("513 scalars admitted")
		}
	}
}

func capTerms(fill int) []Term {
	category, manufacturer := make([]string, 16), make([]string, 16)
	for i := 0; i < 16; i++ {
		category[i] = fmt.Sprintf("c%02d", i) + strings.Repeat("x", 509)
		manufacturer[i] = fmt.Sprintf("m%02d", i) + strings.Repeat("x", 509)
	}
	manufacturer[15] = "m15" + strings.Repeat("x", fill)
	return []Term{{"category", category}, {"manufacturer", manufacturer}, {"tenant_id", []string{"tenant-a"}}}
}

func TestNormalizeCompleteCanonicalByteCap(t *testing.T) {
	_, at, err := Normalize(capTerms(345))
	if err != nil || len(at) != 16384 || MaxCanonicalBytes != 16384 {
		t.Fatalf("at-cap fixture mismatch: length %d, error %v", len(at), err)
	}
	for name, terms := range map[string][]Term{
		"one-over":   capTerms(346),
		"scope-push": append(capTerms(345), Term{"partition_id", []string{"north"}}),
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := Normalize(terms); err == nil {
				t.Fatal("oversized complete scoped expression admitted")
			}
		})
	}
	for _, scalar := range []string{"😀", "\x01"} {
		values := make([]string, 16)
		for i := range values {
			values[i] = fmt.Sprintf("%02d", i) + strings.Repeat(scalar, 510)
		}
		if _, _, err := Normalize([]Term{{"category", values}}); err == nil {
			t.Fatal("valid scalar counts bypassed the canonical byte cap")
		}
	}
}

func TestNormalizeDoesNotAliasInput(t *testing.T) {
	input := []Term{{"category", []string{"filter", "bearing", "bearing"}}}
	terms, raw, err := Normalize(input)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(input[0].Values, []string{"filter", "bearing", "bearing"}) {
		t.Fatal("normalization mutated caller ordering")
	}
	input[0].Name, input[0].Values[0] = "changed", "changed"
	if terms[0].Name != "category" || terms[0].Values[0] != "bearing" || string(raw) != `{"category":{"in":["bearing","filter"]}}` {
		t.Fatal("normalized state aliases input")
	}
	terms[0].Values[0] = "changed-again"
	if input[0].Values[1] != "bearing" || string(raw) != `{"category":{"in":["bearing","filter"]}}` {
		t.Fatal("output slices alias input or canonical bytes")
	}
}
