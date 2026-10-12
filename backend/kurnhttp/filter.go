package kurnhttp

import (
	"bytes"
	"sort"
	"unicode/utf8"
)

const (
	maxFilterTerms      = 8
	maxFilterNameRunes  = 128
	maxFilterValueRunes = 512
)

// FilterTerm is one exact-string filter term. NewStringFilter rejects
// duplicate names before request serialization.
type FilterTerm struct {
	Name  string
	Value string
}

// StringFilter is an immutable exact-string expression with canonical Kurn
// wire bytes. Its zero value is the unfiltered expression.
type StringFilter struct {
	terms     []FilterTerm
	canonical []byte
}

func NewStringFilter(terms []FilterTerm) (StringFilter, error) {
	if len(terms) == 0 {
		return StringFilter{}, nil
	}
	if len(terms) > maxFilterTerms {
		return StringFilter{}, classified(CategoryInvalidRequest, 0, nil)
	}
	copyTerms := append([]FilterTerm(nil), terms...)
	seen := make(map[string]struct{}, len(copyTerms))
	for i := range copyTerms {
		term := copyTerms[i]
		if !utf8.ValidString(term.Name) || !utf8.ValidString(term.Value) ||
			term.Name == "" || utf8.RuneCountInString(term.Name) > maxFilterNameRunes ||
			utf8.RuneCountInString(term.Value) > maxFilterValueRunes {
			return StringFilter{}, classified(CategoryInvalidRequest, 0, nil)
		}
		if _, exists := seen[term.Name]; exists {
			return StringFilter{}, classified(CategoryInvalidRequest, 0, nil)
		}
		seen[term.Name] = struct{}{}
	}
	sort.Slice(copyTerms, func(i, j int) bool { return copyTerms[i].Name < copyTerms[j].Name })

	buf := make([]byte, 0, 2+len(copyTerms)*8)
	buf = append(buf, '{')
	for i, term := range copyTerms {
		if i > 0 {
			buf = append(buf, ',')
		}
		buf = appendCanonicalString(buf, term.Name)
		buf = append(buf, ':')
		buf = appendCanonicalString(buf, term.Value)
	}
	buf = append(buf, '}')
	return StringFilter{terms: copyTerms, canonical: buf}, nil
}

func (f StringFilter) Empty() bool { return len(f.terms) == 0 }

func (f StringFilter) CanonicalJSON() []byte { return bytes.Clone(f.canonical) }

func (f StringFilter) Terms() []FilterTerm { return append([]FilterTerm(nil), f.terms...) }

func appendCanonicalString(dst []byte, s string) []byte {
	dst = append(dst, '"')
	for i := 0; i < len(s); i++ {
		c := s[i]
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
				dst = append(dst, '\\', 'u', '0', '0', hex[c>>4], hex[c&0xf])
			} else {
				dst = append(dst, c)
			}
		}
	}
	return append(dst, '"')
}
