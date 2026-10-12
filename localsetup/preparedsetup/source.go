// Package preparedsetup builds one immutable, local prepared Search version.
package preparedsetup

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"syscall"
	"unicode/utf8"

	"github.com/kurn-dev/kurn-search/adapters/partscatalogsuggest"
)

const (
	maxSourceBytes = 64 << 20
	maxLineBytes   = 1 << 20
	maxRecords     = 100000
	maxTenants     = 16
)

var errRefused = errors.New("preparedsetup: refused")

type sourceRecord struct {
	Category     string `json:"category"`
	ID           string `json:"id"`
	Identifier   string `json:"identifier"`
	Manufacturer string `json:"manufacturer"`
	PartitionID  string `json:"partition_id,omitempty"`
	Status       string `json:"status"`
	TenantID     string `json:"tenant_id"`
}

type projectedRecord struct {
	record sourceRecord
	line   []byte
	pretty []byte
}

type sourceSet struct {
	records []projectedRecord
	sha256  string
	bytes   int64
	tenants []string
}

func readSource(path string) (sourceSet, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || !validSourceSize(info.Size()) {
		return sourceSet{}, errRefused
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok && stat.Nlink != 1 {
		return sourceSet{}, errRefused
	}
	f, err := os.Open(path)
	if err != nil {
		return sourceSet{}, errRefused
	}
	defer f.Close()
	h := sha256.New()
	r := bufio.NewReader(io.TeeReader(io.LimitReader(f, maxSourceBytes+1), h))
	var out sourceSet
	ids, tenants := map[string]struct{}{}, map[string]struct{}{}
	for {
		line, e := r.ReadBytes('\n')
		if (len(line) != 0 && !validLineBodySize(len(line)-1)) || (e == io.EOF && len(line) != 0) || bytes.IndexByte(line, '\r') >= 0 || !utf8.Valid(line) {
			return sourceSet{}, errRefused
		}
		if len(line) != 0 {
			body := line[:len(line)-1]
			entry, pe := partscatalogsuggest.New().Project(body)
			if pe != nil || len(entry) != 1 || !entry[0].Valid() {
				return sourceSet{}, errRefused
			}
			var rec sourceRecord
			if strictDecode(body, &rec) != nil || rec.ID != entry[0].EntryID() || len(entry[0].Keys()) != 1 || rec.Identifier != entry[0].Keys()[0] {
				return sourceSet{}, errRefused
			}
			if _, exists := ids[rec.ID]; exists {
				return sourceSet{}, errRefused
			}
			ids[rec.ID], tenants[rec.TenantID] = struct{}{}, struct{}{}
			compact := new(bytes.Buffer)
			if json.Compact(compact, entry[0].Payload()) != nil {
				return sourceSet{}, errRefused
			}
			compact.WriteByte('\n')
			out.records = append(out.records, projectedRecord{record: rec, line: compact.Bytes(), pretty: entry[0].Payload()})
			if len(out.records) > maxRecords || len(tenants) > maxTenants {
				return sourceSet{}, errRefused
			}
		}
		if e == io.EOF {
			break
		}
		if e != nil {
			return sourceSet{}, errRefused
		}
	}
	if len(out.records) == 0 {
		return sourceSet{}, errRefused
	}
	out.bytes, out.sha256 = info.Size(), hex.EncodeToString(h.Sum(nil))
	for tenant := range tenants {
		out.tenants = append(out.tenants, tenant)
	}
	slicesSort(out.tenants)
	return out, nil
}

func validSourceSize(size int64) bool { return size >= 1 && size <= maxSourceBytes }
func validLineBodySize(size int) bool { return size >= 1 && size <= maxLineBytes }

func strictDecode(raw []byte, dst any) error {
	if !utf8.Valid(raw) || !json.Valid(raw) || duplicateJSONName(raw) {
		return errRefused
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(dst) != nil {
		return errRefused
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return errRefused
	}
	return nil
}

func duplicateJSONName(raw []byte) bool {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var walk func() bool
	walk = func() bool {
		t, err := d.Token()
		if err != nil {
			return true
		}
		delim, ok := t.(json.Delim)
		if !ok {
			return false
		}
		if delim == '{' {
			seen := map[string]bool{}
			for d.More() {
				n, e := d.Token()
				if e != nil {
					return true
				}
				s, ok := n.(string)
				if !ok || seen[s] {
					return true
				}
				seen[s] = true
				if walk() {
					return true
				}
			}
			_, err = d.Token()
			return err != nil
		}
		if delim == '[' {
			for d.More() {
				if walk() {
					return true
				}
			}
			_, err = d.Token()
			return err != nil
		}
		return true
	}
	return walk()
}

func slicesSort(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}
