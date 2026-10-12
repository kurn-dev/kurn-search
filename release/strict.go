package release

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
	"strings"
	"unicode/utf8"
)

var (
	tokenRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)
	listRE  = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
)

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
	if err := decoder.Decode(destination); err != nil || requireEOF(decoder) != nil {
		return errRefused
	}
	want, err := marshalCanonical(destination)
	if err != nil || !bytes.Equal(raw, want) {
		return errRefused
	}
	return nil
}

func decodeStrict(raw []byte, maximum int, destination any) error {
	if len(raw) == 0 || len(raw) > maximum || !utf8.Valid(raw) || rejectDuplicateNames(raw) != nil {
		return errRefused
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(destination); err != nil || requireEOF(decoder) != nil {
		return errRefused
	}
	return nil
}

func rejectDuplicateNames(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := walkJSONValue(decoder); err != nil {
		return err
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
		seen := map[string]struct{}{}
		for decoder.More() {
			nameToken, err := decoder.Token()
			if err != nil {
				return err
			}
			name, ok := nameToken.(string)
			if !ok || !utf8.ValidString(name) {
				return errRefused
			}
			if _, exists := seen[name]; exists {
				return errRefused
			}
			seen[name] = struct{}{}
			if err := walkJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return errRefused
		}
	case '[':
		for decoder.More() {
			if err := walkJSONValue(decoder); err != nil {
				return err
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

func topLevelNames(raw []byte) (map[string]struct{}, error) {
	if rejectDuplicateNames(raw) != nil {
		return nil, errRefused
	}
	var members map[string]json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(&members); err != nil || requireEOF(decoder) != nil || members == nil {
		return nil, errRefused
	}
	result := make(map[string]struct{}, len(members))
	for name := range members {
		result[name] = struct{}{}
	}
	return result, nil
}

func validSHA256(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size && hex.EncodeToString(decoded) == value
}

func digestBytes(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func digestFile(path string, maximum int64) (string, error) {
	file, err := openRegular(path, maximum)
	if err != nil {
		return "", errRefused
	}
	defer file.Close()
	hash := sha256.New()
	limited := &io.LimitedReader{R: file, N: maximum + 1}
	count, err := io.Copy(hash, limited)
	if err != nil || count > maximum {
		return "", errRefused
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
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

func readRegular(path string, maximum int64) ([]byte, error) {
	file, err := openRegular(path, maximum)
	if err != nil {
		return nil, errRefused
	}
	defer file.Close()
	limited := &io.LimitedReader{R: file, N: maximum + 1}
	raw, err := io.ReadAll(limited)
	if err != nil || int64(len(raw)) > maximum {
		return nil, errRefused
	}
	return raw, nil
}

func sameFile(left, right string) bool {
	a, errA := os.Stat(left)
	b, errB := os.Stat(right)
	return errA == nil && errB == nil && os.SameFile(a, b)
}

func cleanAbsolute(path string) (string, error) {
	if path == "" || strings.IndexByte(path, 0) >= 0 {
		return "", errRefused
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", errRefused
	}
	return filepath.Clean(absolute), nil
}

func requireResolved(path string) error {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || resolved != path {
		return errRefused
	}
	return nil
}
