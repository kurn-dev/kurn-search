package jsjson

import (
	"bytes"
	"testing"
)

func TestEncodeFrozenEscapingAndUTF16KeyOrder(t *testing.T) {
	t.Parallel()
	got, err := Encode(Object{
		"\uE000": "private",
		"😀":      "astral",
		"escape": "\"\\\b\f\n\r\t\x00/ <>& \u2028\u2029",
		"array":  []any{"x", int64(25), Object{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []byte("{\n" +
		"  \"array\": [\n" +
		"    \"x\",\n" +
		"    25,\n" +
		"    {}\n" +
		"  ],\n" +
		"  \"escape\": \"\\\"\\\\\\b\\f\\n\\r\\t\\u0000/ <>& \u2028\u2029\",\n" +
		"  \"😀\": \"astral\",\n" +
		"  \"\uE000\": \"private\"\n" +
		"}\n")
	if !bytes.Equal(got, want) {
		t.Fatalf("Encode() mismatch\n got: %q\nwant: %q", got, want)
	}
}

func TestEncodeRejectsOutsideClosedClasses(t *testing.T) {
	t.Parallel()
	for _, value := range []any{
		nil, true, 1.5, MaxExactInteger + 1, uint64(MaxExactInteger) + 1,
		map[string]any{"bad": nil}, string([]byte{0xff}),
	} {
		if _, err := Encode(value); err == nil {
			t.Fatalf("Encode(%T) unexpectedly succeeded", value)
		}
	}
}
