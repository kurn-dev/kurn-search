package release

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/kurn-dev/kurn-search/adapter"
	"github.com/kurn-dev/kurn-search/adapters/catalogid"
	"github.com/kurn-dev/kurn-search/internal/jsjson"
	"github.com/kurn-dev/kurn-search/internal/jsorder"
	"github.com/kurn-dev/kurn-search/profile"
)

type projected struct {
	id      string
	keys    []string
	payload []byte
	record  payloadRecord
}

type payloadRecord struct {
	ID          string  `json:"id"`
	Identifier  string  `json:"identifier"`
	PartitionID *string `json:"partition_id,omitempty"`
	TenantID    string  `json:"tenant_id"`
}

type feedKey struct {
	Value string `json:"value"`
}

type feedRow struct {
	ID          string    `json:"id"`
	Identifier  string    `json:"identifier"`
	Keys        []feedKey `json:"keys"`
	PartitionID *string   `json:"partition_id,omitempty"`
	TenantID    string    `json:"tenant_id"`
}

type baseRow struct {
	ID      string          `json:"id"`
	Keys    []string        `json:"keys"`
	Payload json.RawMessage `json:"payload"`
}

type projectionResult struct {
	feed           []byte
	base           []byte
	mapping        []byte
	snapshotSHA256 string
	entries        int
	keys           int
	golden         GoldenSpec
}

func projectCatalog(source []byte, spec Spec) (projectionResult, error) {
	if len(source) == 0 || int64(len(source)) > spec.Snapshot.MaxBytes || source[len(source)-1] != '\n' {
		return projectionResult{}, projectionError("source-shape")
	}
	lines := bytes.Split(source[:len(source)-1], []byte{'\n'})
	if len(lines) != spec.Snapshot.Records || len(lines) > spec.Snapshot.MaxRecords {
		return projectionResult{}, projectionError("record-count")
	}
	semantics := catalogid.New()
	entries := make([]projected, 0, len(lines))
	seenIDs := make(map[string]struct{}, len(lines))
	for _, line := range lines {
		if len(line) == 0 || len(line) > profile.HardMaxSourceBytes || !utf8.Valid(line) {
			return projectionResult{}, projectionError("line-shape")
		}
		values, err := semantics.Project(line)
		if err != nil || len(values) != 1 || !values[0].Valid() {
			return projectionResult{}, projectionError("adapter-project")
		}
		entry := values[0]
		if _, exists := seenIDs[entry.EntryID()]; exists {
			return projectionResult{}, projectionError("duplicate-id")
		}
		seenIDs[entry.EntryID()] = struct{}{}
		keys := entry.Keys()
		if len(keys) == 0 || len(keys) > adapter.MaxPhysicalKeys {
			return projectionResult{}, projectionError("key-count")
		}
		seenKeys := make(map[string]struct{}, len(keys))
		for _, key := range keys {
			if key == "" || !utf8.ValidString(key) {
				return projectionResult{}, projectionError("key-shape")
			}
			if _, exists := seenKeys[key]; exists {
				return projectionResult{}, projectionError("duplicate-key")
			}
			seenKeys[key] = struct{}{}
		}
		var record payloadRecord
		if decodeStrict(entry.Payload(), adapter.MaxOpaqueJSONBytes, &record) != nil || record.ID != entry.EntryID() ||
			record.ID == "" || record.Identifier == "" || record.TenantID == "" ||
			(record.PartitionID != nil && *record.PartitionID == "") {
			return projectionResult{}, projectionError("payload-decode")
		}
		canonical, err := canonicalPayload(record)
		if err != nil || !bytes.Equal(canonical, entry.Payload()) {
			return projectionResult{}, errRefused
		}
		entries = append(entries, projected{id: entry.EntryID(), keys: keys, payload: entry.Payload(), record: record})
	}
	sort.SliceStable(entries, func(i, j int) bool { return jsorder.Less(entries[i].id, entries[j].id) })
	if len(entries) != spec.Snapshot.Records {
		return projectionResult{}, projectionError("sorted-count")
	}

	snapshotHash := sha256.New()
	snapshotHash.Write([]byte("kurn-search canonical source snapshot v1\x00"))
	var length [8]byte
	var feed bytes.Buffer
	var base bytes.Buffer
	totalKeys := 0
	goldenFound := false
	for _, entry := range entries {
		binary.BigEndian.PutUint64(length[:], uint64(len(entry.payload)))
		snapshotHash.Write(length[:])
		snapshotHash.Write(entry.payload)
		keys := make([]feedKey, len(entry.keys))
		for index, key := range entry.keys {
			keys[index] = feedKey{Value: key}
			if entry.id == spec.Golden.EntryID && key == spec.Golden.PhysicalKey {
				goldenFound = true
			}
		}
		totalKeys += len(keys)
		row, err := json.Marshal(feedRow{
			ID: entry.id, Identifier: entry.record.Identifier, Keys: keys,
			PartitionID: entry.record.PartitionID, TenantID: entry.record.TenantID,
		})
		if err != nil {
			return projectionResult{}, errRefused
		}
		feed.Write(row)
		feed.WriteByte('\n')
		baseRaw, err := json.Marshal(baseRow{ID: entry.id, Keys: entry.keys, Payload: entry.payload})
		if err != nil {
			return projectionResult{}, errRefused
		}
		base.Write(baseRaw)
		base.WriteByte('\n')
	}
	snapshotSHA := hex.EncodeToString(snapshotHash.Sum(nil))
	if snapshotSHA != spec.Snapshot.SHA256 || !goldenFound {
		return projectionResult{}, errRefused
	}
	mapping, err := catalogMapping(spec.Golden)
	if err != nil {
		return projectionResult{}, errRefused
	}
	return projectionResult{
		feed: feed.Bytes(), base: base.Bytes(), mapping: mapping, snapshotSHA256: snapshotSHA,
		entries: len(entries), keys: totalKeys, golden: spec.Golden,
	}, nil
}

func canonicalPayload(record payloadRecord) ([]byte, error) {
	value := jsjson.Object{
		"id": record.ID, "identifier": record.Identifier, "tenant_id": record.TenantID,
	}
	if record.PartitionID != nil {
		value["partition_id"] = *record.PartitionID
	}
	return jsjson.Encode(value)
}

func projectionError(reason string) error {
	_ = reason
	return errRefused
}

func catalogMapping(golden GoldenSpec) ([]byte, error) {
	return jsjson.Encode(jsjson.Object{
		"format": "ndjson",
		"id":     "id",
		"keys":   []any{jsjson.Object{"path": "keys.value"}},
		"payload": jsjson.Object{
			"id": "id", "identifier": "identifier", "partition_id": "partition_id", "tenant_id": "tenant_id",
		},
		"list": jsjson.Object{
			"analyzer": jsjson.Object{"steps": []string{}},
			"filterable": []any{
				jsjson.Object{"name": "partition_id", "path": "partition_id"},
				jsjson.Object{"name": "tenant_id", "path": "tenant_id"},
			},
			"golden": []any{jsjson.Object{"expect_id": golden.EntryID, "q": golden.PhysicalKey}},
			"match":  jsjson.Object{"mode": "exact"},
		},
	})
}

func validateSpec(spec Spec) error {
	if spec.Schema != SpecSchema || !tokenRE.MatchString(spec.Snapshot.ID) || !validSHA256(spec.Snapshot.SHA256) ||
		spec.Snapshot.Records < 1 || spec.Snapshot.Records > MaxSnapshotRecords ||
		spec.Snapshot.MaxRecords < spec.Snapshot.Records || spec.Snapshot.MaxRecords > MaxSnapshotRecords ||
		spec.Snapshot.MaxBytes < 1 || spec.Snapshot.MaxBytes > MaxSnapshotBytes ||
		spec.Adapter.Name != "catalog-identifier" || spec.Adapter.Version != "v1" ||
		!validSHA256(spec.Adapter.BehaviorSHA256) || !validSHA256(spec.Adapter.CodecSHA256) || !validSHA256(spec.Adapter.SearchSHA256) ||
		spec.Kurn.Path == "" || spec.Kurn.SHA256 != KurnBinarySHA256 || spec.Kurn.SourceRevision != KurnSourceRevision ||
		spec.Kurn.CompatibilityLabel != KurnCompatibilityLabel || spec.Kurn.DeadlineMillis < 1 || spec.Kurn.DeadlineMillis > profile.HardMaxTimeoutMillis ||
		!listRE.MatchString(spec.Bundle.PhysicalList) || spec.Bundle.Mode != "exact" || spec.Bundle.AnalyzerSHA256 != EmptyAnalyzerSHA256 ||
		spec.Policy.TimeoutMillis < 1 || spec.Policy.TimeoutMillis > profile.HardMaxTimeoutMillis ||
		spec.Policy.MaxInflight < 1 || spec.Policy.MaxInflight > profile.HardMaxInflight ||
		spec.Policy.WorstCaseChargeBytes < 1 || spec.Policy.WorstCaseChargeBytes > profile.HardMaxWorstCaseChargeBytes ||
		spec.Golden.EntryID == "" || len(spec.Golden.EntryID) > adapter.MaxEntryIDBytes ||
		spec.Golden.PhysicalKey == "" || utf8.RuneCountInString(spec.Golden.PhysicalKey) > adapter.MaxPhysicalKeyRunes ||
		!utf8.ValidString(spec.Golden.EntryID) || !utf8.ValidString(spec.Golden.PhysicalKey) {
		return errRefused
	}
	kurnPath, err := cleanAbsolute(spec.Kurn.Path)
	if err != nil || kurnPath != spec.Kurn.Path {
		return errRefused
	}
	if spec.Bundle.Predecessor != nil {
		previous, err := cleanAbsolute(spec.Bundle.Predecessor.Path)
		if err != nil || previous != spec.Bundle.Predecessor.Path || !validSHA256(spec.Bundle.Predecessor.CandidateSHA256) {
			return errRefused
		}
	}
	if err := validateAdapterIdentities(spec); err != nil {
		return errRefused
	}
	return nil
}

func validateAdapterIdentities(spec Spec) error {
	descriptor, err := catalogid.NewDescriptor(catalogid.OperationalBinding{
		PhysicalListName:     spec.Bundle.PhysicalList,
		ConfigSHA256:         strings.Repeat("0", 64),
		TestedEngines:        []string{KurnCompatibilityLabel},
		TimeoutMillis:        spec.Policy.TimeoutMillis,
		MaxInflight:          spec.Policy.MaxInflight,
		WorstCaseChargeBytes: spec.Policy.WorstCaseChargeBytes,
	})
	if err != nil || !descriptor.Valid() {
		return errRefused
	}
	identities := descriptor.Identities()
	if identities.Behavior.Hex() != spec.Adapter.BehaviorSHA256 || identities.Codec.Hex() != spec.Adapter.CodecSHA256 || identities.Search.Hex() != spec.Adapter.SearchSHA256 {
		return errRefused
	}
	return nil
}

func descriptorIdentity(spec Spec, configSHA string) (string, error) {
	descriptor, err := catalogid.NewDescriptor(catalogid.OperationalBinding{
		PhysicalListName:     spec.Bundle.PhysicalList,
		ConfigSHA256:         configSHA,
		TestedEngines:        []string{KurnCompatibilityLabel},
		TimeoutMillis:        spec.Policy.TimeoutMillis,
		MaxInflight:          spec.Policy.MaxInflight,
		WorstCaseChargeBytes: spec.Policy.WorstCaseChargeBytes,
	})
	if err != nil || !descriptor.Valid() {
		return "", errRefused
	}
	ids := descriptor.Identities()
	if ids.Behavior.Hex() != spec.Adapter.BehaviorSHA256 || ids.Codec.Hex() != spec.Adapter.CodecSHA256 || ids.Search.Hex() != spec.Adapter.SearchSHA256 {
		return "", errRefused
	}
	return digestBytes(descriptor.DiagnosticJSON()), nil
}
