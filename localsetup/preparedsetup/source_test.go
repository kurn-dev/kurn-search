package preparedsetup

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadSourcePinsAndRefusals(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "s")
	good := "{\"category\":\"联接<&>\\u2028\\u2029件\",\"id\":\"A08\",\"identifier\":\"ÅB-880\",\"manufacturer\":\"M<&>\\u2028\\u2029国\",\"partition_id\":\"south\",\"status\":\"active\",\"tenant_id\":\"alpha\"}\n"
	if os.WriteFile(p, []byte(good), 0600) != nil {
		t.Fatal()
	}
	s, e := readSource(p)
	if e != nil || s.records[0].record.Identifier != "ÅB-880" || !strings.Contains(s.records[0].record.Manufacturer, "国") {
		t.Fatalf("%v %#v", e, s)
	}
	for _, bad := range []string{strings.TrimSuffix(good, "\n"), "\n", strings.Replace(good, "\n", "\r\n", 1), good + good} {
		os.WriteFile(p, []byte(bad), 0600)
		if _, e := readSource(p); e == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
}

func TestSourceDuplicateIdentifierAndOptionalPartition(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s")
	raw := "{\"category\":\"c\",\"id\":\"A01\",\"identifier\":\"ÅΩ国联接件\",\"manufacturer\":\"M\",\"status\":\"active\",\"tenant_id\":\"alpha\"}\n" +
		"{\"category\":\"c\",\"id\":\"A02\",\"identifier\":\"ÅΩ国联接件\",\"manufacturer\":\"M\",\"partition_id\":\"north\",\"status\":\"active\",\"tenant_id\":\"alpha\"}\n"
	if err := os.WriteFile(p, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := readSource(p)
	if err != nil || len(got.records) != 2 || got.records[0].record.PartitionID != "" || got.records[1].record.Identifier != "ÅΩ国联接件" {
		t.Fatalf("legitimate tie/optional partition/unicode changed: %v %#v", err, got)
	}
	for _, mutation := range []string{
		strings.Replace(raw, `"id":"A02"`, `"id":"A01"`, 1),
		strings.Replace(raw, `"category":"c"`, `"category":null`, 1),
		strings.Replace(raw, `"category":"c"`, `"unknown":"x","category":"c"`, 1),
		strings.Replace(raw, `"category":"c"`, `"category":"c","category":"c"`, 1),
	} {
		if err := os.WriteFile(p, []byte(mutation), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := readSource(p); err == nil {
			t.Fatal("accepted invalid source mutation")
		}
	}
}

func TestSourceBoundaryFixtures(t *testing.T) {
	if !validSourceSize(maxSourceBytes) || validSourceSize(maxSourceBytes+1) || !validLineBodySize(maxLineBytes) || validLineBodySize(maxLineBytes+1) {
		t.Fatal("source/line byte boundary predicates")
	}
	d := t.TempDir()
	p := filepath.Join(d, "s")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(maxSourceBytes + 1); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := readSource(p); err == nil {
		t.Fatal("accepted source larger than 64 MiB")
	}

	long := []byte(`{"category":"sentinel","id":"A01","identifier":"` + strings.Repeat("x", maxLineBytes) + `","manufacturer":"M","status":"active","tenant_id":"alpha"}` + "\n")
	if err := os.WriteFile(p, long, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readSource(p); err == nil {
		t.Fatal("accepted record body larger than 1 MiB")
	}

	var records strings.Builder
	for i := 0; i < maxTenants; i++ {
		fmt.Fprintf(&records, "{\"category\":\"c\",\"id\":\"A%02d\",\"identifier\":\"I%02d\",\"manufacturer\":\"M\",\"status\":\"active\",\"tenant_id\":\"t%02d\"}\n", i, i, i)
	}
	if err := os.WriteFile(p, []byte(records.String()), 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := readSource(p); err != nil || len(got.tenants) != maxTenants {
		t.Fatalf("16 tenant boundary: %v/%d", err, len(got.tenants))
	}
	records.Reset()
	for i := 0; i < maxTenants+1; i++ {
		fmt.Fprintf(&records, "{\"category\":\"c\",\"id\":\"A%02d\",\"identifier\":\"I%02d\",\"manufacturer\":\"M\",\"status\":\"active\",\"tenant_id\":\"t%02d\"}\n", i, i, i)
	}
	if err := os.WriteFile(p, []byte(records.String()), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readSource(p); err == nil {
		t.Fatal("accepted 17 tenants")
	}
}

func TestSourceHundredThousandRecordBoundary(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s")
	f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < maxRecords; i++ {
		if _, err := fmt.Fprintf(f, "{\"category\":\"c\",\"id\":\"A%06d\",\"identifier\":\"I%06d\",\"manufacturer\":\"M\",\"status\":\"active\",\"tenant_id\":\"alpha\"}\n", i, i); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := readSource(p)
	if err != nil || len(got.records) != maxRecords {
		t.Fatalf("100000 record boundary: %v/%d", err, len(got.records))
	}
}

func TestAssignedExampleOraclesMatchSources(t *testing.T) {
	root := filepath.Join("..", "..", "examples", "parts-catalog-suggest-prepared")
	for _, version := range []string{"a", "b"} {
		source, err := readSource(filepath.Join(root, "source-"+version+".ndjson"))
		if err != nil || len(source.records) != 24 {
			t.Fatalf("%s source: %v/%d", version, err, len(source.records))
		}
		raw, err := os.ReadFile(filepath.Join(root, "expected-"+version+".json"))
		if err != nil {
			t.Fatal(err)
		}
		var oracle struct {
			Records []sourceRecord `json:"records"`
			Cases   []struct {
				ExpectedIDs    []string `json:"expected_ids"`
				ExpectedScores []int    `json:"expected_scores"`
				ExpectedExact  []string `json:"expected_normalized_exact"`
			} `json:"cases"`
		}
		if json.Unmarshal(raw, &oracle) != nil || duplicateJSONName(raw) || len(oracle.Records) != 24 || len(oracle.Cases) != 18 {
			t.Fatalf("%s oracle shape", version)
		}
		for i, record := range oracle.Records {
			if record != source.records[i].record {
				t.Fatalf("%s record %d", version, i)
			}
		}
		for _, c := range oracle.Cases {
			if len(c.ExpectedIDs) != len(c.ExpectedScores) || len(c.ExpectedIDs) != len(c.ExpectedExact) {
				t.Fatalf("%s case vectors", version)
			}
		}
		if !json.Valid(raw) {
			t.Fatal("invalid json")
		}
	}
}
