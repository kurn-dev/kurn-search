package jsorder

import (
	"slices"
	"testing"
)

func TestCompareFrozenUTF16Cases(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		left, right string
		want        int
	}{
		{name: "equal", left: "same", right: "same", want: 0},
		{name: "prefix", left: "a", right: "aa", want: -1},
		{name: "bmp", left: "a", right: "b", want: -1},
		{name: "astral-before-private-use", left: "😀", right: "\uE000", want: -1},
		{name: "private-use-after-astral", left: "\uE000", right: "😀", want: 1},
		{name: "combining", left: "a\u0301", right: "á", want: -1},
		{name: "empty", left: "", right: "x", want: -1},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := Compare(test.left, test.right); got != test.want {
				t.Fatalf("Compare() = %d, want %d", got, test.want)
			}
		})
	}
}

func TestSortUsesUTF16NotUTF8Order(t *testing.T) {
	t.Parallel()
	values := []string{"\uE000", "z", "😀", "a"}
	Sort(values)
	want := []string{"a", "z", "😀", "\uE000"}
	if !slices.Equal(values, want) {
		t.Fatalf("Sort() = %#v, want %#v", values, want)
	}
}
