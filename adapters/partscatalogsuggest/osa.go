package partscatalogsuggest

// osaLeq1 reports whether the optimal-string-alignment distance between a
// and b, over runes, is at most one: equality, one substitution, one
// insertion or deletion, or one adjacent transposition. It is the policy's
// acceptance distance (Damerau 1964; the restricted form Lucene's
// FuzzyQuery documents), implemented without a matrix because only the
// bound matters; TestOSALeq1AgainstReference pins it to a full-matrix
// reference.
func osaLeq1(a, b string) bool {
	if a == b {
		return true
	}
	ra, rb := []rune(a), []rune(b)
	switch {
	case len(ra) == len(rb):
		i := 0
		for i < len(ra) && ra[i] == rb[i] {
			i++
		}
		if i == len(ra) {
			return true
		}
		if equalRunes(ra[i+1:], rb[i+1:]) {
			return true // one substitution
		}
		return i+1 < len(ra) && ra[i] == rb[i+1] && ra[i+1] == rb[i] && equalRunes(ra[i+2:], rb[i+2:])
	case len(ra) == len(rb)+1:
		return oneInsertion(rb, ra)
	case len(rb) == len(ra)+1:
		return oneInsertion(ra, rb)
	}
	return false
}

// oneInsertion reports whether long is short with exactly one rune inserted.
func oneInsertion(short, long []rune) bool {
	i := 0
	for i < len(short) && short[i] == long[i] {
		i++
	}
	return equalRunes(short[i:], long[i+1:])
}

func equalRunes(a, b []rune) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
