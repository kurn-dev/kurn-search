package kurnhttp

import (
	"regexp"
	"strconv"
)

var storeStampRE = regexp.MustCompile(`^([0-9a-f]{64})@([0-9]+)\+j(0|([1-9][0-9]*)\.([0-9a-f]{64}))\+c([0-9a-f]{64})$`)

// Stamp is a parsed non-empty, configuration-bearing store version.
type Stamp struct {
	Raw           string
	BaseSHA256    string
	Entries       uint64
	JournalBytes  uint64
	JournalSHA256 string
	ConfigSHA256  string
}

func ParseStamp(raw string) (Stamp, error) {
	m := storeStampRE.FindStringSubmatch(raw)
	if m == nil {
		return Stamp{}, classified(CategoryListIdentityMismatch, 0, nil)
	}
	entries, err := strconv.ParseUint(m[2], 10, 64)
	if err != nil {
		return Stamp{}, classified(CategoryListIdentityMismatch, 0, nil)
	}
	var journal uint64
	if m[3] != "0" {
		journal, err = strconv.ParseUint(m[4], 10, 64)
		if err != nil {
			return Stamp{}, classified(CategoryListIdentityMismatch, 0, nil)
		}
	}
	return Stamp{
		Raw: raw, BaseSHA256: m[1], Entries: entries,
		JournalBytes: journal, JournalSHA256: m[5], ConfigSHA256: m[6],
	}, nil
}
