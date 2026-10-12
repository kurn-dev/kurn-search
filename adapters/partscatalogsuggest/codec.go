package partscatalogsuggest

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/kurn-dev/kurn-search/adapter"
	"github.com/kurn-dev/kurn-search/adapter/analyzer"
)

const (
	kindSimilarity    adapter.EvidenceKind = "similarity"
	kindTransposition adapter.EvidenceKind = "transposition"
	listRole          adapter.ListRole     = "identifiers"

	// maxTranspositions caps the fallback's variant list (the evaluated
	// cap; a longer identifier's later positions are not tried).
	maxTranspositions = 24
	// minSimilarityScore is the lowest score the similarity probe can
	// return under the recipe's default threshold
	// (similarityThresholdMillionths).
	minSimilarityScore = 65

	// PolicyOSA1 is the opt-in experimental acceptance policy: candidates
	// are generated at generationThresholdMillionths and a similarity
	// candidate is accepted only when one of its recipe keys is within
	// optimal-string-alignment edit distance one of the analyzed query;
	// the transposition fallback is taken when no candidate is accepted.
	PolicyOSA1 = "osa1"
	// generationThresholdMillionths is the policy's similarity threshold,
	// and generationMinScore the lowest score it can return.
	generationThresholdMillionths = uint32(450_000)
	generationMinScore            = 45

	// PolicyOSA1G60 is the same verifier with generation at 0.60: the
	// one-factor comparison against PolicyOSA1 and the default. It is its
	// own policy identity (diagnostic, threshold, score floor); its
	// acceptance behaviour is PolicyOSA1's.
	PolicyOSA1G60                   = "osa1-060"
	generation60ThresholdMillionths = uint32(600_000)
	generation60MinScore            = 60

	// PolicyOSA1G60Union is PolicyOSA1G60 with the transposition any-of
	// issued for every query that has variants, whether or not a
	// similarity candidate was accepted, and its hits combined with the
	// accepted similarity hits (one entry once, the transposition evidence
	// winning) before ranking and the response limit. Same generation
	// threshold, score floor and acceptance predicate as PolicyOSA1G60;
	// its own policy identity.
	PolicyOSA1G60Union = "osa1-060-union"

	maxSourceBytes       = 1 << 20
	maxRequestBytes      = 1 << 20
	maxTrustedScopeBytes = 64 << 10
)

func projectKeys(identifier string) ([]string, error) {
	if !utf8.ValidString(identifier) || identifier == "" ||
		utf8.RuneCountInString(identifier) > adapter.MaxPhysicalKeyRunes {
		return nil, invalidInput()
	}
	return []string{identifier}, nil
}

func normalizedIdentifier(value string) string {
	return strings.Join(strings.Fields(strings.ToLower(value)), " ")
}

// analyzedKey mirrors the list's declared analyzer steps (lowercase, trim):
// the equality the engine's exact index applies between keys and any-of
// terms.
func analyzedKey(value string) string { return strings.ToLower(strings.TrimSpace(value)) }

func alphanumeric(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

// transpositions returns the adjacent-transposition variants of query: for
// every adjacent pair of differing alphanumeric runes, in position order,
// the query with the two swapped, without repeats and at most
// maxTranspositions of them. The query itself is never a variant.
func transpositions(query string) []string {
	runes := []rune(query)
	var out []string
	seen := make(map[string]struct{})
	for i := 0; i+1 < len(runes) && len(out) < maxTranspositions; i++ {
		a, b := runes[i], runes[i+1]
		if a == b || !alphanumeric(a) || !alphanumeric(b) {
			continue
		}
		runes[i], runes[i+1] = b, a
		variant := string(runes)
		runes[i], runes[i+1] = a, b
		if _, dup := seen[variant]; dup {
			continue
		}
		seen[variant] = struct{}{}
		out = append(out, variant)
	}
	return out
}

// validPolicy reports whether policy is the default ("") or a known opt-in.
func validPolicy(policy string) bool {
	return policy == "" || policy == PolicyOSA1 || policy == PolicyOSA1G60 || policy == PolicyOSA1G60Union
}

// verifierPolicy reports whether policy accepts by the OSA-1 verifier.
func verifierPolicy(policy string) bool {
	return policy == PolicyOSA1 || policy == PolicyOSA1G60 || policy == PolicyOSA1G60Union
}

// unionPolicy reports whether policy always takes the transposition step
// for a query with variants and combines both branches.
func unionPolicy(policy string) bool { return policy == PolicyOSA1G60Union }

// policyThreshold is the similarity threshold a policy generates candidates
// at, and policyMinScore the lowest similarity score it accepts.
func policyThreshold(policy string) uint32 {
	switch policy {
	case PolicyOSA1:
		return generationThresholdMillionths
	case PolicyOSA1G60, PolicyOSA1G60Union:
		return generation60ThresholdMillionths
	}
	return similarityThresholdMillionths
}

func policyMinScore(policy string) float64 {
	switch policy {
	case PolicyOSA1:
		return generationMinScore
	case PolicyOSA1G60, PolicyOSA1G60Union:
		return generation60MinScore
	}
	return minSimilarityScore
}

// accepts is the shared acceptance predicate of a similarity candidate: the
// default policy accepts every structurally valid candidate the score
// bounds admit; a verifier policy accepts only an identifier within
// optimal-string-alignment distance one of the query under the analyzer.
func accepts(policy, query string, keys []string) bool {
	if !verifierPolicy(policy) {
		return true
	}
	want := analyzedKey(query)
	for _, key := range keys {
		if osaLeq1(want, analyzedKey(key)) {
			return true
		}
	}
	return false
}

// matchesVariant reports whether identifier equals one of keys under the
// list's analyzer.
func matchesVariant(keys []string, identifier string) bool {
	want := analyzedKey(identifier)
	for _, key := range keys {
		if analyzedKey(key) == want {
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
