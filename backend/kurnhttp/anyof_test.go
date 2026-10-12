package kurnhttp

import (
	"strings"
	"testing"
)

func TestExactAnyOfProbeMarshalsAndValidates(t *testing.T) {
	config := strings.Repeat("c", 64)
	topK := 8
	probe, err := NewProbe(ProbeSpec{ExactAnyOf: []string{"AX-100", "XA-010"}, List: "parts-v1", TopK: &topK, ExpectedConfigSHA256: config})
	if err != nil {
		t.Fatal(err)
	}
	if got := probe.ExactAnyOf(); len(got) != 2 || got[0] != "AX-100" {
		t.Fatalf("terms: %v", got)
	}
	want := `{"checks":[{"exact_any_of":["AX-100","XA-010"],"lists":["parts-v1"],"topk":8}]}`
	if body := string(marshalBatch([]Probe{probe})); body != want {
		t.Fatalf("body %s, want %s", body, want)
	}
	threshold := 0.5
	for name, spec := range map[string]ProbeSpec{
		"query and terms": {Query: "x", ExactAnyOf: []string{"x"}, List: "parts-v1", ExpectedConfigSHA256: config},
		"threshold":       {ExactAnyOf: []string{"x"}, Threshold: &threshold, List: "parts-v1", ExpectedConfigSHA256: config},
		"empty term":      {ExactAnyOf: []string{"x", ""}, List: "parts-v1", ExpectedConfigSHA256: config},
		"long term":       {ExactAnyOf: []string{strings.Repeat("x", 513)}, List: "parts-v1", ExpectedConfigSHA256: config},
		"too many":        {ExactAnyOf: make([]string, 65), List: "parts-v1", ExpectedConfigSHA256: config},
		"invalid utf8":    {ExactAnyOf: []string{"a\xff"}, List: "parts-v1", ExpectedConfigSHA256: config},
	} {
		if _, err := NewProbe(spec); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	plain, err := NewProbe(ProbeSpec{Query: "x", List: "parts-v1", ExpectedConfigSHA256: config})
	if err != nil || plain.ExactAnyOf() != nil {
		t.Fatalf("ordinary probe: %v %v", err, plain.ExactAnyOf())
	}
}
