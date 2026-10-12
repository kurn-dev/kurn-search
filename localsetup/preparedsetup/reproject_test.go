package preparedsetup

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestReprojectMatchesPrepareProjection(t *testing.T) {
	source := "{\"tenant_id\":\"alpha\",\"status\":\"active\",\"manufacturer\":\"Northstar\",\"identifier\":\"AX-100\",\"id\":\"A01\",\"category\":\"bearing\"}\n" +
		"{\"category\":\"seal\",\"id\":\"A02\",\"identifier\":\"BX-200\",\"manufacturer\":\"Southwind\",\"partition_id\":\"north\",\"status\":\"discontinued\",\"tenant_id\":\"beta\"}\n"
	path := filepath.Join(t.TempDir(), "source.ndjson")
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	set, err := readSource(path)
	if err != nil {
		t.Fatal(err)
	}
	want, wantSHA, wantKeys, err := serializeProjected(set)
	if err != nil {
		t.Fatal(err)
	}
	got, sha, keys, err := Reproject(path)
	if err != nil || !bytes.Equal(got, want) || sha != wantSHA || keys != wantKeys || keys != 2 {
		t.Fatalf("Reproject = %q %s %d %v; want %q %s %d", got, sha, keys, err, want, wantSHA, wantKeys)
	}
	if sum := sha256.Sum256(got); hex.EncodeToString(sum[:]) != sha {
		t.Fatal("returned digest is not the SHA-256 of the returned bytes")
	}
	bad := filepath.Join(t.TempDir(), "bad.ndjson")
	if err := os.WriteFile(bad, []byte("{\"id\":\"A01\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := Reproject(bad); err == nil {
		t.Fatal("invalid source re-projected")
	}
}

func TestMappingIsACopyOfThePrepareMapping(t *testing.T) {
	m := Mapping()
	if !bytes.Equal(m, mappingBytes) {
		t.Fatal("Mapping differs from the bytes prepare writes")
	}
	m[0] ^= 0xff
	if !bytes.Equal(Mapping(), mappingBytes) || mappingBytes[0] == m[0] {
		t.Fatal("Mapping exposed the package bytes")
	}
}
