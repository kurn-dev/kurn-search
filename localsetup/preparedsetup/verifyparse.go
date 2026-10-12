package preparedsetup

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

const (
	configSHA   = "5c77cc033a580ee8f654fc6def1559713d527f2d3466006ea30735afc7eaecfc"
	analyzerSHA = "ba7fe7f6aaaef24d85e2db7ede33cc3c3009924863585953eb0fd3648f221bb1"
	listName    = "parts-catalog-suggest-v1"
)

var hex64RE = regexp.MustCompile(`^[0-9a-f]{64}$`)
var decimalRE = regexp.MustCompile(`^(0|[1-9][0-9]*)$`)

type verifyReport struct {
	Entries, Keys          uint64
	Base, Container, Input string
}

func parseBuild(raw []byte, artifact, input, container, base string, entries, keys uint64) error {
	want := fmt.Sprintf("input sha256 %s (preserve this receipt with the source ref)\nartifact %s: list %s, %d entries, %d keys, base %s (mode ngram)\ncontainer sha256 %s\n", input, artifact, listName, entries, keys, base, container)
	if string(raw) != want {
		return errRefused
	}
	return nil
}

func parseVerify(raw []byte, artifact string) (verifyReport, error) {
	// The companion list's transcript: 23 lines, with the exact-companion
	// identity line after the mode and its semantic check after the index.
	lines := strings.Split(string(raw), "\n")
	if len(lines) != 23 || lines[22] != "" {
		return verifyReport{}, errRefused
	}
	fixed := map[int]string{0: `verified ` + artifact + ` at level "full + semantic"`, 1: "  list              " + listName, 2: "  mode              ngram", 3: "  exact companion   yes (section kind 7)", 4: "  format            KURNART1 1.1", 9: "semantic checks (rebuilt from the records with the engine):", 12: "  index (ngram)     ok  byte-identical to the rebuild", 13: "  exact companion   ok  byte-identical to the rebuild", 14: "  dropped_keys      ok  declared 0, recomputed 0", 15: "  keyless_entries   ok  declared 0, recomputed 0", 16: "declared, not verifiable from the artifact:", 17: `  source            ""`, 18: `  created at        ""`, 20: "  collapsed         0", 21: "note: verification shows the artifact is self-consistent; it does not authenticate who produced it"}
	for i, w := range fixed {
		if lines[i] != w {
			return verifyReport{}, errRefused
		}
	}
	entry, ok := valueAfter(lines[5], "  entries           ")
	if !ok {
		return verifyReport{}, errRefused
	}
	keys, ok := valueAfter(lines[6], "  keys              ")
	if !ok {
		return verifyReport{}, errRefused
	}
	e, n1 := parseDecimal(entry)
	k, n2 := parseDecimal(keys)
	if !n1 || !n2 || e == 0 || e > maxRecords {
		return verifyReport{}, errRefused
	}
	base, ok := between(lines[7], "  base id           ", " (recomputed from the records)")
	if !ok || !hex64RE.MatchString(base) {
		return verifyReport{}, errRefused
	}
	container, ok := valueAfter(lines[8], "  container sha256  ")
	if !ok || !hex64RE.MatchString(container) {
		return verifyReport{}, errRefused
	}
	if lines[10] != "  config digest     ok  declared "+configSHA+", recomputed "+configSHA || lines[11] != "  analyzer digest   ok  declared "+analyzerSHA+", recomputed "+analyzerSHA {
		return verifyReport{}, errRefused
	}
	quoted, ok := valueAfter(lines[19], `  input sha256      "`)
	if !ok || !strings.HasSuffix(quoted, `"`) {
		return verifyReport{}, errRefused
	}
	input := strings.TrimSuffix(quoted, `"`)
	if !hex64RE.MatchString(input) {
		return verifyReport{}, errRefused
	}
	return verifyReport{Entries: e, Keys: k, Base: base, Container: container, Input: input}, nil
}

func valueAfter(s, p string) (string, bool) {
	if !strings.HasPrefix(s, p) {
		return "", false
	}
	return strings.TrimPrefix(s, p), true
}
func between(s, p, q string) (string, bool) {
	v, ok := valueAfter(s, p)
	if !ok || !strings.HasSuffix(v, q) {
		return "", false
	}
	return strings.TrimSuffix(v, q), true
}
func parseDecimal(s string) (uint64, bool) {
	if !decimalRE.MatchString(s) {
		return 0, false
	}
	v, e := strconv.ParseUint(s, 10, 53)
	return v, e == nil
}
