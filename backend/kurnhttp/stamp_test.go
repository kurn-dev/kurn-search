package kurnhttp

import (
	"strings"
	"testing"
)

func TestParseStampValid(t *testing.T) {
	base := strings.Repeat("a", 64)
	journal := strings.Repeat("b", 64)
	config := strings.Repeat("c", 64)
	tests := []struct {
		raw          string
		entries      uint64
		journalBytes uint64
		journalHash  string
	}{
		{raw: base + "@0+j0+c" + config},
		{raw: base + "@0007+j9." + journal + "+c" + config, entries: 7, journalBytes: 9, journalHash: journal},
		{raw: base + "@18446744073709551615+j18446744073709551615." + journal + "+c" + config, entries: ^uint64(0), journalBytes: ^uint64(0), journalHash: journal},
	}
	for _, test := range tests {
		stamp, err := ParseStamp(test.raw)
		if err != nil {
			t.Fatalf("ParseStamp(%q): %v", test.raw, err)
		}
		if stamp.Raw != test.raw || stamp.BaseSHA256 != base || stamp.Entries != test.entries ||
			stamp.JournalBytes != test.journalBytes || stamp.JournalSHA256 != test.journalHash ||
			stamp.ConfigSHA256 != config {
			t.Fatalf("parsed stamp = %#v", stamp)
		}
	}
}

func TestParseStampInvalidCorpus(t *testing.T) {
	base := strings.Repeat("a", 64)
	journal := strings.Repeat("b", 64)
	config := strings.Repeat("c", 64)
	valid := base + "@1+j2." + journal + "+c" + config
	invalid := []string{
		"", "empty@0+j0+c" + config, "gen1", base + "@1+j0", base + "@1+j0+c",
		strings.ToUpper(valid), " " + valid, valid + " ", valid + "suffix",
		base[:63] + "@1+j0+c" + config, base + "@x+j0+c" + config,
		base + "@18446744073709551616+j0+c" + config,
		base + "@1+j01." + journal + "+c" + config,
		base + "@1+j0." + journal + "+c" + config,
		base + "@1+j2+c" + config,
		base + "@1+j2." + journal[:63] + "+c" + config,
		base + "@1+j18446744073709551616." + journal + "+c" + config,
		base + "@1+j2." + journal + "+c" + config[:63],
	}
	for _, raw := range invalid {
		_, err := ParseStamp(raw)
		requireCategory(t, err, CategoryListIdentityMismatch)
	}
}
