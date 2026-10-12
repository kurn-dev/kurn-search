package release

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"sort"
	"strings"
	"unicode/utf8"
)

const exactIndexMagic = "KURNEXA1"

type exactIndexMeta struct {
	KeyCount    int             `json:"key_count"`
	PostingsLen int             `json:"postings_len"`
	ArenaLen    int             `json:"arena_len"`
	Analyzer    string          `json:"analyzer,omitempty"`
	Build       *indexBuildInfo `json:"build,omitempty"`
}

type indexBuildInfo struct {
	BaseID         string `json:"base_id"`
	Entries        int    `json:"entries"`
	DroppedKeys    int    `json:"dropped_keys"`
	KeylessEntries int    `json:"keyless_entries"`
}

func verifyExactIndex(path string, manifest manifestV2, projection projectionResult) error {
	expected, err := expectedExactIndex(projection.base)
	if err != nil || len(expected) == 0 {
		return indexError("expected")
	}
	raw, err := readRegular(path, maxBundleFile)
	if err != nil || len(raw) < len(exactIndexMagic)+1 || string(raw[:len(exactIndexMagic)]) != exactIndexMagic {
		return indexError("magic")
	}
	reader := bytes.NewReader(raw[len(exactIndexMagic):])
	headerLength, err := binary.ReadUvarint(reader)
	if err != nil || headerLength == 0 || headerLength > 1<<20 || headerLength > uint64(reader.Len()) {
		return indexError("header-length")
	}
	header := make([]byte, int(headerLength))
	if _, err := io.ReadFull(reader, header); err != nil {
		return indexError("header-read")
	}
	var meta exactIndexMeta
	if decodeStrict(header, 1<<20, &meta) != nil || meta.KeyCount < 1 || meta.KeyCount > manifest.Keys ||
		meta.PostingsLen != manifest.Keys || meta.ArenaLen < meta.KeyCount || meta.Analyzer != manifest.Analyzer ||
		meta.Build == nil || meta.Build.BaseID != manifest.SHA256 || meta.Build.Entries != manifest.Entries ||
		meta.Build.DroppedKeys != 0 || meta.Build.KeylessEntries != 0 || meta.ArenaLen > reader.Len() {
		return indexError("header-content")
	}
	canonicalHeader, err := json.Marshal(meta)
	if err != nil || !bytes.Equal(header, canonicalHeader) {
		return indexError("header-canonical")
	}
	arena := make([]byte, meta.ArenaLen)
	if _, err := io.ReadFull(reader, arena); err != nil {
		return indexError("arena-read")
	}
	keyLengths := make([]int, meta.KeyCount)
	runLengths := make([]int, meta.KeyCount)
	totalKeys, totalPostings := 0, 0
	for index := 0; index < meta.KeyCount; index++ {
		keyLength, err := binary.ReadUvarint(reader)
		if err != nil || keyLength == 0 || keyLength > uint64(meta.ArenaLen-totalKeys) {
			return indexError("key-length")
		}
		runLength, err := binary.ReadUvarint(reader)
		if err != nil || runLength == 0 || runLength > uint64(meta.PostingsLen-totalPostings) {
			return indexError("run-length")
		}
		keyLengths[index], runLengths[index] = int(keyLength), int(runLength)
		totalKeys += int(keyLength)
		totalPostings += int(runLength)
	}
	if totalKeys != meta.ArenaLen || totalPostings != meta.PostingsLen || reader.Len() != meta.PostingsLen*4 {
		return indexError("section-totals")
	}
	offset := 0
	keys := make([]string, meta.KeyCount)
	prior := ""
	for index, length := range keyLengths {
		key := string(arena[offset : offset+length])
		if !utf8.ValidString(key) || key == "" || (index > 0 && strings.Compare(prior, key) >= 0) {
			return indexError("key-order")
		}
		keys[index] = key
		prior = key
		offset += length
	}
	expectedKeys := make([]string, 0, len(expected))
	for key := range expected {
		expectedKeys = append(expectedKeys, key)
	}
	sort.Strings(expectedKeys)
	if len(keys) != len(expectedKeys) {
		return indexError("key-census")
	}
	for keyIndex, runLength := range runLengths {
		if keys[keyIndex] != expectedKeys[keyIndex] || runLength != len(expected[expectedKeys[keyIndex]]) {
			return indexError("key-content")
		}
		var prior uint32
		for index := 0; index < runLength; index++ {
			var encoded [4]byte
			if _, err := io.ReadFull(reader, encoded[:]); err != nil {
				return indexError("posting-read")
			}
			ordinal := binary.LittleEndian.Uint32(encoded[:])
			if int(ordinal) >= manifest.Entries || (index > 0 && ordinal <= prior) || ordinal != expected[expectedKeys[keyIndex]][index] {
				return indexError("posting-value")
			}
			prior = ordinal
		}
	}
	if reader.Len() != 0 {
		return indexError("trailing-data")
	}
	return nil
}

func expectedExactIndex(base []byte) (map[string][]uint32, error) {
	expected := map[string][]uint32{}
	for ordinal, line := range orderedBaseLines(base) {
		var row baseRow
		if decodeStrict(line, len(line), &row) != nil || row.ID == "" || len(row.Keys) == 0 || uint64(ordinal) > uint64(^uint32(0)) {
			return nil, errRefused
		}
		canonical, err := json.Marshal(row)
		if err != nil || !bytes.Equal(canonical, line) {
			return nil, errRefused
		}
		for _, key := range row.Keys {
			if key == "" {
				return nil, errRefused
			}
			expected[key] = append(expected[key], uint32(ordinal))
		}
	}
	return expected, nil
}

func indexError(reason string) error {
	_ = reason
	return errRefused
}
