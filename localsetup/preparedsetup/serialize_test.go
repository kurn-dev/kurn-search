package preparedsetup

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"unicode/utf16"

	"github.com/kurn-dev/kurn-search/adapters/partscatalogsuggest"
)

func TestSerializeIndependentProjectionSeams(t *testing.T) {
	tests := []struct {
		name, source, wantLine, wantGoEscaped string
		literal                               map[string]string
		partition, special                    bool
	}{
		{name: "without-partition", source: "{\"tenant_id\":\"alpha\",\"status\":\"active\",\"manufacturer\":\"Northstar\",\"identifier\":\"AX-100\",\"id\":\"A01\",\"category\":\"bearing\"}\n", wantLine: "{\"category\":\"bearing\",\"id\":\"A01\",\"identifier\":\"AX-100\",\"manufacturer\":\"Northstar\",\"status\":\"active\",\"tenant_id\":\"alpha\"}\n", literal: map[string]string{"category": "bearing", "id": "A01", "identifier": "AX-100", "manufacturer": "Northstar", "status": "active", "tenant_id": "alpha"}, wantGoEscaped: "{\"category\":\"bearing\",\"id\":\"A01\",\"identifier\":\"AX-100\",\"manufacturer\":\"Northstar\",\"status\":\"active\",\"tenant_id\":\"alpha\"}"},
		{name: "partition-and-fixed-unicode", source: "{\"category\":\"联接<&>\\u2028\\u2029件\",\"id\":\"A08\",\"identifier\":\"ÅB-880\",\"manufacturer\":\"M<&>\\u2028\\u2029国\",\"partition_id\":\"south\",\"status\":\"active\",\"tenant_id\":\"alpha\"}\n", wantLine: "{\"category\":\"联接<&>  件\",\"id\":\"A08\",\"identifier\":\"ÅB-880\",\"manufacturer\":\"M<&>  国\",\"partition_id\":\"south\",\"status\":\"active\",\"tenant_id\":\"alpha\"}\n", literal: map[string]string{"category": "联接<&>  件", "id": "A08", "identifier": "ÅB-880", "manufacturer": "M<&>  国", "partition_id": "south", "status": "active", "tenant_id": "alpha"}, wantGoEscaped: "{\"category\":\"联接\\u003c\\u0026\\u003e\\u2028\\u2029件\",\"id\":\"A08\",\"identifier\":\"ÅB-880\",\"manufacturer\":\"M\\u003c\\u0026\\u003e\\u2028\\u2029国\",\"partition_id\":\"south\",\"status\":\"active\",\"tenant_id\":\"alpha\"}", partition: true, special: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "source.ndjson")
			if err := os.WriteFile(path, []byte(test.source), 0600); err != nil {
				t.Fatal(err)
			}
			source, err := readSource(path)
			if err != nil {
				t.Fatal(err)
			}
			line, _, count, err := serializeProjected(source)
			if err != nil || count != 1 || string(line) != test.wantLine {
				t.Fatalf("serialized count=%d err=%v\nwant %q\n got %q", count, err, test.wantLine, line)
			}
			projected, err := partscatalogsuggest.New().Project([]byte(test.source[:len(test.source)-1]))
			if err != nil || len(projected) != 1 {
				t.Fatalf("initial Project: %v", err)
			}
			var compact bytes.Buffer
			if err := json.Compact(&compact, projected[0].Payload()); err != nil {
				t.Fatal(err)
			}
			compact.WriteByte('\n')
			if !bytes.Equal(compact.Bytes(), line) {
				t.Fatalf("Compact(P)+LF drift:\nwant %q\n got %q", compact.Bytes(), line)
			}
			reprojected, err := partscatalogsuggest.New().Project(line[:len(line)-1])
			if err != nil || len(reprojected) != 1 || !bytes.Equal(reprojected[0].Payload(), projected[0].Payload()) {
				t.Fatalf("re-Project drift: %v", err)
			}
			var decoded map[string]json.RawMessage
			if err := json.Unmarshal(line, &decoded); err != nil {
				t.Fatal(err)
			}
			_, present := decoded["partition_id"]
			if present != test.partition {
				t.Fatalf("partition presence=%t", present)
			}
			goEscaped, err := json.Marshal(test.literal)
			if err != nil || string(goEscaped) != test.wantGoEscaped {
				t.Fatalf("independent C*: %v\nwant %q\n got %q", err, test.wantGoEscaped, goEscaped)
			}
			if test.special && bytes.Equal(append(goEscaped, '\n'), line) {
				t.Fatal("C* unexpectedly equals L")
			}
		})
	}
}

func TestSourceFieldASCIIAndUTF16OrderAgree(t *testing.T) {
	fields := []string{"category", "id", "identifier", "manufacturer", "partition_id", "status", "tenant_id"}
	byteOrder := append([]string(nil), fields...)
	sort.Strings(byteOrder)
	utf16Order := append([]string(nil), fields...)
	sort.Slice(utf16Order, func(i, j int) bool {
		a, b := utf16.Encode([]rune(utf16Order[i])), utf16.Encode([]rune(utf16Order[j]))
		for k := 0; k < len(a) && k < len(b); k++ {
			if a[k] != b[k] {
				return a[k] < b[k]
			}
		}
		return len(a) < len(b)
	})
	if !equalStrings(fields, byteOrder) || !equalStrings(fields, utf16Order) {
		t.Fatalf("field order differs: byte=%v utf16=%v", byteOrder, utf16Order)
	}
}

func equalStrings(a, b []string) bool {
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
