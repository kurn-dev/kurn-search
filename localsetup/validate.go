package localsetup

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"unicode/utf8"
)

var bundleHashes = map[string]string{
	"config.json": "02dc69b92b68299d55752846e04be10d8eb014f4d3c9f0d770342e30bf53b53a",
	"base.jsonl":  "c42bda2fa64706595077a9131934d60d73e397a6f3b2c7009d19fead83db18d0",
	"base.idx":    "57af90d680a7ea51481d7094f526c17bf65297a0a1c9613998b489c8d588f18c",
}

func absent(path string) bool {
	_, err := os.Lstat(path)
	return errors.Is(err, os.ErrNotExist)
}

func physicalDirectory(path string, private bool) bool {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return false
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || resolved != path {
		return false
	}
	info, err := os.Lstat(path)
	return err == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0 &&
		info.Mode().Perm()&0o022 == 0 && (!private || info.Mode().Perm() == 0o700)
}

func overlaps(left, right string) bool {
	return left == right || strings.HasPrefix(left, right+string(filepath.Separator)) ||
		strings.HasPrefix(right, left+string(filepath.Separator))
}

func underCheckout(path string) bool {
	for {
		_, err := os.Lstat(filepath.Join(path, ".git"))
		if !errors.Is(err, os.ErrNotExist) {
			return true
		}
		parent := filepath.Dir(path)
		if parent == path {
			return false
		}
		path = parent
	}
}

func port(address string) (int, bool) {
	host, number, err := net.SplitHostPort(address)
	if err != nil || host != "127.0.0.1" || number == "" {
		return 0, false
	}
	for _, digit := range number {
		if digit < '0' || digit > '9' {
			return 0, false
		}
	}
	value, err := strconv.Atoi(number)
	return value, err == nil && value > 0 && value <= 65535 && strconv.Itoa(value) == number
}

func validateOptions(options Options) bool {
	back, backOK := port(options.KurndAddress)
	search, searchOK := port(options.SearchdAddress)
	if !backOK || !searchOK || back == search {
		return false
	}
	if !physicalDirectory(options.Bundle, false) || !filepath.IsAbs(options.Out) ||
		filepath.Clean(options.Out) != options.Out || !absent(options.Out) ||
		overlaps(options.Bundle, options.Out) {
		return false
	}
	parent := filepath.Dir(options.Out)
	return physicalDirectory(parent, true) && !underCheckout(parent)
}

func boundedFile(path string, maximum int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o133 != 0 ||
		info.Size() < 1 || info.Size() > maximum {
		return nil, errRefused
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Nlink != 1 {
		return nil, errRefused
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, errRefused
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, errRefused
	}
	raw, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil || int64(len(raw)) != info.Size() || int64(len(raw)) > maximum {
		return nil, errRefused
	}
	return raw, nil
}

// The exact-example manifest is flat. Tokenizing its names rejects duplicates
// including escaped-name duplicates before a map can silently collapse them.
func validateManifest(raw []byte, recipe recipe) bool {
	if len(raw) == 0 || !utf8.Valid(raw) {
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	first, err := decoder.Token()
	if err != nil || first != json.Delim('{') {
		return false
	}
	values := map[string]json.RawMessage{}
	for decoder.More() {
		token, err := decoder.Token()
		name, ok := token.(string)
		if err != nil || !ok {
			return false
		}
		if _, duplicate := values[name]; duplicate {
			return false
		}
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return false
		}
		values[name] = value
	}
	last, err := decoder.Token()
	if err != nil || last != json.Delim('}') || len(values) != 8 {
		return false
	}
	var extra any
	if !errors.Is(decoder.Decode(&extra), io.EOF) {
		return false
	}
	stringsWanted := map[string]string{
		"sha256": recipe.bundleHashes["base.jsonl"], "version_id": recipe.bundleHashes["base.jsonl"][:12],
		"analyzer": recipe.analyzerSHA256,
		"mode":     recipe.mode, "config_sha256": recipe.configSHA256,
	}
	for name, wanted := range stringsWanted {
		var got string
		value, exists := values[name]
		if !exists || json.Unmarshal(value, &got) != nil || got != wanted {
			return false
		}
	}
	for name, wanted := range map[string]int{"v": 2, "entries": recipe.entries, "keys": recipe.keys} {
		var got int
		value, exists := values[name]
		if !exists || json.Unmarshal(value, &got) != nil || got != wanted {
			return false
		}
	}
	return true
}

func readBundle(root string, recipe recipe) (map[string][]byte, error) {
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 4 {
		return nil, errRefused
	}
	for _, entry := range entries {
		if entry.Name() != "manifest.json" {
			if _, ok := recipe.bundleHashes[entry.Name()]; !ok {
				return nil, errRefused
			}
		}
	}
	raw, err := boundedFile(filepath.Join(root, "manifest.json"), maxManifestBytes)
	if err != nil || !validateManifest(raw, recipe) {
		return nil, errRefused
	}
	files := make(map[string][]byte, 3)
	for name, expected := range recipe.bundleHashes {
		data, err := boundedFile(filepath.Join(root, name), maxDataBytes)
		digest := sha256.Sum256(data)
		if err != nil || hex.EncodeToString(digest[:]) != expected {
			return nil, errRefused
		}
		files[name] = data
	}
	return files, nil
}
