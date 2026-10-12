package release

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/kurn-dev/kurn-search/adapters/catalogid"
	"github.com/kurn-dev/kurn-search/internal/jsorder"
)

func TestCanonicalSpecAndClosedAuthorities(t *testing.T) {
	source := testSource()
	spec := testSpec(t, source)
	raw, err := MarshalSpec(spec)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Spec
	if err := decodeCanonical(raw, maxSpecBytes, &decoded); err != nil || decoded != spec {
		t.Fatalf("canonical spec did not round trip: %v", err)
	}
	compact, _ := json.Marshal(spec)
	if decodeCanonical(compact, maxSpecBytes, &decoded) == nil {
		t.Fatal("non-canonical spec accepted")
	}
	for name, invalid := range map[string][]byte{
		"trailing":     append(bytes.Clone(raw), []byte("null\n")...),
		"duplicate":    bytes.Replace(raw, []byte("  \"schema\":"), []byte("  \"schema\": \"kurn-search-release-spec/v1\",\n  \"schema\":"), 1),
		"unknown":      bytes.Replace(raw, []byte("{\n"), []byte("{\n  \"unknown\": 1,\n"), 1),
		"invalid-utf8": append(bytes.Clone(raw[:len(raw)-1]), 0xff),
	} {
		if decodeCanonical(invalid, maxSpecBytes, &decoded) == nil {
			t.Fatalf("%s spec accepted", name)
		}
	}
	mutations := map[string]func(*Spec){
		"driver":        func(value *Spec) { value.Adapter.Name = "unknown" },
		"behavior":      func(value *Spec) { value.Adapter.BehaviorSHA256 = strings.Repeat("1", 64) },
		"kurn-source":   func(value *Spec) { value.Kurn.SourceRevision = strings.Repeat("a", 40) },
		"kurn-binary":   func(value *Spec) { value.Kurn.SHA256 = strings.Repeat("2", 64) },
		"kurn-label":    func(value *Spec) { value.Kurn.CompatibilityLabel = "v0.0.0" },
		"mapping-mode":  func(value *Spec) { value.Bundle.Mode = "ngram" },
		"analyzer":      func(value *Spec) { value.Bundle.AnalyzerSHA256 = strings.Repeat("3", 64) },
		"relative-kurn": func(value *Spec) { value.Kurn.Path = "kurn" },
		"timeout":       func(value *Spec) { value.Kurn.DeadlineMillis = 0 },
	}
	for name, mutate := range mutations {
		changed := spec
		mutate(&changed)
		if validateSpec(changed) == nil {
			t.Fatalf("%s authority mismatch accepted", name)
		}
	}
}

func TestRegularInputRefusesExternalHardLink(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "input")
	alias := filepath.Join(root, "alias")
	if err := os.WriteFile(path, []byte("input"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(path, alias); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !hasMultipleLinks(info) {
		t.Skip("link-count proof unavailable on this platform")
	}
	if _, err := readRegular(path, 64); err == nil {
		t.Fatal("external hard-link alias was accepted")
	}
}

func TestProjectionIsOrderIndependentAndFailClosed(t *testing.T) {
	source := testSource()
	spec := testSpec(t, source)
	first, err := projectCatalog(source, spec)
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSuffix(source, []byte{'\n'}), []byte{'\n'})
	permuted := append(append([]byte(nil), lines[1]...), '\n')
	permuted = append(permuted, lines[0]...)
	permuted = append(permuted, '\n')
	second, err := projectCatalog(permuted, spec)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.feed, second.feed) || !bytes.Equal(first.mapping, second.mapping) || !bytes.Equal(first.base, second.base) || first.snapshotSHA256 != second.snapshotSHA256 {
		t.Fatal("source permutation changed canonical projection")
	}
	if first.entries != 2 || first.keys == 0 || !bytes.Contains(first.feed, []byte("keys")) {
		t.Fatal("projection census malformed")
	}

	badDigest := spec
	badDigest.Snapshot.SHA256 = strings.Repeat("0", 64)
	badGolden := spec
	badGolden.Golden.PhysicalKey = "missing-key"
	duplicate := append(bytes.Clone(lines[0]), '\n')
	duplicate = append(duplicate, lines[0]...)
	duplicate = append(duplicate, '\n')
	unknown := bytes.Replace(source, []byte("\"tenant_id\""), []byte("\"unknown\":\"x\",\"tenant_id\""), 1)
	invalidUTF8 := append(bytes.Clone(source), 0xff, '\n')
	for name, probe := range []struct {
		raw  []byte
		spec Spec
	}{
		{source[:len(source)-1], spec},
		{nil, spec},
		{duplicate, spec},
		{unknown, spec},
		{invalidUTF8, spec},
		{source, badDigest},
		{source, badGolden},
	} {
		if _, err := projectCatalog(probe.raw, probe.spec); err == nil {
			t.Fatalf("projection refusal %d accepted (%s)", name, probe.raw)
		}
	}
}

func TestProjectionUsesJavaScriptEntryOrderAndPreservesKeyOrder(t *testing.T) {
	source := []byte("{\"id\":\"\\ue000\",\"identifier\":\"ABC-123\",\"tenant_id\":\"tenant-main\"}\n" +
		"{\"id\":\"😀\",\"identifier\":\"SKU-314\",\"tenant_id\":\"tenant-main\"}\n")
	spec := testSpec(t, source)
	projection, err := projectCatalog(source, spec)
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSuffix(projection.feed, []byte{'\n'}), []byte{'\n'})
	if len(lines) != 2 {
		t.Fatal("JavaScript-order projection census drift")
	}
	var first feedRow
	if json.Unmarshal(lines[0], &first) != nil || first.ID != "😀" {
		t.Fatal("projection used UTF-8/Go ordering instead of JavaScript UTF-16 ordering")
	}
	projected, err := catalogid.New().Project([]byte("{\"id\":\"😀\",\"identifier\":\"SKU-314\",\"tenant_id\":\"tenant-main\"}"))
	if err != nil || len(projected) != 1 || len(first.Keys) != len(projected[0].Keys()) {
		t.Fatal("projector key authority unavailable")
	}
	for index, key := range first.Keys {
		if key.Value != projected[0].Keys()[index] {
			t.Fatal("projection reordered projector-owned keys")
		}
	}
}

func TestBoundedProcessBoundary(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	run := func(timeout time.Duration, mode string) ([]byte, error) {
		return runBounded(context.Background(), timeout, t.TempDir(), executable, "-test.run=TestReleaseProcessHelper", "--", mode)
	}
	output, err := run(5*time.Second, "success")
	if err != nil || string(output) != "ok" {
		t.Fatalf("bounded success mismatch: %q %v", output, err)
	}
	for _, mode := range []string{"failure", "stderr", "overflow", "timeout"} {
		limit := time.Second
		if mode == "timeout" {
			limit = time.Millisecond
		}
		if _, err := run(limit, mode); err == nil {
			t.Fatalf("bounded process %s accepted", mode)
		}
	}
}

func TestDryRunReportIsStrictlyReconciled(t *testing.T) {
	projection := projectionResult{entries: 2, keys: 4}
	valid := []byte("{\"sampled\":2,\"records\":2,\"filtered\":0,\"bad\":0,\"distinct_ids\":2,\"duplicate_ids\":0,\"raw_keys\":4,\"analyzed_keys\":4,\"empty_keys\":0,\"keyless_entries\":0,\"empty_key_rate\":0,\"est_memory_bytes\":532,\"samples\":[]}")
	if err := verifyDryRunOutput(valid, projection); err != nil {
		t.Fatalf("valid dry-run report refused: %v", err)
	}
	mutations := [][]byte{
		nil,
		[]byte("not-json"),
		append(bytes.Clone(valid), []byte("{}")...),
		bytes.Replace(valid, []byte("\"sampled\":2"), []byte("\"sampled\":1"), 1),
		bytes.Replace(valid, []byte("\"records\":2"), []byte("\"records\":1"), 1),
		bytes.Replace(valid, []byte("\"filtered\":0"), []byte("\"filtered\":1"), 1),
		bytes.Replace(valid, []byte("\"bad\":0"), []byte("\"bad\":1"), 1),
		bytes.Replace(valid, []byte("\"duplicate_ids\":0"), []byte("\"duplicate_ids\":1"), 1),
		bytes.Replace(valid, []byte("\"raw_keys\":4"), []byte("\"raw_keys\":3"), 1),
		bytes.Replace(valid, []byte("\"empty_keys\":0"), []byte("\"empty_keys\":1"), 1),
		bytes.Replace(valid, []byte("\"samples\":[]"), []byte("\"samples\":[{}]"), 1),
		bytes.Replace(valid, []byte("{\"sampled\""), []byte("{\"unknown\":0,\"sampled\""), 1),
		bytes.Repeat([]byte{'x'}, MaxProcessOutput+1),
	}
	for index, mutation := range mutations {
		if err := verifyDryRunOutput(mutation, projection); err == nil {
			t.Fatalf("dry-run report mutation %d was accepted", index)
		}
	}
}

func TestKurnBuildOutputIsExact(t *testing.T) {
	root := t.TempDir()
	bundle := BundleIdentity{BaseSHA256: strings.Repeat("a", 64), Entries: 2, Keys: 4, Mode: "exact", DeltaAdds: 1}
	valid := []byte("bundle " + root + ": 2 entries, 4 keys, base aaaaaaaaaaaa (mode exact)\n")
	if err := verifyKurnBuildOutput(valid, root, bundle, nil); err != nil {
		t.Fatalf("valid build output refused: %v", err)
	}
	predecessor := &PredecessorEvidence{BaseSHA256: strings.Repeat("b", 64)}
	withDelta := append(bytes.Clone(valid), []byte("delta vs bbbbbbbbbbbb: +1 ~0 -0\n")...)
	if err := verifyKurnBuildOutput(withDelta, root, bundle, predecessor); err != nil {
		t.Fatalf("valid predecessor build output refused: %v", err)
	}
	for index, mutation := range [][]byte{
		nil,
		bytes.TrimSuffix(valid, []byte{'\n'}),
		append(bytes.Clone(valid), []byte("unexpected\n")...),
		bytes.Replace(valid, []byte("2 entries"), []byte("1 entries"), 1),
		bytes.Replace(valid, []byte(root), []byte("other"), 1),
		withDelta[:len(valid)],
	} {
		link := predecessor
		if index < 5 {
			link = nil
		}
		if err := verifyKurnBuildOutput(mutation, root, bundle, link); err == nil {
			t.Fatalf("build output mutation %d was accepted", index)
		}
	}
}

func TestExactIndexContentIsIndependentlyVerified(t *testing.T) {
	base := []byte("{\"id\":\"alpha\",\"keys\":[\"a\",\"shared\"],\"payload\":{\"id\":\"alpha\"}}\n" +
		"{\"id\":\"beta\",\"keys\":[\"b\",\"shared\"],\"payload\":{\"id\":\"beta\"}}\n")
	projection := projectionResult{base: base}
	manifest := manifestV2{SHA256: strings.Repeat("a", 64), Analyzer: EmptyAnalyzerSHA256, Entries: 2, Keys: 4}
	valid := testExactIndexBytes(t, manifest, map[string][]uint32{"a": {0}, "b": {1}, "shared": {0, 1}})
	path := filepath.Join(t.TempDir(), "base.idx")
	if err := os.WriteFile(path, valid, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := verifyExactIndex(path, manifest, projection); err != nil {
		t.Fatalf("valid exact index refused: %v", err)
	}
	wrong := testExactIndexBytes(t, manifest, map[string][]uint32{"a": {1}, "b": {1}, "shared": {0, 1}})
	if err := os.WriteFile(path, wrong, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := verifyExactIndex(path, manifest, projection); err == nil {
		t.Fatal("structurally valid index with wrong postings was accepted")
	}
}

func TestDeltaEntriesMustMatchCurrentBase(t *testing.T) {
	base := []byte("{\"id\":\"alpha\",\"keys\":[\"a\"],\"payload\":{\"id\":\"alpha\"}}\n")
	matching := []byte("{\"op\":\"add\",\"id\":\"alpha\",\"entry\":{\"id\":\"alpha\",\"keys\":[\"a\"],\"payload\":{\"id\":\"alpha\"}}}\n")
	if err := verifyDeltaCurrent(matching, base); err != nil {
		t.Fatalf("matching current delta refused: %v", err)
	}
	wrongEntry := bytes.Replace(matching, []byte("\"keys\":[\"a\"]"), []byte("\"keys\":[\"b\"]"), 1)
	if err := verifyDeltaCurrent(wrongEntry, base); err == nil {
		t.Fatal("delta entry not present in the current base was accepted")
	}
	deletePresent := []byte("{\"op\":\"delete\",\"id\":\"alpha\"}\n")
	if err := verifyDeltaCurrent(deletePresent, base); err == nil {
		t.Fatal("delta deletion of a current entry was accepted")
	}
}

func testExactIndexBytes(t *testing.T, manifest manifestV2, postings map[string][]uint32) []byte {
	t.Helper()
	keys := make([]string, 0, len(postings))
	postingsLen, arenaLen := 0, 0
	for key, values := range postings {
		keys = append(keys, key)
		postingsLen += len(values)
		arenaLen += len(key)
	}
	sort.Strings(keys)
	header, err := json.Marshal(exactIndexMeta{
		KeyCount: len(keys), PostingsLen: postingsLen, ArenaLen: arenaLen, Analyzer: manifest.Analyzer,
		Build: &indexBuildInfo{BaseID: manifest.SHA256, Entries: manifest.Entries},
	})
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	output.WriteString(exactIndexMagic)
	var encoded [binary.MaxVarintLen64]byte
	count := binary.PutUvarint(encoded[:], uint64(len(header)))
	output.Write(encoded[:count])
	output.Write(header)
	for _, key := range keys {
		output.WriteString(key)
	}
	for _, key := range keys {
		count = binary.PutUvarint(encoded[:], uint64(len(key)))
		output.Write(encoded[:count])
		count = binary.PutUvarint(encoded[:], uint64(len(postings[key])))
		output.Write(encoded[:count])
	}
	var ordinal [4]byte
	for _, key := range keys {
		for _, value := range postings[key] {
			binary.LittleEndian.PutUint32(ordinal[:], value)
			output.Write(ordinal[:])
		}
	}
	return output.Bytes()
}

func TestReleaseProcessHelper(t *testing.T) {
	separator := -1
	for index, argument := range os.Args {
		if argument == "--" {
			separator = index
			break
		}
	}
	if separator < 0 || separator+1 >= len(os.Args) {
		return
	}
	switch os.Args[separator+1] {
	case "success":
		_, _ = os.Stdout.WriteString("ok")
	case "failure":
		os.Exit(7)
	case "stderr":
		_, _ = os.Stderr.WriteString("unexpected")
	case "overflow":
		_, _ = os.Stdout.Write(bytes.Repeat([]byte{'x'}, MaxProcessOutput+1))
	case "timeout":
		time.Sleep(time.Second)
	default:
		os.Exit(8)
	}
	os.Exit(0)
}

func testSource() []byte {
	return []byte("{\"id\":\"zulu\",\"identifier\":\"SKU-314\",\"tenant_id\":\"tenant-main\",\"partition_id\":\"partition-main\"}\n" +
		"{\"id\":\"alpha\",\"identifier\":\"ABC-123\",\"tenant_id\":\"tenant-main\",\"partition_id\":\"partition-main\"}\n")
}

func testSpec(t *testing.T, source []byte) Spec {
	t.Helper()
	lines := bytes.Split(bytes.TrimSuffix(source, []byte{'\n'}), []byte{'\n'})
	type item struct {
		id      string
		keys    []string
		payload []byte
	}
	items := make([]item, 0, len(lines))
	semantics := catalogid.New()
	for _, line := range lines {
		projected, err := semantics.Project(line)
		if err != nil || len(projected) != 1 {
			t.Fatal("test projection unavailable")
		}
		items = append(items, item{id: projected[0].EntryID(), keys: projected[0].Keys(), payload: projected[0].Payload()})
	}
	sort.Slice(items, func(i, j int) bool { return jsorder.Less(items[i].id, items[j].id) })
	hash := sha256.New()
	hash.Write([]byte("kurn-search canonical source snapshot v1\x00"))
	var length [8]byte
	for _, item := range items {
		binary.BigEndian.PutUint64(length[:], uint64(len(item.payload)))
		hash.Write(length[:])
		hash.Write(item.payload)
	}
	policy := PolicySpec{TimeoutMillis: 1000, MaxInflight: 1, WorstCaseChargeBytes: 1 << 20}
	descriptor, err := catalogid.NewDescriptor(catalogid.OperationalBinding{
		PhysicalListName: "neutral-release-v1", ConfigSHA256: strings.Repeat("0", 64), TestedEngines: []string{KurnCompatibilityLabel},
		TimeoutMillis: policy.TimeoutMillis, MaxInflight: policy.MaxInflight, WorstCaseChargeBytes: policy.WorstCaseChargeBytes,
	})
	if err != nil {
		t.Fatal(err)
	}
	ids := descriptor.Identities()
	return Spec{
		Schema:   SpecSchema,
		Snapshot: SnapshotSpec{ID: "neutral", SHA256: hex.EncodeToString(hash.Sum(nil)), Records: len(items), MaxBytes: MaxSnapshotBytes, MaxRecords: MaxSnapshotRecords},
		Adapter:  AdapterSpec{Name: "catalog-identifier", Version: "v1", BehaviorSHA256: ids.Behavior.Hex(), CodecSHA256: ids.Codec.Hex(), SearchSHA256: ids.Search.Hex()},
		Kurn:     KurnSpec{Path: "/bin/false", SHA256: KurnBinarySHA256, SourceRevision: KurnSourceRevision, CompatibilityLabel: KurnCompatibilityLabel, DeadlineMillis: 1000},
		Bundle:   BundleSpec{PhysicalList: "neutral-release-v1", Mode: "exact", AnalyzerSHA256: EmptyAnalyzerSHA256},
		Policy:   policy,
		Golden:   GoldenSpec{EntryID: items[0].id, PhysicalKey: items[0].keys[0]},
	}
}
