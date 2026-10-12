package analyzer

import (
	_ "embed"
	"slices"
)

//go:embed testdata/neutral-golden.json
var neutralGolden []byte

// NeutralGolden returns a defensive copy of the exact Slice-0 analyzer output
// artifact used by concrete behavior descriptors.
func NeutralGolden() []byte { return slices.Clone(neutralGolden) }
