package kurnhttp_test

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/kurn-dev/kurn-search/backend/kurnhttp"
)

func TestMembershipFilterCanonicalAndCopies(t *testing.T) {
	input := []kurnhttp.MembershipTerm{
		{Name: "tenant_id", Values: []string{"tenant-a"}},
		{Name: "category", Values: []string{"filter", "bearing", "bearing"}},
	}
	filter, err := kurnhttp.NewMembershipFilter(input)
	want := `{"category":{"in":["bearing","filter"]},"tenant_id":"tenant-a"}`
	if err != nil || filter.Empty() || string(filter.CanonicalJSON()) != want {
		t.Fatal("canonical filter mismatch")
	}
	input[1].Values[0], input[0].Name = "other", "other"
	terms, raw := filter.Terms(), filter.CanonicalJSON()
	terms[0].Name, terms[0].Values[0], raw[0] = "other", "other", '!'
	if string(filter.CanonicalJSON()) != want || !reflect.DeepEqual(filter.Terms()[0].Values, []string{"bearing", "filter"}) {
		t.Fatal("constructed filter aliases mutable input/accessor data")
	}
}

func TestMembershipSingletonKeepsExactStringWire(t *testing.T) {
	for _, values := range [][]string{{"bearing"}, {"bearing", "bearing"}} {
		membership, err := kurnhttp.NewMembershipFilter([]kurnhttp.MembershipTerm{{Name: "category", Values: values}})
		if err != nil {
			t.Fatal(err)
		}
		exact, err := kurnhttp.NewStringFilter([]kurnhttp.FilterTerm{{Name: "category", Value: "bearing"}})
		if err != nil || !bytes.Equal(membership.CanonicalJSON(), exact.CanonicalJSON()) || string(membership.CanonicalJSON()) != `{"category":"bearing"}` {
			t.Fatal("singleton is not downstream-identical to legacy scalar")
		}
	}
	empty, err := kurnhttp.NewMembershipFilter(nil)
	if err != nil || !empty.Empty() || len(empty.CanonicalJSON()) != 0 || !(kurnhttp.MembershipFilter{}).Empty() {
		t.Fatal("empty backend filter changed meaning")
	}
}

func TestMembershipFilterRefusesInvalidSuppliedInput(t *testing.T) {
	seventeen := make([]string, 17)
	for i := range seventeen {
		seventeen[i] = "bearing"
	}
	tests := [][]kurnhttp.MembershipTerm{
		{{Name: "category", Values: nil}},
		{{Name: "category", Values: []string{""}}},
		{{Name: "category", Values: seventeen}},
		{{Name: "category", Values: []string{strings.Repeat("é", 513)}}},
		{{Name: "category", Values: []string{string([]byte{0xed, 0xa0, 0x80})}}},
		{{Name: "", Values: []string{"x"}}},
		{{Name: "category", Values: []string{"x"}}, {Name: "category", Values: []string{"y"}}},
	}
	for _, input := range tests {
		filter, err := kurnhttp.NewMembershipFilter(input)
		var classified *kurnhttp.Error
		if !errors.As(err, &classified) || classified.Category != kurnhttp.CategoryInvalidRequest || !filter.Empty() {
			t.Fatal("invalid input failed to produce a closed backend constructor refusal")
		}
	}
}

func TestMembershipFilterCompleteByteCap(t *testing.T) {
	makeInput := func(fill int) []kurnhttp.MembershipTerm {
		category, manufacturer := make([]string, 16), make([]string, 16)
		for i := 0; i < 16; i++ {
			category[i] = fmt.Sprintf("c%02d", i) + strings.Repeat("x", 509)
			manufacturer[i] = fmt.Sprintf("m%02d", i) + strings.Repeat("x", 509)
		}
		manufacturer[15] = "m15" + strings.Repeat("x", fill)
		return []kurnhttp.MembershipTerm{{Name: "category", Values: category}, {Name: "manufacturer", Values: manufacturer}, {Name: "tenant_id", Values: []string{"tenant-a"}}}
	}
	input := makeInput(345)
	filter, err := kurnhttp.NewMembershipFilter(input)
	if err != nil || len(filter.CanonicalJSON()) != 16384 || kurnhttp.MaxMembershipFilterBytes != 16384 {
		t.Fatal("at-cap complete filter refused")
	}
	if _, err := kurnhttp.NewMembershipFilter(makeInput(346)); err == nil {
		t.Fatal("one-over complete filter admitted")
	}
	input = append(input, kurnhttp.MembershipTerm{Name: "partition_id", Values: []string{"north"}})
	if _, err := kurnhttp.NewMembershipFilter(input); err == nil {
		t.Fatal("partition bytes not charged against cap")
	}
}
