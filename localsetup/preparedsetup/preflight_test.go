package preparedsetup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPreflightSentinel(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s")
	line := []byte("{\"category\":\"sentinel\",\"id\":\"A01\",\"identifier\":\"ZS-901\",\"manufacturer\":\"Northstar\",\"partition_id\":\"north\",\"status\":\"active\",\"tenant_id\":\"alpha\"}\n")
	os.WriteFile(p, line, 0600)
	s, _ := readSource(p)
	r, e := preflight(s, "alpha", "north")
	if e != nil || r.expectedID != "A01" {
		t.Fatal(e)
	}
	s.records = append(s.records, s.records[0])
	if _, e := preflight(s, "alpha", "north"); e == nil {
		t.Fatal("two sentinels accepted")
	}
}

func TestPreflightBoundaryRefusals(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s")
	write := func(lines string) sourceSet {
		t.Helper()
		if err := os.WriteFile(p, []byte(lines), 0600); err != nil {
			t.Fatal(err)
		}
		s, err := readSource(p)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	ordinary := "{\"category\":\"ordinary\",\"id\":\"A02\",\"identifier\":\"AB-2\",\"manufacturer\":\"M\",\"partition_id\":\"south\",\"status\":\"active\",\"tenant_id\":\"alpha\"}\n"
	if _, err := preflight(write(ordinary), "alpha", "north"); err == nil {
		t.Fatal("accepted zero sentinel rows")
	}
	overlong := "{\"category\":\"sentinel\",\"id\":\"A01\",\"identifier\":\"" + strings.Repeat("x", 257) + "\",\"manufacturer\":\"M\",\"partition_id\":\"north\",\"status\":\"active\",\"tenant_id\":\"alpha\"}\n"
	if _, err := preflight(write(overlong), "alpha", "north"); err == nil {
		t.Fatal("accepted 257-rune sentinel identifier")
	}
}

func TestPreflightQueryRuneBoundaries(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s")
	for _, runes := range []int{128, 256, 512} {
		line := "{\"category\":\"sentinel\",\"id\":\"A01\",\"identifier\":\"" + strings.Repeat("Ω", runes) + "\",\"manufacturer\":\"M\",\"partition_id\":\"north\",\"status\":\"active\",\"tenant_id\":\"alpha\"}\n"
		if err := os.WriteFile(p, []byte(line), 0600); err != nil {
			t.Fatal(err)
		}
		s, err := readSource(p)
		if err != nil {
			t.Fatalf("source %d: %v", runes, err)
		}
		_, err = preflight(s, "alpha", "north")
		if (runes <= 256) != (err == nil) {
			t.Fatalf("query rune boundary %d: %v", runes, err)
		}
	}
}
