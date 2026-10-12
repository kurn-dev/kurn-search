package lifecycle

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

var opaqueRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._/-]{0,127}$`)

func marshalCanonical(value any) ([]byte, error) {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, errRefused
	}
	return append(raw, '\n'), nil
}

func decodeCanonical(raw []byte, maximum int, destination any) error {
	if len(raw) == 0 || len(raw) > maximum || !utf8.Valid(raw) || rejectDuplicateNames(raw) != nil {
		return errRefused
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if decoder.Decode(destination) != nil || requireEOF(decoder) != nil {
		return errRefused
	}
	want, err := marshalCanonical(destination)
	if err != nil || !bytes.Equal(raw, want) {
		return errRefused
	}
	return nil
}

func rejectDuplicateNames(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if walkJSONValue(decoder) != nil {
		return errRefused
	}
	return requireEOF(decoder)
}

func walkJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := map[string]bool{}
		for decoder.More() {
			nameToken, err := decoder.Token()
			if err != nil {
				return err
			}
			name, ok := nameToken.(string)
			if !ok || seen[name] || !utf8.ValidString(name) {
				return errRefused
			}
			seen[name] = true
			if walkJSONValue(decoder) != nil {
				return errRefused
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return errRefused
		}
	case '[':
		for decoder.More() {
			if walkJSONValue(decoder) != nil {
				return errRefused
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return errRefused
		}
	default:
		return errRefused
	}
	return nil
}

func requireEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errRefused
	}
	return nil
}

func digestBytes(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func DigestBytes(raw []byte) string { return digestBytes(raw) }

func validSHA256(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size && hex.EncodeToString(decoded) == value
}

func validOpaque(value string) bool {
	return opaqueRE.MatchString(value) && !strings.Contains(value, "..") && !strings.HasPrefix(value, "/") && !strings.HasSuffix(value, "/")
}

func validName(value string) bool {
	return validOpaque(value) && !strings.Contains(value, "/")
}

func readRegular(path string, maximum int64) ([]byte, error) {
	file, err := openRegular(path, maximum)
	if err != nil {
		return nil, errRefused
	}
	defer file.Close()
	reader := &io.LimitedReader{R: file, N: maximum + 1}
	raw, err := io.ReadAll(reader)
	if err != nil || int64(len(raw)) > maximum {
		return nil, errRefused
	}
	return raw, nil
}

func openRegular(path string, maximum int64) (*os.File, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() < 0 || info.Size() > maximum || hasMultipleLinks(info) {
		return nil, errRefused
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, errRefused
	}
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) || hasMultipleLinks(opened) {
		file.Close()
		return nil, errRefused
	}
	return file, nil
}

func digestFile(path string, maximum int64) (string, error) {
	raw, err := readRegular(path, maximum)
	if err != nil {
		return "", errRefused
	}
	return digestBytes(raw), nil
}

func DigestExecutable(path string) (string, error) {
	checked, err := resolvedRegular(path, 1<<30)
	if err != nil {
		return "", errRefused
	}
	info, err := os.Stat(checked)
	if err != nil || info.Mode().Perm()&0o111 == 0 || info.Mode().Perm()&0o022 != 0 {
		return "", errRefused
	}
	return digestFile(checked, 1<<30)
}

func resolvedRegular(path string, maximum int64) (string, error) {
	if path == "" || strings.IndexByte(path, 0) >= 0 || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return "", errRefused
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || resolved != path {
		return "", errRefused
	}
	file, err := openRegular(path, maximum)
	if err != nil {
		return "", errRefused
	}
	file.Close()
	return path, nil
}

func resolvedDirectory(path string, mode os.FileMode) (string, error) {
	if path == "" || strings.IndexByte(path, 0) >= 0 || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return "", errRefused
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || resolved != path {
		return "", errRefused
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != mode.Perm() {
		return "", errRefused
	}
	return path, nil
}

func writePrivate(path string, raw []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return errRefused
	}
	ok := false
	defer func() {
		_ = file.Close()
		if !ok {
			_ = os.Remove(path)
		}
	}()
	if _, err := file.Write(raw); err != nil || file.Sync() != nil || file.Close() != nil || os.Chmod(path, 0o600) != nil {
		return errRefused
	}
	ok = true
	return nil
}

func equalInventory(left, right []FileDigest) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func inventorySortedUnique(values []FileDigest) bool {
	if len(values) < 4 || len(values) > 5 {
		return false
	}
	previous := ""
	for _, value := range values {
		if value.Path <= previous || !strings.HasPrefix(value.Path, "bundle/") || filepath.ToSlash(filepath.Clean(filepath.FromSlash(value.Path))) != value.Path || strings.Count(value.Path, "/") != 1 || !validSHA256(value.SHA256) {
			return false
		}
		previous = value.Path
	}
	want := []string{"bundle/base.idx", "bundle/base.jsonl", "bundle/config.json", "bundle/manifest.json"}
	if len(values) == 5 {
		want = append(want, "bundle/delta.jsonl")
	}
	sort.Strings(want)
	for i := range want {
		if values[i].Path != want[i] {
			return false
		}
	}
	return true
}
