// Package membershipjson is the bounded canonical string-membership codec
// shared only by the new private plan and backend paths. It is not a general
// public typed-filter parser and never accepts numeric or boolean alternatives.
package membershipjson

import (
	"errors"
	"slices"
	"sort"
	"unicode/utf8"
)

const (
	MaxTerms          = 8
	MaxNameRunes      = 128
	MaxValueRunes     = 512
	MaxAlternatives   = 16
	MaxCanonicalBytes = 16 << 10
)

var errInvalid = errors.New("membership filter: invalid expression")

// Term is constructor input, not an immutable validated filter. Values are
// supplied alternatives; their count is checked before normalization.
type Term struct {
	Name   string
	Values []string
}

// Normalize validates the entire supplied shape, copies and byte-sorts its
// terms/members, removes exact duplicates, and returns bounded canonical JSON.
// Empty input has no applied predicate and therefore returns nil JSON.
// All returned slices are independent of the input.
func Normalize(input []Term) ([]Term, []byte, error) {
	if len(input) > MaxTerms {
		return nil, nil, errInvalid
	}
	terms := make([]Term, len(input))
	seen := make(map[string]struct{}, len(input))
	for i, term := range input {
		if !bounded(term.Name, MaxNameRunes) || len(term.Values) == 0 || len(term.Values) > MaxAlternatives {
			return nil, nil, errInvalid
		}
		if _, exists := seen[term.Name]; exists {
			return nil, nil, errInvalid
		}
		seen[term.Name] = struct{}{}
		for _, value := range term.Values {
			if !bounded(value, MaxValueRunes) {
				return nil, nil, errInvalid
			}
		}
		values := slices.Clone(term.Values)
		sort.Strings(values)
		terms[i] = Term{Name: term.Name, Values: slices.Compact(values)}
	}
	sort.Slice(terms, func(i, j int) bool { return terms[i].Name < terms[j].Name })
	if len(terms) == 0 {
		return terms, nil, nil
	}
	buf := []byte{'{'}
	for i, term := range terms {
		if i != 0 {
			buf = append(buf, ',')
		}
		buf = appendString(buf, term.Name)
		buf = append(buf, ':')
		if len(term.Values) > 1 {
			buf = append(buf, "{\"in\":["...)
		}
		for j, value := range term.Values {
			if j != 0 {
				buf = append(buf, ',')
			}
			buf = appendString(buf, value)
			if len(buf) > MaxCanonicalBytes {
				return nil, nil, errInvalid
			}
		}
		if len(term.Values) > 1 {
			buf = append(buf, ']', '}')
		}
	}
	buf = append(buf, '}')
	if len(buf) > MaxCanonicalBytes {
		return nil, nil, errInvalid
	}
	return terms, buf, nil
}

func bounded(value string, runes int) bool {
	return value != "" && len(value) <= runes*utf8.UTFMax && utf8.ValidString(value) && utf8.RuneCountInString(value) <= runes
}

// Inputs have already been checked for valid UTF-8. Preserve all non-ASCII
// bytes and use only the public canonical codec's mandatory/short escapes.
func appendString(dst []byte, value string) []byte {
	dst = append(dst, '"')
	for i := 0; i < len(value); i++ {
		c := value[i]
		switch c {
		case '"', '\\':
			dst = append(dst, '\\', c)
		case '\b':
			dst = append(dst, '\\', 'b')
		case '\f':
			dst = append(dst, '\\', 'f')
		case '\n':
			dst = append(dst, '\\', 'n')
		case '\r':
			dst = append(dst, '\\', 'r')
		case '\t':
			dst = append(dst, '\\', 't')
		default:
			if c < 0x20 {
				const hex = "0123456789abcdef"
				dst = append(dst, '\\', 'u', '0', '0', hex[c>>4], hex[c&15])
			} else {
				dst = append(dst, c)
			}
		}
	}
	return append(dst, '"')
}
