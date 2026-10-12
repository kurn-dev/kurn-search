package partscatalogsuggest

import (
	"math/rand"
	"testing"
)

// osaReference is the full-matrix optimal-string-alignment distance over
// runes (substitution, insertion, deletion and adjacent transposition, each
// costing one).
func osaReference(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	n, m := len(ra), len(rb)
	d := make([][]int, n+1)
	for i := range d {
		d[i] = make([]int, m+1)
		d[i][0] = i
	}
	for j := 0; j <= m; j++ {
		d[0][j] = j
	}
	for i := 1; i <= n; i++ {
		for j := 1; j <= m; j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			v := min(d[i-1][j]+1, d[i][j-1]+1, d[i-1][j-1]+cost)
			if i > 1 && j > 1 && ra[i-1] == rb[j-2] && ra[i-2] == rb[j-1] {
				v = min(v, d[i-2][j-2]+1)
			}
			d[i][j] = v
		}
	}
	return d[n][m]
}

// levenshteinReference is the plain edit distance, without transposition.
func levenshteinReference(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	n, m := len(ra), len(rb)
	prev := make([]int, m+1)
	cur := make([]int, m+1)
	for j := 0; j <= m; j++ {
		prev[j] = j
	}
	for i := 1; i <= n; i++ {
		cur[0] = i
		for j := 1; j <= m; j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[m]
}

func TestOSALeq1Directed(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"ax-100", "ax-100", true},
		{"ax-100", "xa-100", true},  // adjacent transposition
		{"ax-100", "ax-010", true},  // adjacent transposition
		{"ax-100", "ax-1000", true}, // insertion
		{"ax-1000", "ax-100", true}, // deletion
		{"ax-100", "ax-101", true},  // substitution
		{"ax-100", "bx-100", true},
		{"ax-100", "ax-001", false}, // two edits (non-adjacent swap)
		{"ax-100", "xa-010", false}, // two transpositions
		{"ax-100", "ax-10", true},
		{"ax-100", "ax-1", false},
		{"ax-100", "ax100", true},
		{"ax-100", "ax 100", true},
		{"", "a", true},
		{"", "", true},
		{"", "ab", false},
		{"abc", "cba", false},
		{"ab", "ba", true},
		{"ab", "ab ", true},
		{"zb-9000", "zb-0009", false},
		{"gx-200", "gx-201", true},
		{"zb-9199", "zb-9119", true},
		{"ß1", "1ß", true}, // non-ASCII transposition
		{"aé", "éa", true}, // non-ASCII transposition
		{"é", "e", true},   // substitution of a multi-byte rune
		{"abé", "abéx", true},
		{"ab", "baé", false},
	} {
		if got := osaLeq1(tc.a, tc.b); got != tc.want {
			t.Errorf("osaLeq1(%q, %q) = %v, want %v (reference %d)", tc.a, tc.b, got, tc.want, osaReference(tc.a, tc.b))
		}
		if got := osaLeq1(tc.b, tc.a); got != tc.want {
			t.Errorf("osaLeq1(%q, %q) = %v, want %v (symmetry)", tc.b, tc.a, got, tc.want)
		}
	}
	// Transposition is one OSA edit but two Levenshtein edits.
	if levenshteinReference("ax-100", "xa-100") != 2 || osaReference("ax-100", "xa-100") != 1 {
		t.Fatal("reference distances disagree with Damerau's definition")
	}
}

// TestOSALeq1AgainstReference pins the bound to the full-matrix reference
// over random pairs drawn from a small alphabet so that every edit class
// appears, including pairs whose transposition is the only distance-1 path.
func TestOSALeq1AgainstReference(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	alphabet := []rune("ab1-é ")
	random := func() string {
		n := rng.Intn(7)
		r := make([]rune, n)
		for i := range r {
			r[i] = alphabet[rng.Intn(len(alphabet))]
		}
		return string(r)
	}
	mutate := func(s string) string {
		r := []rune(s)
		switch rng.Intn(4) {
		case 0:
			if len(r) > 0 {
				r[rng.Intn(len(r))] = alphabet[rng.Intn(len(alphabet))]
			}
		case 1:
			i := rng.Intn(len(r) + 1)
			r = append(r[:i], append([]rune{alphabet[rng.Intn(len(alphabet))]}, r[i:]...)...)
		case 2:
			if len(r) > 0 {
				i := rng.Intn(len(r))
				r = append(r[:i], r[i+1:]...)
			}
		case 3:
			if len(r) > 1 {
				i := rng.Intn(len(r) - 1)
				r[i], r[i+1] = r[i+1], r[i]
			}
		}
		return string(r)
	}
	pairs, within, transpositionOnly := 0, 0, 0
	for i := 0; i < 50000; i++ {
		a := random()
		b := random()
		if i%2 == 0 {
			b = mutate(a)
		}
		want := osaReference(a, b) <= 1
		if got := osaLeq1(a, b); got != want {
			t.Fatalf("osaLeq1(%q, %q) = %v, reference distance %d", a, b, got, osaReference(a, b))
		}
		pairs++
		if want {
			within++
			if levenshteinReference(a, b) > 1 {
				transpositionOnly++
			}
		}
	}
	if within < 1000 || transpositionOnly < 100 {
		t.Fatalf("weak coverage: %d pairs, %d within one edit, %d only by transposition", pairs, within, transpositionOnly)
	}
}
