// Package jsorder implements JavaScript UTF-16 code-unit string ordering.
// It is the shared ordering seam for frozen cross-runtime adapter semantics.
package jsorder

import (
	"sort"
	"unicode/utf16"
)

// Compare returns -1, 0, or 1 using JavaScript's lexicographic UTF-16
// code-unit order.
func Compare(left, right string) int {
	a := utf16.Encode([]rune(left))
	b := utf16.Encode([]rune(right))
	limit := min(len(a), len(b))
	for index := 0; index < limit; index++ {
		if a[index] < b[index] {
			return -1
		}
		if a[index] > b[index] {
			return 1
		}
	}
	if len(a) < len(b) {
		return -1
	}
	if len(a) > len(b) {
		return 1
	}
	return 0
}

// Less reports whether left precedes right in JavaScript UTF-16 order.
func Less(left, right string) bool { return Compare(left, right) < 0 }

// Sort sorts strings in place in JavaScript UTF-16 order.
func Sort(values []string) {
	sort.SliceStable(values, func(i, j int) bool { return Less(values[i], values[j]) })
}
