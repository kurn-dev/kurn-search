package analyzer_test

import (
	"errors"
	"testing"

	"github.com/kurn-dev/kurn-search/adapter/analyzer"
)

func TestWhitespaceIncludesBOM(t *testing.T) {
	terms, err := analyzer.Analyze(analyzer.StartsIndex, "ABC\uFEFFXYZ")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"abc", "xyz"}
	assertTerms(t, terms, want)
}

func TestOrderingUsesUTF16CodeUnits(t *testing.T) {
	terms, err := analyzer.Analyze(analyzer.EdgeSearch, "\uE000 😀")
	if err != nil {
		t.Fatal(err)
	}
	// JavaScript compares the emoji's leading surrogate D83D before E000.
	assertTerms(t, terms, []string{"😀", "\uE000"})
}

func TestWordLikeExcludesOtherNumber(t *testing.T) {
	terms, err := analyzer.Analyze(analyzer.WordIndex, "² ½ Ⅳ ٣ _")
	if err != nil {
		t.Fatal(err)
	}
	assertTerms(t, terms, []string{"٣", "ⅳ"})
}

func TestAnalyzeRefusesInvalidInputAndView(t *testing.T) {
	if _, err := analyzer.Analyze(analyzer.WordIndex, string([]byte{0xff})); !errors.Is(err, analyzer.ErrInvalidUTF8) {
		t.Fatalf("invalid UTF-8 error = %v", err)
	}
	if _, err := analyzer.Analyze(analyzer.View("unknown"), "ABC"); !errors.Is(err, analyzer.ErrUnknownView) {
		t.Fatalf("unknown view error = %v", err)
	}
}

func TestViewsAreFrozenAndFresh(t *testing.T) {
	first := analyzer.Views()
	assertViews(t, first, []analyzer.View{
		analyzer.ExactNormalizer,
		analyzer.StartsIndex,
		analyzer.EndsIndex,
		analyzer.EdgeSearch,
		analyzer.WordIndex,
	})
	first[0] = "changed"
	if analyzer.Views()[0] != analyzer.ExactNormalizer {
		t.Fatal("Views returned shared mutable state")
	}
}

func assertTerms(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("terms = %#v, want %#v", got, want)
	}
	for index := range got {
		if got[index] != want[index] {
			t.Fatalf("terms = %#v, want %#v", got, want)
		}
	}
}

func assertViews(t *testing.T, got, want []analyzer.View) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("views = %#v, want %#v", got, want)
	}
	for index := range got {
		if got[index] != want[index] {
			t.Fatalf("views = %#v, want %#v", got, want)
		}
	}
}
