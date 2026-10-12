package analyzer_test

import (
	"runtime"
	"testing"
	"unicode"

	"github.com/clipperhouse/uax29/v2/words"
	"github.com/kurn-dev/kurn-search/adapter/analyzer"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	"golang.org/x/text/unicode/norm"
	"golang.org/x/text/unicode/rangetable"
)

func TestActiveUnicode17Authorities(t *testing.T) {
	if runtime.Version() != "go1.27.0" {
		t.Fatalf("runtime = %s, want go1.27.0", runtime.Version())
	}
	if unicode.Version != "17.0.0" || cases.UnicodeVersion != "17.0.0" ||
		norm.Version != "17.0.0" {
		t.Fatalf("unicode versions stdlib=%s cases=%s norm=%s",
			unicode.Version, cases.UnicodeVersion, norm.Version)
	}
}

func TestUnicode17BehavioralCanaries(t *testing.T) {
	assigned15 := rangetable.Assigned("15.0.0")
	assigned16 := rangetable.Assigned("16.0.0")
	assigned17 := rangetable.Assigned("17.0.0")

	if unicode.Is(assigned15, '\uA7CE') || !unicode.Is(assigned17, '\uA7CE') {
		t.Fatal("U+A7CE age derivation failed")
	}
	if got := cases.Lower(language.Und).String("\uA7CE"); got != "\uA7CF" {
		t.Fatalf("full lowercase canary = %U", []rune(got))
	}
	if unicode.Is(assigned16, '\uA7F1') || !unicode.Is(assigned17, '\uA7F1') {
		t.Fatal("U+A7F1 age derivation failed")
	}
	if got := norm.NFKD.String("\uA7F1"); got != "S" {
		t.Fatalf("NFKD canary = %U", []rune(got))
	}
	if unicode.Is(assigned15, '\u1C89') || !unicode.Is(assigned16, '\u1C89') ||
		!oneWord("a\u1C89b") {
		t.Fatal("Unicode-16 word-boundary canary failed")
	}
	if unicode.Is(assigned16, '\u088F') || !unicode.Is(assigned17, '\u088F') ||
		!oneWord("a\u088Fb") {
		t.Fatal("Unicode-17 word-boundary canary failed")
	}
}

func TestFrozenRuntimeAnchors(t *testing.T) {
	dottedI, err := analyzer.Analyze(analyzer.WordIndex, "İST")
	if err != nil {
		t.Fatal(err)
	}
	assertTerms(t, dottedI, []string{"ist"})
	astral, err := analyzer.Analyze(analyzer.WordIndex, "LOT😀TAIL")
	if err != nil {
		t.Fatal(err)
	}
	assertTerms(t, astral, []string{"lot", "tail"})
}

func oneWord(value string) bool {
	iterator := words.FromString(value)
	count := 0
	observed := ""
	for iterator.Next() {
		count++
		observed = iterator.Value()
	}
	return count == 1 && observed == value
}
