// Package analyzer reproduces the frozen W3 local analyzer semantics.
//
// It is deliberately not a Lucene analyzer or an Elasticsearch compatibility
// layer. Inputs must be valid UTF-8, and every operation is deterministic and
// free of I/O, environment, clock, randomness, and mutable global state.
package analyzer

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/clipperhouse/uax29/v2/words"
	"github.com/kurn-dev/kurn-search/internal/jsorder"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	"golang.org/x/text/unicode/norm"
)

// View identifies one of the five frozen analyzer projections.
type View string

const (
	ExactNormalizer View = "exact-normalizer"
	StartsIndex     View = "starts-index"
	EndsIndex       View = "ends-index"
	EdgeSearch      View = "edge-search"
	WordIndex       View = "word-index"
)

// Error is a stable analyzer refusal without mutable package state.
type Error string

func (err Error) Error() string { return string(err) }

const (
	ErrInvalidUTF8 Error = "analyzer input must be valid UTF-8"
	ErrUnknownView Error = "unknown analyzer view"
)

// Views returns the five views in the frozen B1 order.
func Views() []View {
	return []View{ExactNormalizer, StartsIndex, EndsIndex, EdgeSearch, WordIndex}
}

// Analyze projects value through one frozen analyzer view.
func Analyze(view View, value string) ([]string, error) {
	if !utf8.ValidString(value) {
		return nil, ErrInvalidUTF8
	}

	switch view {
	case ExactNormalizer:
		return []string{lowercaseASCIIFold(value)}, nil
	case StartsIndex:
		return indexedStartsTerms(value), nil
	case EndsIndex:
		return indexedEndsTerms(value), nil
	case EdgeSearch:
		return edgeSearchTerms(value), nil
	case WordIndex:
		return standardTokens(value), nil
	default:
		return nil, ErrUnknownView
	}
}

func lowercaseASCIIFold(value string) string {
	decomposed := norm.NFKD.String(cases.Lower(language.Und).String(value))
	var result strings.Builder
	result.Grow(len(decomposed))
	for _, r := range decomposed {
		if unicode.Is(unicode.M, r) {
			continue
		}
		result.WriteString(extraASCIIFold(r))
	}
	return result.String()
}

func extraASCIIFold(r rune) string {
	switch r {
	case 'æ':
		return "ae"
	case 'œ':
		return "oe"
	case 'ø':
		return "o"
	case 'ł':
		return "l"
	case 'đ', 'ð':
		return "d"
	case 'þ':
		return "th"
	case 'ß':
		return "ss"
	case 'ı':
		return "i"
	case 'ŧ':
		return "t"
	case 'ŋ':
		return "n"
	case 'ħ':
		return "h"
	case 'ĸ':
		return "k"
	case 'ſ':
		return "s"
	default:
		return string(r)
	}
}

func whitespaceTokens(value string) []string {
	return strings.FieldsFunc(value, IsWhitespace)
}

// IsWhitespace is the frozen JavaScript-trim-compatible whitespace seam used
// by both analyzer tokenization and concrete adapter query validation.
func IsWhitespace(r rune) bool { return unicode.IsSpace(r) || r == '\uFEFF' }

func truncateRunes(value string, length int) string {
	runes := []rune(value)
	if len(runes) <= length {
		return value
	}
	return string(runes[:length])
}

func reverseRunes(value string) string {
	runes := []rune(value)
	for left, right := 0, len(runes)-1; left < right; left, right = left+1, right-1 {
		runes[left], runes[right] = runes[right], runes[left]
	}
	return string(runes)
}

func addPrefixes(terms map[string]struct{}, value string) {
	runes := []rune(value)
	maximum := min(20, len(runes))
	for length := 3; length <= maximum; length++ {
		terms[string(runes[:length])] = struct{}{}
	}
}

func indexedStartsTerms(value string) []string {
	terms := make(map[string]struct{})
	for _, token := range whitespaceTokens(value) {
		normalized := lowercaseASCIIFold(truncateRunes(token, 20))
		addPrefixes(terms, normalized)
	}
	return sortedTerms(terms)
}

func indexedEndsTerms(value string) []string {
	terms := make(map[string]struct{})
	for _, token := range whitespaceTokens(value) {
		reversed := reverseRunes(token)
		normalized := lowercaseASCIIFold(truncateRunes(reversed, 20))
		edges := make(map[string]struct{})
		addPrefixes(edges, normalized)
		for edge := range edges {
			terms[reverseRunes(edge)] = struct{}{}
		}
	}
	return sortedTerms(terms)
}

func edgeSearchTerms(value string) []string {
	terms := make(map[string]struct{})
	for _, token := range whitespaceTokens(value) {
		terms[lowercaseASCIIFold(truncateRunes(token, 20))] = struct{}{}
	}
	return sortedTerms(terms)
}

func standardTokens(value string) []string {
	terms := make(map[string]struct{})
	segments := words.FromString(value)
	for segments.Next() {
		segment := segments.Value()
		if !isWordLike(segment) {
			continue
		}
		var lowered strings.Builder
		lowered.Grow(len(segment))
		for _, r := range segment {
			lowered.WriteRune(unicode.ToLower(r))
		}
		if token := lowered.String(); token != "" {
			terms[token] = struct{}{}
		}
	}
	return sortedTerms(terms)
}

func isWordLike(segment string) bool {
	for _, r := range segment {
		if unicode.IsLetter(r) || unicode.Is(unicode.Nd, r) || unicode.Is(unicode.Nl, r) {
			return true
		}
	}
	return false
}

func sortedTerms(terms map[string]struct{}) []string {
	result := make([]string, 0, len(terms))
	for term := range terms {
		result = append(result, term)
	}
	jsorder.Sort(result)
	return result
}
