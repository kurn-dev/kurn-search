package release

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"unicode/utf8"

	"github.com/kurn-dev/kurn-search/adapter"
	"github.com/kurn-dev/kurn-search/adapters/catalogid"
	"github.com/kurn-dev/kurn-search/internal/jsorder"
	"github.com/kurn-dev/kurn-search/profile"
)

type verifiedCandidate struct {
	root       string
	result     Result
	candidate  Candidate
	evidence   PrivateEvidence
	projection projectionResult
}

func verify(candidatePath string) (Result, error) {
	checked, err := verifyRoot(candidatePath)
	if err != nil {
		return Result{}, errRefused
	}
	return checked.result, nil
}

func verifyRoot(candidatePath string) (verifiedCandidate, error) {
	root, err := cleanAbsolute(candidatePath)
	if err != nil {
		return verifiedCandidate{}, errRefused
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o700 {
		return verifiedCandidate{}, errRefused
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil || resolved != root || validateTreeShape(root) != nil {
		return verifiedCandidate{}, errRefused
	}
	candidateRaw, err := readRegular(filepath.Join(root, "candidate.json"), maxCandidateBytes)
	if err != nil {
		return verifiedCandidate{}, errRefused
	}
	var candidate Candidate
	if decodeCanonical(candidateRaw, maxCandidateBytes, &candidate) != nil || validateCandidateHeader(candidate) != nil {
		return verifiedCandidate{}, errRefused
	}
	evidenceRaw, err := readRegular(filepath.Join(root, "private", "evidence.json"), maxEvidenceBytes)
	if err != nil {
		return verifiedCandidate{}, errRefused
	}
	var evidence PrivateEvidence
	if decodeCanonical(evidenceRaw, maxEvidenceBytes, &evidence) != nil || validateEvidence(evidence) != nil {
		return verifiedCandidate{}, errRefused
	}
	projection, err := verifyMaterial(root, evidence)
	if err != nil {
		return verifiedCandidate{}, errRefused
	}
	hasPrevious := evidence.Predecessor != nil
	files, err := candidateFiles(root, hasPrevious, digestBytes(evidenceRaw))
	if err != nil || !reflect.DeepEqual(files, candidate.Files) {
		return verifiedCandidate{}, errRefused
	}
	bundle, err := verifyBundle(filepath.Join(root, "bundle"), projection, nil, evidence.Predecessor)
	if err != nil || !reflect.DeepEqual(bundle, candidate.Bundle) {
		return verifiedCandidate{}, errRefused
	}
	spec := specFromCandidate(candidate, evidence)
	profileSHA, err := descriptorIdentity(spec, bundle.ConfigSHA256)
	if err != nil || profileSHA != candidate.ProfileSHA256 {
		return verifiedCandidate{}, errRefused
	}
	wantRelation := "not-equal"
	if bundle.BaseSHA256 == S1InitialBaseSHA256 {
		wantRelation = "equal"
	}
	if candidate.S1Relation != wantRelation || !reflect.DeepEqual(candidate.Identities, identityFrame(spec, bundle, profileSHA)) {
		return verifiedCandidate{}, errRefused
	}
	result := Result{CandidateSHA256: digestBytes(candidateRaw), Entries: bundle.Entries, Keys: bundle.Keys, State: CandidateState}
	return verifiedCandidate{root: root, result: result, candidate: candidate, evidence: evidence, projection: projection}, nil
}

func validateTreeShape(root string) error {
	top, err := os.ReadDir(root)
	if err != nil || !namesEqual(top, []string{"bundle", "candidate.json", "private"}) {
		return errRefused
	}
	for _, directory := range []string{"bundle", "private"} {
		info, err := os.Lstat(filepath.Join(root, directory))
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o700 {
			return errRefused
		}
	}
	private, err := os.ReadDir(filepath.Join(root, "private"))
	if err != nil || !namesEqual(private, []string{"evidence.json", "feed.ndjson", "mapping.json"}) {
		return errRefused
	}
	paths := []string{filepath.Join(root, "candidate.json")}
	for _, name := range []string{"evidence.json", "feed.ndjson", "mapping.json"} {
		paths = append(paths, filepath.Join(root, "private", name))
	}
	bundle, err := os.ReadDir(filepath.Join(root, "bundle"))
	if err != nil {
		return errRefused
	}
	for _, entry := range bundle {
		paths = append(paths, filepath.Join(root, "bundle", entry.Name()))
	}
	for index, path := range paths {
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o600 {
			return errRefused
		}
		for prior := 0; prior < index; prior++ {
			if sameFile(paths[prior], path) {
				return errRefused
			}
		}
	}
	return nil
}

func namesEqual(entries []os.DirEntry, expected []string) bool {
	if len(entries) != len(expected) {
		return false
	}
	got := make([]string, len(entries))
	for index, entry := range entries {
		got[index] = entry.Name()
		if entry.Type()&os.ModeSymlink != 0 {
			return false
		}
	}
	sort.Strings(got)
	want := append([]string(nil), expected...)
	sort.Strings(want)
	return reflect.DeepEqual(got, want)
}

func validateCandidateHeader(candidate Candidate) error {
	if candidate.Schema != CandidateSchema || candidate.State != CandidateState || candidate.Adapter.Name != "catalog-identifier" || candidate.Adapter.Version != "v1" ||
		!validSHA256(candidate.Adapter.BehaviorSHA256) || !validSHA256(candidate.Adapter.CodecSHA256) || !validSHA256(candidate.Adapter.SearchSHA256) ||
		candidate.Kurn.SHA256 != KurnBinarySHA256 || candidate.Kurn.SourceRevision != KurnSourceRevision ||
		candidate.Kurn.CompatibilityLabel != KurnCompatibilityLabel || candidate.Kurn.GoVersion != requiredGoVersion ||
		candidate.Kurn.ModulePath != "github.com/kurn-dev/kurn" || candidate.Kurn.CommandPath != "github.com/kurn-dev/kurn/cmd/kurn" ||
		!validSHA256(candidate.ProfileSHA256) || (candidate.S1Relation != "equal" && candidate.S1Relation != "not-equal") || len(candidate.Identities) != 6 {
		return errRefused
	}
	return nil
}

func validateEvidence(evidence PrivateEvidence) error {
	if evidence.Schema != EvidenceSchema || !tokenRE.MatchString(evidence.Snapshot.ID) || !validSHA256(evidence.Snapshot.SHA256) ||
		evidence.Snapshot.Records < 1 || evidence.Snapshot.Records > MaxSnapshotRecords || evidence.Snapshot.Bytes < 1 || evidence.Snapshot.Bytes > MaxSnapshotBytes ||
		!validSHA256(evidence.FeedSHA256) || !validSHA256(evidence.MappingSHA256) || !listRE.MatchString(evidence.PhysicalList) ||
		evidence.Golden.EntryID == "" || evidence.Golden.PhysicalKey == "" || !utf8.ValidString(evidence.Golden.EntryID) || !utf8.ValidString(evidence.Golden.PhysicalKey) ||
		evidence.Policy.TimeoutMillis < 1 || evidence.Policy.TimeoutMillis > profile.HardMaxTimeoutMillis ||
		evidence.Policy.MaxInflight < 1 || evidence.Policy.MaxInflight > profile.HardMaxInflight ||
		evidence.Policy.WorstCaseChargeBytes < 1 || evidence.Policy.WorstCaseChargeBytes > profile.HardMaxWorstCaseChargeBytes {
		return errRefused
	}
	if evidence.Predecessor != nil && (!validSHA256(evidence.Predecessor.CandidateSHA256) || !validSHA256(evidence.Predecessor.BaseSHA256)) {
		return errRefused
	}
	return nil
}

func specFromCandidate(candidate Candidate, evidence PrivateEvidence) Spec {
	var predecessor *PredecessorSpec
	if evidence.Predecessor != nil {
		predecessor = &PredecessorSpec{Path: "/private-not-retained", CandidateSHA256: evidence.Predecessor.CandidateSHA256}
	}
	return Spec{
		Schema:   SpecSchema,
		Snapshot: SnapshotSpec{ID: evidence.Snapshot.ID, SHA256: evidence.Snapshot.SHA256, Records: evidence.Snapshot.Records, MaxBytes: evidence.Snapshot.Bytes, MaxRecords: evidence.Snapshot.Records},
		Adapter:  candidate.Adapter,
		Kurn:     KurnSpec{Path: "/private-not-retained", SHA256: candidate.Kurn.SHA256, SourceRevision: candidate.Kurn.SourceRevision, CompatibilityLabel: candidate.Kurn.CompatibilityLabel, DeadlineMillis: 1},
		Bundle:   BundleSpec{PhysicalList: evidence.PhysicalList, Mode: candidate.Bundle.Mode, AnalyzerSHA256: candidate.Bundle.AnalyzerSHA256, Predecessor: predecessor},
		Policy:   evidence.Policy, Golden: evidence.Golden,
	}
}

func verifyMaterial(root string, evidence PrivateEvidence) (projectionResult, error) {
	feed, err := readRegular(filepath.Join(root, "private", "feed.ndjson"), MaxSnapshotBytes*8)
	if err != nil || digestBytes(feed) != evidence.FeedSHA256 || len(feed) == 0 || feed[len(feed)-1] != '\n' {
		return projectionResult{}, errRefused
	}
	mapping, err := readRegular(filepath.Join(root, "private", "mapping.json"), maxEvidenceBytes)
	if err != nil || digestBytes(mapping) != evidence.MappingSHA256 {
		return projectionResult{}, errRefused
	}
	wantMapping, err := catalogMapping(evidence.Golden)
	if err != nil || !bytes.Equal(mapping, wantMapping) {
		return projectionResult{}, errRefused
	}
	lines := bytes.Split(feed[:len(feed)-1], []byte{'\n'})
	if len(lines) != evidence.Snapshot.Records {
		return projectionResult{}, errRefused
	}
	semantics := catalogid.New()
	var base bytes.Buffer
	snapshotHash := sha256.New()
	snapshotHash.Write([]byte("kurn-search canonical source snapshot v1\x00"))
	var length [8]byte
	keysTotal := 0
	prior := ""
	goldenFound := false
	for index, line := range lines {
		if len(line) == 0 || len(line) > adapter.MaxOpaqueJSONBytes+adapter.MaxPhysicalKeys*(adapter.MaxPhysicalKeyRunes*4+32) {
			return projectionResult{}, errRefused
		}
		var row feedRow
		if decodeStrict(line, len(line), &row) != nil || row.ID == "" || row.Identifier == "" || row.TenantID == "" || len(row.Keys) == 0 ||
			(row.PartitionID != nil && *row.PartitionID == "") {
			return projectionResult{}, errRefused
		}
		canonicalRow, err := json.Marshal(row)
		if err != nil || !bytes.Equal(canonicalRow, line) || (index > 0 && !jsorder.Less(prior, row.ID)) {
			return projectionResult{}, errRefused
		}
		prior = row.ID
		record := payloadRecord{ID: row.ID, Identifier: row.Identifier, PartitionID: row.PartitionID, TenantID: row.TenantID}
		payload, err := canonicalPayload(record)
		if err != nil {
			return projectionResult{}, errRefused
		}
		projectedEntries, err := semantics.Project(payload)
		if err != nil || len(projectedEntries) != 1 || projectedEntries[0].EntryID() != row.ID || !bytes.Equal(projectedEntries[0].Payload(), payload) {
			return projectionResult{}, errRefused
		}
		wantKeys := projectedEntries[0].Keys()
		if len(wantKeys) != len(row.Keys) {
			return projectionResult{}, errRefused
		}
		keys := make([]string, len(row.Keys))
		seen := map[string]struct{}{}
		for keyIndex, key := range row.Keys {
			if key.Value != wantKeys[keyIndex] {
				return projectionResult{}, errRefused
			}
			if _, exists := seen[key.Value]; exists {
				return projectionResult{}, errRefused
			}
			seen[key.Value] = struct{}{}
			keys[keyIndex] = key.Value
			if row.ID == evidence.Golden.EntryID && key.Value == evidence.Golden.PhysicalKey {
				goldenFound = true
			}
		}
		keysTotal += len(keys)
		binary.BigEndian.PutUint64(length[:], uint64(len(payload)))
		snapshotHash.Write(length[:])
		snapshotHash.Write(payload)
		baseRaw, err := json.Marshal(baseRow{ID: row.ID, Keys: keys, Payload: payload})
		if err != nil {
			return projectionResult{}, errRefused
		}
		base.Write(baseRaw)
		base.WriteByte('\n')
	}
	snapshotSHA := hex.EncodeToString(snapshotHash.Sum(nil))
	if snapshotSHA != evidence.Snapshot.SHA256 || !goldenFound {
		return projectionResult{}, errRefused
	}
	return projectionResult{feed: feed, base: base.Bytes(), mapping: mapping, snapshotSHA256: snapshotSHA, entries: len(lines), keys: keysTotal, golden: evidence.Golden}, nil
}

type manifestV2 struct {
	V            int         `json:"v"`
	SHA256       string      `json:"sha256"`
	Collapsed    int         `json:"collapsed,omitempty"`
	VersionID    string      `json:"version_id"`
	Analyzer     string      `json:"analyzer"`
	Mode         string      `json:"mode"`
	ConfigSHA256 string      `json:"config_sha256,omitempty"`
	Entries      int         `json:"entries"`
	Keys         int         `json:"keys"`
	Source       string      `json:"source,omitempty"`
	CreatedAt    string      `json:"created_at,omitempty"`
	PrevSHA256   string      `json:"prev_sha256,omitempty"`
	Delta        *deltaStats `json:"delta,omitempty"`
}

type deltaStats struct {
	Adds    int `json:"adds"`
	Updates int `json:"updates"`
	Deletes int `json:"deletes"`
}

func verifyBundle(directory string, projection projectionResult, predecessor *verifiedCandidate, link *PredecessorEvidence) (BundleIdentity, error) {
	entries, err := os.ReadDir(directory)
	wantNames := []string{"base.idx", "base.jsonl", "config.json", "manifest.json"}
	if link != nil {
		wantNames = append(wantNames, "delta.jsonl")
	}
	if err != nil || !namesEqual(entries, wantNames) {
		return BundleIdentity{}, bundleError("inventory")
	}
	manifestRaw, err := readRegular(filepath.Join(directory, "manifest.json"), maxEvidenceBytes)
	if err != nil {
		return BundleIdentity{}, bundleError("manifest-read")
	}
	names, err := topLevelNames(manifestRaw)
	if err != nil {
		return BundleIdentity{}, bundleError("manifest-names")
	}
	if _, present := names["source"]; present {
		return BundleIdentity{}, bundleError("manifest-source")
	}
	if _, present := names["created_at"]; present {
		return BundleIdentity{}, bundleError("manifest-created-at")
	}
	var manifest manifestV2
	if decodeStrict(manifestRaw, maxEvidenceBytes, &manifest) != nil || manifest.V != 2 || manifest.Collapsed != 0 ||
		!validSHA256(manifest.SHA256) || manifest.VersionID != manifest.SHA256[:12] || manifest.Analyzer != EmptyAnalyzerSHA256 ||
		manifest.Mode != "exact" || !validSHA256(manifest.ConfigSHA256) || manifest.Entries != projection.entries || manifest.Keys != projection.keys ||
		manifest.Source != "" || manifest.CreatedAt != "" {
		return BundleIdentity{}, bundleError("manifest-content")
	}
	baseRaw, err := readRegular(filepath.Join(directory, "base.jsonl"), maxBundleFile)
	if err != nil || !bytes.Equal(baseRaw, projection.base) || digestBytes(baseRaw) != manifest.SHA256 {
		return BundleIdentity{}, bundleError("base")
	}
	configRaw, err := readRegular(filepath.Join(directory, "config.json"), maxEvidenceBytes)
	if err != nil {
		return BundleIdentity{}, bundleError("config-read")
	}
	configSHA, err := verifyConfig(configRaw, projection)
	if err != nil || configSHA != manifest.ConfigSHA256 {
		return BundleIdentity{}, bundleError("config")
	}
	if err := verifyExactIndex(filepath.Join(directory, "base.idx"), manifest, projection); err != nil {
		return BundleIdentity{}, bundleError("index")
	}
	result := BundleIdentity{
		AnalyzerSHA256: manifest.Analyzer, BaseSHA256: manifest.SHA256, ConfigSHA256: manifest.ConfigSHA256,
		Entries: manifest.Entries, Keys: manifest.Keys, Mode: manifest.Mode,
	}
	if link == nil {
		if manifest.PrevSHA256 != "" || manifest.Delta != nil {
			return BundleIdentity{}, errRefused
		}
		return result, nil
	}
	if manifest.PrevSHA256 != link.BaseSHA256 || manifest.Delta == nil || manifest.Delta.Adds < 0 || manifest.Delta.Updates < 0 || manifest.Delta.Deletes < 0 {
		return BundleIdentity{}, errRefused
	}
	deltaRaw, counts, err := verifyDelta(filepath.Join(directory, "delta.jsonl"))
	if err != nil || counts != *manifest.Delta || verifyDeltaCurrent(deltaRaw, projection.base) != nil {
		return BundleIdentity{}, errRefused
	}
	if predecessor != nil {
		if predecessor.result.CandidateSHA256 != link.CandidateSHA256 || predecessor.candidate.Bundle.BaseSHA256 != link.BaseSHA256 {
			return BundleIdentity{}, errRefused
		}
		wantDelta, wantCounts, err := buildExpectedDelta(predecessor.projection.base, projection.base)
		if err != nil || !bytes.Equal(deltaRaw, wantDelta) || wantCounts != counts {
			return BundleIdentity{}, errRefused
		}
	}
	result.PredecessorSHA256 = manifest.PrevSHA256
	result.DeltaAdds, result.DeltaUpdates, result.DeltaDeletes = counts.Adds, counts.Updates, counts.Deletes
	return result, nil
}

func verifyDeltaCurrent(deltaRaw, currentBase []byte) error {
	current, err := baseLineMap(currentBase)
	if err != nil {
		return errRefused
	}
	if len(deltaRaw) == 0 {
		return nil
	}
	for _, line := range bytes.Split(deltaRaw[:len(deltaRaw)-1], []byte{'\n'}) {
		var record deltaRecord
		if json.Unmarshal(line, &record) != nil {
			return errRefused
		}
		currentLine, present := current[record.ID]
		switch record.Op {
		case "add", "update":
			if !present || !bytes.Equal(currentLine, record.Entry) {
				return errRefused
			}
		case "delete":
			if present {
				return errRefused
			}
		default:
			return errRefused
		}
	}
	return nil
}

func bundleError(reason string) error {
	_ = reason
	return errRefused
}

type analyzerConfig struct {
	Preset string   `json:"preset,omitempty"`
	Steps  []string `json:"steps,omitempty"`
}

type matchConfig struct {
	Mode        string  `json:"mode"`
	Fallback    string  `json:"fallback,omitempty"`
	Grams       []int   `json:"grams,omitempty"`
	StripSpaces bool    `json:"strip_spaces,omitempty"`
	Threshold   float64 `json:"threshold,omitempty"`
	TopK        int     `json:"topk,omitempty"`
}

type goldenProbe struct {
	Q        string  `json:"q"`
	ExpectID string  `json:"expect_id,omitempty"`
	Absent   bool    `json:"absent,omitempty"`
	MinScore float64 `json:"min_score,omitempty"`
}

type filterField struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type listConfig struct {
	Analyzer           analyzerConfig `json:"analyzer"`
	Match              matchConfig    `json:"match"`
	Golden             []goldenProbe  `json:"golden,omitempty"`
	Filterable         []filterField  `json:"filterable,omitempty"`
	OverlayAutoCompact int            `json:"overlay_auto_compact,omitempty"`
}

func verifyConfig(raw []byte, projection projectionResult) (string, error) {
	want := listConfig{
		Analyzer: analyzerConfig{}, Match: matchConfig{Mode: "exact"},
		Golden:     []goldenProbe{{Q: projection.golden.PhysicalKey, ExpectID: projection.golden.EntryID}},
		Filterable: []filterField{{Name: "partition_id", Path: "partition_id"}, {Name: "tenant_id", Path: "tenant_id"}},
	}
	compact, err := json.Marshal(want)
	if err != nil || !bytes.Equal(raw, append(compact, '\n')) {
		return "", errRefused
	}
	var decoded listConfig
	if decodeStrict(bytes.TrimSuffix(raw, []byte{'\n'}), len(raw), &decoded) != nil || !reflect.DeepEqual(decoded, want) {
		return "", errRefused
	}
	hash := sha256.New()
	hash.Write([]byte("kurn config v1\x00"))
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(compact)))
	hash.Write(length[:])
	hash.Write(compact)
	return hex.EncodeToString(hash.Sum(nil)), nil
}

type deltaRecord struct {
	Op    string          `json:"op"`
	ID    string          `json:"id"`
	Entry json.RawMessage `json:"entry,omitempty"`
}

func verifyDelta(path string) ([]byte, deltaStats, error) {
	raw, err := readRegular(path, maxBundleFile)
	if err != nil || (len(raw) > 0 && raw[len(raw)-1] != '\n') {
		return nil, deltaStats{}, errRefused
	}
	counts := deltaStats{}
	seen := map[string]struct{}{}
	if len(raw) == 0 {
		return raw, counts, nil
	}
	for _, line := range bytes.Split(raw[:len(raw)-1], []byte{'\n'}) {
		var record deltaRecord
		if len(line) == 0 || decodeStrict(line, len(line), &record) != nil || record.ID == "" {
			return nil, deltaStats{}, errRefused
		}
		canonical, err := json.Marshal(record)
		if err != nil || !bytes.Equal(canonical, line) {
			return nil, deltaStats{}, errRefused
		}
		if _, exists := seen[record.ID]; exists {
			return nil, deltaStats{}, errRefused
		}
		seen[record.ID] = struct{}{}
		switch record.Op {
		case "add":
			counts.Adds++
		case "update":
			counts.Updates++
		case "delete":
			counts.Deletes++
			if len(record.Entry) != 0 {
				return nil, deltaStats{}, errRefused
			}
		default:
			return nil, deltaStats{}, errRefused
		}
		if record.Op != "delete" {
			var entry baseRow
			if decodeStrict(record.Entry, len(record.Entry), &entry) != nil || entry.ID != record.ID || len(entry.Keys) == 0 {
				return nil, deltaStats{}, errRefused
			}
		}
	}
	return raw, counts, nil
}

func buildExpectedDelta(previous, next []byte) ([]byte, deltaStats, error) {
	prevLines, err := baseLineMap(previous)
	if err != nil {
		return nil, deltaStats{}, errRefused
	}
	nextLines, err := baseLineMap(next)
	if err != nil {
		return nil, deltaStats{}, errRefused
	}
	var output bytes.Buffer
	counts := deltaStats{}
	for _, line := range orderedBaseLines(next) {
		var entry baseRow
		if json.Unmarshal(line, &entry) != nil {
			return nil, deltaStats{}, errRefused
		}
		prior, exists := prevLines[entry.ID]
		if exists && bytes.Equal(prior, line) {
			continue
		}
		op := "add"
		if exists {
			op = "update"
			counts.Updates++
		} else {
			counts.Adds++
		}
		raw, _ := json.Marshal(deltaRecord{Op: op, ID: entry.ID, Entry: line})
		output.Write(raw)
		output.WriteByte('\n')
	}
	for _, line := range orderedBaseLines(previous) {
		var entry baseRow
		if json.Unmarshal(line, &entry) != nil {
			return nil, deltaStats{}, errRefused
		}
		if _, exists := nextLines[entry.ID]; exists {
			continue
		}
		counts.Deletes++
		raw, _ := json.Marshal(deltaRecord{Op: "delete", ID: entry.ID})
		output.Write(raw)
		output.WriteByte('\n')
	}
	return output.Bytes(), counts, nil
}

func baseLineMap(raw []byte) (map[string][]byte, error) {
	result := map[string][]byte{}
	for _, line := range orderedBaseLines(raw) {
		var entry baseRow
		if json.Unmarshal(line, &entry) != nil || entry.ID == "" {
			return nil, errRefused
		}
		result[entry.ID] = bytes.Clone(line)
	}
	return result, nil
}

func orderedBaseLines(raw []byte) [][]byte {
	if len(raw) == 0 {
		return nil
	}
	trimmed := bytes.TrimSuffix(raw, []byte{'\n'})
	if len(trimmed) == 0 {
		return nil
	}
	return bytes.Split(trimmed, []byte{'\n'})
}
