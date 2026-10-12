package preparedsetup

import (
	"fmt"
	"strings"
	"testing"
)

func verifyFixture(a string) []byte {
	return []byte(fmt.Sprintf("verified %s at level \"full + semantic\"\n  list              %s\n  mode              ngram\n  exact companion   yes (section kind 7)\n  format            KURNART1 1.1\n  entries           24\n  keys              24\n  base id           %s (recomputed from the records)\n  container sha256  %s\nsemantic checks (rebuilt from the records with the engine):\n  config digest     ok  declared %s, recomputed %s\n  analyzer digest   ok  declared %s, recomputed %s\n  index (ngram)     ok  byte-identical to the rebuild\n  exact companion   ok  byte-identical to the rebuild\n  dropped_keys      ok  declared 0, recomputed 0\n  keyless_entries   ok  declared 0, recomputed 0\ndeclared, not verifiable from the artifact:\n  source            \"\"\n  created at        \"\"\n  input sha256      \"%s\"\n  collapsed         0\nnote: verification shows the artifact is self-consistent; it does not authenticate who produced it\n", a, listName, strings.Repeat("a", 64), strings.Repeat("b", 64), configSHA, configSHA, analyzerSHA, analyzerSHA, strings.Repeat("c", 64)))
}
func TestVerifyClosedGrammar(t *testing.T) {
	raw := verifyFixture("/x")
	r, e := parseVerify(raw, "/x")
	if e != nil || r.Entries != 24 {
		t.Fatal(e)
	}
	for _, bad := range [][]byte{raw[:len(raw)-1], []byte(strings.Replace(string(raw), "  entries           24", "  entries           024", 1)), append(append([]byte{}, raw...), []byte("extra\n")...)} {
		if _, e := parseVerify(bad, "/x"); e == nil {
			t.Fatal("accepted drift")
		}
	}
}

func TestBuildClosedThreeLineGrammar(t *testing.T) {
	artifact := "/private/stage/data/" + listName + ".kurn"
	input, container, base := strings.Repeat("a", 64), strings.Repeat("b", 64), strings.Repeat("c", 64)
	want := []byte(fmt.Sprintf("input sha256 %s (preserve this receipt with the source ref)\nartifact %s: list %s, 24 entries, 24 keys, base %s (mode ngram)\ncontainer sha256 %s\n", input, artifact, listName, base, container))
	if err := parseBuild(want, artifact, input, container, base, 24, 24); err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]byte{
		[]byte(strings.Join(strings.Split(string(want), "\n")[1:], "\n")),
		[]byte(strings.Replace(string(want), "container sha256 "+container+"\n", "", 1)),
		[]byte(strings.Replace(string(want), input, strings.Repeat("d", 64), 1)),
		[]byte(strings.Replace(string(want), container, strings.Repeat("d", 64), 1)),
		append(append([]byte{}, want...), []byte("extra\n")...),
	} {
		if err := parseBuild(bad, artifact, input, container, base, 24, 24); err == nil {
			t.Fatal("accepted build transcript drift")
		}
	}
}
