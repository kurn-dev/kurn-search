package partscatalog

import (
	"encoding/base64"
	"strings"
	"unicode/utf8"

	"github.com/kurn-dev/kurn-search/adapter"
	"github.com/kurn-dev/kurn-search/adapter/analyzer"
	"github.com/kurn-dev/kurn-search/internal/jsorder"
)

const (
	kindExact  adapter.EvidenceKind = "exact"
	kindStarts adapter.EvidenceKind = "starts"
	kindEnds   adapter.EvidenceKind = "ends"
	kindWord   adapter.EvidenceKind = "word"
	listRole   adapter.ListRole     = "identifiers"

	maxSourceBytes       = 1 << 20
	maxRequestBytes      = 1 << 20
	maxTrustedScopeBytes = 64 << 10
)

var kindOrder = map[adapter.EvidenceKind]int{
	kindExact: 0, kindStarts: 1, kindEnds: 2, kindWord: 3,
}

func physicalKey(kind adapter.EvidenceKind, logicalTerm string) (string, error) {
	namespace := map[adapter.EvidenceKind]string{
		kindExact: "e", kindStarts: "p", kindEnds: "s", kindWord: "w",
	}[kind]
	if namespace == "" || !utf8.ValidString(logicalTerm) {
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

func projectKeys(identifier string) ([]string, error) {
	sets := make(map[string]struct{})
	views := []struct {
		kind adapter.EvidenceKind
		view analyzer.View
	}{
		{kindExact, analyzer.ExactNormalizer},
		{kindStarts, analyzer.StartsIndex},
		{kindEnds, analyzer.EndsIndex},
		{kindWord, analyzer.WordIndex},
	}
	for _, item := range views {
		terms, err := analyzed(item.view, identifier)
		if err != nil {
			return nil, err
		}
		for _, term := range terms {
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
