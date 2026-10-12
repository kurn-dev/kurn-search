package partscatalogdiscover

import (
	"encoding/base64"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/kurn-dev/kurn-search/adapter"
	"github.com/kurn-dev/kurn-search/adapter/analyzer"
	"github.com/kurn-dev/kurn-search/internal/jsorder"
)

const (
	kindExact            adapter.EvidenceKind = "exact"
	kindIdentifierPrefix adapter.EvidenceKind = "identifier-prefix"
	kindIdentifierWord   adapter.EvidenceKind = "identifier-word"
	kindNamePrefix       adapter.EvidenceKind = "name-prefix"
	kindNameWord         adapter.EvidenceKind = "name-word"
	listRole             adapter.ListRole     = "catalog"

	maxSourceBytes       = 1 << 20
	maxRequestBytes      = 1 << 20
	maxTrustedScopeBytes = 64 << 10
	maxQueryRunes        = 256
	maxQueryTerms        = 8
	maxTermRunes         = 80
)

var kindOrder = map[adapter.EvidenceKind]int{
	kindExact: 0, kindIdentifierPrefix: 1, kindIdentifierWord: 2, kindNamePrefix: 3, kindNameWord: 4,
}

var keyNamespace = map[adapter.EvidenceKind]string{
	kindExact: "e", kindIdentifierPrefix: "p", kindIdentifierWord: "w", kindNamePrefix: "np", kindNameWord: "nw",
}

func physicalKey(kind adapter.EvidenceKind, logicalTerm string) (string, error) {
	namespace := keyNamespace[kind]
	if namespace == "" || logicalTerm == "" || !utf8.ValidString(logicalTerm) {
		return "", invalidInput()
	}
	key := namespace + ":" + base64.RawURLEncoding.EncodeToString([]byte(logicalTerm))
	if utf8.RuneCountInString(key) > adapter.MaxPhysicalKeyRunes {
		return "", invalidInput()
	}
	return key, nil
}

func analyzed(view analyzer.View, value string) ([]string, error) {
	terms, err := analyzer.Analyze(view, value)
	if err != nil {
		return nil, invalidInput()
	}
	return terms, nil
}

func projectKeys(record sourceRecord) ([]string, error) {
	sets := make(map[string]struct{})
	views := []struct {
		kind  adapter.EvidenceKind
		view  analyzer.View
		value string
	}{
		{kindExact, analyzer.ExactNormalizer, record.Identifier},
		{kindIdentifierPrefix, analyzer.StartsIndex, record.Identifier},
		{kindIdentifierWord, analyzer.WordIndex, record.Identifier},
		{kindNamePrefix, analyzer.StartsIndex, record.Name},
		{kindNameWord, analyzer.WordIndex, record.Name},
	}
	for _, item := range views {
		terms, err := analyzed(item.view, item.value)
		if err != nil {
			return nil, err
		}
		for _, term := range terms {
			if term == "" {
				continue
			}
			key, err := physicalKey(item.kind, term)
			if err != nil {
				return nil, err
			}
			sets[key] = struct{}{}
		}
	}
	keys := make([]string, 0, len(sets))
	for key := range sets {
		keys = append(keys, key)
	}
	jsorder.Sort(keys)
	if len(keys) == 0 || len(keys) > adapter.MaxPhysicalKeys {
		return nil, invalidInput()
	}
	return keys, nil
}

type queryTerm struct {
	prefix string
	words  []string
}

func (term queryTerm) identity() string {
	return term.prefix + "\x00" + strings.Join(term.words, "\x00")
}

func compileQueryTerms(query string) ([]queryTerm, error) {
	if !utf8.ValidString(query) || whitespaceOnly(query) || utf8.RuneCountInString(query) > maxQueryRunes {
		return nil, invalidInput()
	}
	rawTerms := whitespaceTokens(query)
	if len(rawTerms) < 1 || len(rawTerms) > maxQueryTerms {
		return nil, invalidInput()
	}
	seen := make(map[string]struct{}, len(rawTerms))
	terms := make([]queryTerm, 0, len(rawTerms))
	for _, raw := range rawTerms {
		length := utf8.RuneCountInString(raw)
		if length < 1 || length > maxTermRunes {
			return nil, invalidInput()
		}
		term := queryTerm{}
		if length >= 3 && length <= 20 && containsLetterOrNumber(raw) {
			edges, err := analyzed(analyzer.EdgeSearch, raw)
			if err != nil {
				return nil, err
			}
			if len(edges) == 1 && edges[0] != "" {
				term.prefix = edges[0]
			}
		}
		words, err := analyzed(analyzer.WordIndex, raw)
		if err != nil {
			return nil, err
		}
		for _, word := range words {
			if word != "" {
				term.words = append(term.words, word)
			}
		}
		jsorder.Sort(term.words)
		term.words = compactStrings(term.words)
		if term.prefix == "" && len(term.words) == 0 {
			return nil, invalidInput()
		}
		identity := term.identity()
		if _, duplicate := seen[identity]; duplicate {
			continue
		}
		seen[identity] = struct{}{}
		terms = append(terms, term)
	}
	return terms, nil
}

func validQuery(query string) bool {
	_, err := compileQueryTerms(query)
	return err == nil
}

func compactStrings(values []string) []string {
	if len(values) < 2 {
		return values
	}
	result := values[:1]
	for _, value := range values[1:] {
		if value != result[len(result)-1] {
			result = append(result, value)
		}
	}
	return result
}

func containsLetterOrNumber(value string) bool {
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			return true
		}
	}
	return false
}

func whitespaceOnly(value string) bool {
	for _, r := range value {
		if !analyzer.IsWhitespace(r) {
			return false
		}
	}
	return true
}

func whitespaceTokens(value string) []string {
	return strings.FieldsFunc(value, analyzer.IsWhitespace)
}
