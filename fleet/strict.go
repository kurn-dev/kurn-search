package fleet

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
var metricRE = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

func canonical(value any) ([]byte, error) {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, errRefused
	}
	return append(raw, '\n'), nil
}

func decode(raw []byte, maximum int, destination any) error {
	if len(raw) == 0 || len(raw) > maximum || !utf8.Valid(raw) || rejectDuplicates(raw) != nil {
		return errRefused
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if decoder.Decode(destination) != nil || requireEOF(decoder) != nil {
		return errRefused
	}
	want, err := canonical(destination)
	if err != nil || !bytes.Equal(raw, want) {
		return errRefused
	}
	return nil
}

// ParseManagementSignal strictly parses the complete reviewed public Kurn
// stats response, then returns only the bounded management fields that S4 may
// retain. The physical list label is validated and discarded.
func ParseManagementSignal(raw []byte) (ManagementSignal, error) {
	if len(raw) == 0 || len(raw) > MaxControlBytes || !utf8.Valid(raw) || rejectDuplicates(raw) != nil {
		return ManagementSignal{}, errRefused
	}
	var wire struct {
		DroppedKeys      int    `json:"dropped_keys"`
		Entries          int    `json:"entries"`
		KeylessEntries   int    `json:"keyless_entries"`
		Mode             string `json:"mode"`
		Name             string `json:"name"`
		Overlay          int    `json:"overlay"`
		Tombstones       int    `json:"tombstones"`
		UnindexedEntries int    `json:"unindexed_entries,omitempty"`
		Version          string `json:"version"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if decoder.Decode(&wire) != nil || requireEOF(decoder) != nil || !validName(wire.Name) || wire.Entries <= 0 ||
		wire.Overlay != 0 || wire.Tombstones != 0 || wire.DroppedKeys != 0 || wire.KeylessEntries != 0 || wire.UnindexedEntries != 0 ||
		!validName(wire.Mode) || wire.Version == "" || len(wire.Version) > 512 {
		return ManagementSignal{}, errRefused
	}
	return ManagementSignal{Entries: wire.Entries, Version: wire.Version}, nil
}

func rejectDuplicates(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if walkJSON(decoder) != nil {
		return errRefused
	}
	return requireEOF(decoder)
}

func walkJSON(decoder *json.Decoder) error {
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
			if walkJSON(decoder) != nil {
				return errRefused
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return errRefused
		}
	case '[':
		for decoder.More() {
			if walkJSON(decoder) != nil {
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

func digest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func domainDigest(domain string, value any) (string, error) {
	raw, err := canonical(value)
	if err != nil {
		return "", errRefused
	}
	return digest(append(append([]byte(domain), 0), raw...)), nil
}

func validSHA(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size && hex.EncodeToString(decoded) == value
}

func validOpaque(value string) bool {
	return opaqueRE.MatchString(value) && !strings.Contains(value, "..") && !strings.HasPrefix(value, "/") && !strings.HasSuffix(value, "/")
}

func validName(value string) bool { return validOpaque(value) && !strings.Contains(value, "/") }

func readRegular(path string, maximum int64, mode *os.FileMode) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() < 0 || info.Size() > maximum || hasMultipleLinks(info) {
		return nil, errRefused
	}
	if mode != nil && info.Mode().Perm() != mode.Perm() {
		return nil, errRefused
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, errRefused
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) || hasMultipleLinks(opened) {
		return nil, errRefused
	}
	reader := &io.LimitedReader{R: file, N: maximum + 1}
	raw, err := io.ReadAll(reader)
	if err != nil || int64(len(raw)) > maximum {
		return nil, errRefused
	}
	return raw, nil
}

func resolvedExecutable(path string) (string, string, error) {
	if !cleanAbsolute(path) {
		return "", "", errRefused
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || resolved != path {
		return "", "", errRefused
	}
	raw, err := readRegular(path, MaxBundleBytes, nil)
	if err != nil {
		return "", "", errRefused
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm()&0o111 == 0 || info.Mode().Perm()&0o022 != 0 {
		return "", "", errRefused
	}
	return path, digest(raw), nil
}

func resolvedPrivateDirectory(path string) (string, error) {
	if !cleanAbsolute(path) {
		return "", errRefused
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || resolved != path {
		return "", errRefused
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o700 {
		return "", errRefused
	}
	return path, nil
}

func resolvedAbsent(path string) error {
	if !cleanAbsolute(path) {
		return errRefused
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		return errRefused
	}
	parent := filepath.Dir(path)
	resolved, err := filepath.EvalSymlinks(parent)
	if err != nil || resolved != parent {
		return errRefused
	}
	info, err := os.Lstat(parent)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return errRefused
	}
	return nil
}

func cleanAbsolute(path string) bool {
	return path != "" && filepath.IsAbs(path) && filepath.Clean(path) == path && !strings.ContainsRune(path, 0)
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

func privateTransaction(out string, files map[string][]byte) error {
	if resolvedAbsent(out) != nil {
		return errRefused
	}
	temporary := filepath.Join(filepath.Dir(out), "."+filepath.Base(out)+".s4-tmp")
	if _, err := os.Lstat(temporary); !errors.Is(err, os.ErrNotExist) {
		return errRefused
	}
	if os.Mkdir(temporary, 0o700) != nil {
		return errRefused
	}
	ok := false
	defer func() {
		if !ok {
			_ = os.RemoveAll(temporary)
		}
	}()
	names := make([]string, 0, len(files))
	for name := range files {
		if !validName(name) {
			return errRefused
		}
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if writePrivate(filepath.Join(temporary, name), files[name]) != nil {
			return errRefused
		}
	}
	directory, err := os.Open(temporary)
	if err != nil || directory.Sync() != nil || directory.Close() != nil || os.Rename(temporary, out) != nil {
		return errRefused
	}
	ok = true
	return nil
}

func exactDirectory(path string, names []string) error {
	if _, err := resolvedPrivateDirectory(path); err != nil {
		return errRefused
	}
	entries, err := os.ReadDir(path)
	if err != nil || len(entries) != len(names) {
		return errRefused
	}
	sort.Strings(names)
	for index, entry := range entries {
		if entry.Name() != names[index] || !entry.Type().IsRegular() {
			return errRefused
		}
	}
	return nil
}

func overlap(left, right string) bool {
	return left == right || strings.HasPrefix(left, right+string(filepath.Separator)) || strings.HasPrefix(right, left+string(filepath.Separator))
}
