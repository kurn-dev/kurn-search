package preparedsetup

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kurn-dev/kurn-search/adapters/partscatalogsuggest"
	"github.com/kurn-dev/kurn-search/internal/localproof"
)

func TestManifestCanonicalClosed(t *testing.T) {
	m := manifest{Schema: manifestSchema}
	raw, e := marshalManifest(m)
	if e != nil {
		t.Fatal(e)
	}
	var got manifest
	if strictDecode(raw, &got) != nil || got.Schema != manifestSchema {
		t.Fatal("round trip")
	}
	mut := append(raw[:len(raw)-2], []byte(",\"unknown\":1}\n")...)
	if strictDecode(mut, &got) == nil {
		t.Fatal("unknown accepted")
	}
}

func TestManifestFixedGoldenAndIncreasingKeys(t *testing.T) {
	m := manifest{Schema: manifestSchema}
	raw, err := marshalManifest(m)
	if err != nil {
		t.Fatal(err)
	}
	expected := "{\"asserted\":{\"core_inventory_sha256\":\"\",\"executables\":null,\"search_inventory_sha256\":\"\"},\"operational\":{\"admission\":\"\",\"config_sha256\":\"\",\"expected_base_sha256\":\"\",\"expected_entries\":0,\"max_inflight\":0,\"physical_list_name\":\"\",\"tested_engine\":\"\",\"timeout_millis\":0,\"worst_case_charge_bytes\":0},\"profile\":{\"behavior_sha256\":\"\",\"codec_sha256\":\"\",\"diagnostic_file\":\"\",\"diagnostic_sha256\":\"\",\"name\":\"\",\"search_sha256\":\"\",\"version\":\"\"},\"readiness\":{\"expected_id\":\"\",\"partition\":\"\",\"query\":\"\",\"tenant\":\"\"},\"recorded\":{\"artifact\":{\"container_sha256\":\"\",\"path\":\"\"},\"executables\":null,\"files\":null,\"mapping_sha256\":\"\",\"projected\":{\"keys\":0,\"lines_sha256\":\"\"},\"search_contract\":null,\"source\":{\"bytes\":0,\"records\":0,\"sha256\":\"\",\"tenants\":0}},\"reported\":{\"analyzer_declared\":\"\",\"analyzer_recomputed\":\"\",\"base_sha256\":\"\",\"collapsed\":0,\"config_declared\":\"\",\"config_recomputed\":\"\",\"container_sha256\":\"\",\"dropped_keys\":0,\"entries\":0,\"format\":\"\",\"input_sha256\":\"\",\"keyless_entries\":0,\"keys\":0,\"list\":\"\",\"mode\":\"\",\"verify_level\":\"\"},\"schema\":\"kurn-search-prepared-version/v1\"}\n"
	if string(raw) != expected {
		t.Fatalf("golden mismatch\nwant %s\n got %s", expected, raw)
	}
	assertIncreasingObjectKeys(t, raw)
}

func assertIncreasingObjectKeys(t *testing.T, raw []byte) {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var walk func() error
	walk = func() error {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			previous := ""
			for decoder.More() {
				nameToken, err := decoder.Token()
				if err != nil {
					return err
				}
				name := nameToken.(string)
				if previous != "" && name <= previous {
					return errRefused
				}
				previous = name
				if err := walk(); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		case '[':
			for decoder.More() {
				if err := walk(); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		default:
			return errRefused
		}
	}
	if err := walk(); err != nil {
		t.Fatalf("keys not strictly increasing: %v", err)
	}
}

func writeManifestFixture(t *testing.T, raw []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "manifest.json")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReadManifestClosedAndOrdered(t *testing.T) {
	canonical, err := marshalManifest(manifest{Schema: manifestSchema})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := readManifest(writeManifestFixture(t, canonical)); err != nil {
		t.Fatal(err)
	}
	index := bytes.LastIndex(canonical, []byte(",\"schema\":"))
	unsortedTop := append([]byte{'{'}, canonical[index+1:len(canonical)-2]...)
	unsortedTop = append(unsortedTop, ',')
	unsortedTop = append(unsortedTop, canonical[1:index]...)
	unsortedTop = append(unsortedTop, '}', '\n')
	unsortedNested := bytes.Replace(canonical, []byte("\"artifact\":{\"container_sha256\":\"\",\"path\":\"\"}"), []byte("\"artifact\":{\"path\":\"\",\"container_sha256\":\"\"}"), 1)
	nestedNull := bytes.Replace(canonical, []byte("\"artifact\":{\"container_sha256\":\"\",\"path\":\"\"}"), []byte("\"artifact\":null"), 1)
	unknown := append([]byte{}, canonical[:len(canonical)-2]...)
	unknown = append(unknown, []byte(",\"unknown\":1}\n")...)
	cases := map[string][]byte{
		"unknown":         unknown,
		"null":            []byte("null\n"),
		"nested-null":     nestedNull,
		"duplicate":       []byte("{\"schema\":\"kurn-search-prepared-version/v1\",\"schema\":\"kurn-search-prepared-version/v1\"}\n"),
		"missing":         []byte("{}\n"),
		"lone-surrogate":  []byte("{\"schema\":\"\\ud800\"}\n"),
		"noncanonical":    append([]byte(" "), canonical...),
		"extra-line-feed": append(append([]byte{}, canonical...), '\n'),
		"over-one-mib":    bytes.Repeat([]byte{' '}, maxManifest+1),
		"unsorted-top":    unsortedTop,
		"unsorted-nested": unsortedNested,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, _, err := readManifest(writeManifestFixture(t, raw)); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}

func testValidManifest(t *testing.T) manifest {
	t.Helper()
	h := strings.Repeat("a", 64)
	d, err := partscatalogsuggest.NewDescriptor(partscatalogsuggest.OperationalBinding{PhysicalListName: listName, ConfigSHA256: configSHA, TestedEngines: []string{localproof.KurnCompatibilityLabel}, TimeoutMillis: 30000, MaxInflight: 1, WorstCaseChargeBytes: 1073741824})
	if err != nil {
		t.Fatal(err)
	}
	ids := d.Identities()
	m := manifest{
		Schema:      manifestSchema,
		Profile:     profileManifest{Name: "parts-catalog-suggest", Version: "v1", CodecSHA256: ids.Codec.Hex(), SearchSHA256: ids.Search.Hex(), BehaviorSHA256: ids.Behavior.Hex(), DiagnosticFile: "profile-diagnostic.json", DiagnosticSHA256: bytesSHA(d.DiagnosticJSON())},
		Operational: operationalManifest{PhysicalListName: listName, ConfigSHA256: configSHA, TestedEngine: localproof.KurnCompatibilityLabel, TimeoutMillis: 30000, MaxInflight: 1, WorstCaseChargeBytes: 1073741824, Admission: "prepared_full", ExpectedBaseSHA256: h, ExpectedEntries: 1},
		Recorded:    recordedManifest{Source: sourceIdentity{SHA256: h, Bytes: 1, Records: 1, Tenants: 1}, Projected: projectedIdentity{LinesSHA256: h, Keys: 1}, MappingSHA256: bytesSHA(mappingBytes), Artifact: artifactIdentity{Path: "data/" + listName + ".kurn", ContainerSHA256: h}, Executables: map[string]byteIdentity{}, SearchContract: map[string]string{}, Files: map[string]fileIdentity{}},
		Reported:    reportedManifest{VerifyLevel: "full + semantic", List: listName, Mode: "ngram", Format: "KURNART1 1.1", Entries: 1, Keys: 1, BaseSHA256: h, ContainerSHA256: h, ConfigDeclared: configSHA, ConfigRecomputed: configSHA, AnalyzerDeclared: analyzerSHA, AnalyzerRecomputed: analyzerSHA, InputSHA256: h},
		Asserted:    assertedManifest{Executables: map[string]buildAssertion{}, CoreInventorySHA256: "not_supplied", SearchInventorySHA256: "not_supplied"},
		Readiness:   readinessManifest{Tenant: "alpha", Partition: "north", Query: "ZS-901", ExpectedID: "A01"},
	}
	for _, name := range []string{"kurn", "kurnd", "kurn-searchd", "kurn-search-local"} {
		m.Recorded.Executables[name] = byteIdentity{SHA256: h, Bytes: 1}
		m.Asserted.Executables[name] = buildAssertion{GoVersion: "go1", MainPath: "example/main", Path: "example/main", VCSRevision: "absent", VCSModified: "absent"}
	}
	for _, name := range []string{"openapi_manifest_sha256", "clients_manifest_sha256", "typescript_client_mjs_sha256", "typescript_client_dts_sha256"} {
		m.Recorded.SearchContract[name] = h
	}
	for name, mode := range immutableFileModes {
		m.Recorded.Files[name] = fileIdentity{SHA256: h, Mode: mode}
	}
	return m
}

func TestManifestCrossBindings(t *testing.T) {
	m := testValidManifest(t)
	if err := validateManifest(m); err != nil {
		t.Fatal(err)
	}
	m.Reported.InputSHA256 = strings.Repeat("b", 64)
	if err := validateManifest(m); err == nil {
		t.Fatal("accepted projected input mismatch")
	}
}

func TestManifestExactIntegerBoundaryAndFixedModes(t *testing.T) {
	m := testValidManifest(t)
	m.Recorded.Projected.Keys = maxExactInteger
	m.Reported.Keys = maxExactInteger
	raw, err := marshalManifest(m)
	if err != nil || !bytes.Contains(raw, []byte("9007199254740991")) {
		t.Fatalf("exact integer: %v %s", err, raw)
	}
	got, _, err := readManifest(writeManifestFixture(t, raw))
	if err != nil || got.Recorded.Projected.Keys != maxExactInteger || got.Reported.Keys != maxExactInteger || validateManifest(got) != nil {
		t.Fatalf("exact integer round trip: %#v %v", got.Recorded.Projected, err)
	}
	m.Recorded.Projected.Keys = maxExactInteger + 1
	m.Reported.Keys = uint64(maxExactInteger + 1)
	if validateManifest(m) == nil {
		t.Fatal("accepted over-limit integer")
	}
	for name, want := range immutableFileModes {
		m := testValidManifest(t)
		wrong := "0500"
		if want == wrong {
			wrong = "0400"
		}
		id := m.Recorded.Files[name]
		id.Mode = wrong
		m.Recorded.Files[name] = id
		if validateManifest(m) == nil {
			t.Fatalf("accepted wrong mode for %s", name)
		}
	}
}

const manifestTestHexA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const manifestTestHexB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

func populatedManifestGolden() []byte {
	h := manifestTestHexA
	build := "{\"go_version\":\"go1\",\"main_path\":\"example/main\",\"path\":\"example/main\",\"vcs_modified\":\"absent\",\"vcs_revision\":\"absent\"}"
	byteID := "{\"bytes\":1,\"sha256\":\"" + h + "\"}"
	file0400 := "{\"mode\":\"0400\",\"sha256\":\"" + h + "\"}"
	file0500 := "{\"mode\":\"0500\",\"sha256\":\"" + h + "\"}"
	return []byte("{\"asserted\":{\"core_inventory_sha256\":\"not_supplied\",\"executables\":{\"kurn\":" + build + ",\"kurn-search-local\":" + build + ",\"kurn-searchd\":" + build + ",\"kurnd\":" + build + "},\"search_inventory_sha256\":\"not_supplied\"}," +
		"\"operational\":{\"admission\":\"prepared_full\",\"config_sha256\":\"5c77cc033a580ee8f654fc6def1559713d527f2d3466006ea30735afc7eaecfc\",\"expected_base_sha256\":\"" + h + "\",\"expected_entries\":1,\"max_inflight\":1,\"physical_list_name\":\"parts-catalog-suggest-v1\",\"tested_engine\":\"v0.6.0-2-ge11dcf8\",\"timeout_millis\":30000,\"worst_case_charge_bytes\":1073741824}," +
		"\"profile\":{\"behavior_sha256\":\"ef4c10f157ecdace26c0a468d401de7ba581aa02fc15dd467ace2383a354ee7d\",\"codec_sha256\":\"34fe514caebf72a602620927a6fb28f7a00f3b4aba5e9df9a12861e120b46972\",\"diagnostic_file\":\"profile-diagnostic.json\",\"diagnostic_sha256\":\"dfb84c65873f0b8b711ebb185548fbd7714783b193b1128a184ce089382ec8cd\",\"name\":\"parts-catalog-suggest\",\"search_sha256\":\"b0264b4236d2cdc7e4eae64d590961019965c2395eefb10a8eea353236808681\",\"version\":\"v1\"}," +
		"\"readiness\":{\"expected_id\":\"A01\",\"partition\":\"north\",\"query\":\"ZS-901\",\"tenant\":\"alpha\"}," +
		"\"recorded\":{\"artifact\":{\"container_sha256\":\"" + h + "\",\"path\":\"data/parts-catalog-suggest-v1.kurn\"},\"executables\":{\"kurn\":" + byteID + ",\"kurn-search-local\":" + byteID + ",\"kurn-searchd\":" + byteID + ",\"kurnd\":" + byteID + "}," +
		"\"files\":{\"backend-token\":" + file0400 + ",\"bin/kurn-searchd\":" + file0500 + ",\"bin/kurnd\":" + file0500 + ",\"client-credentials.json\":" + file0400 + ",\"data/parts-catalog-suggest-v1.kurn\":" + file0400 + ",\"fixture-auth.json\":" + file0400 + ",\"kurn-searchd.json\":" + file0400 + ",\"kurnd-api-keys\":" + file0400 + ",\"profile-diagnostic.json\":" + file0400 + ",\"readiness.json\":" + file0400 + "}," +
		"\"mapping_sha256\":\"beb406aeb490a9268a5afd4854f02f6e049b5b54882b055c390e09be5b68eb20\",\"projected\":{\"keys\":1,\"lines_sha256\":\"" + h + "\"},\"search_contract\":{\"clients_manifest_sha256\":\"" + h + "\",\"openapi_manifest_sha256\":\"" + h + "\",\"typescript_client_dts_sha256\":\"" + h + "\",\"typescript_client_mjs_sha256\":\"" + h + "\"},\"source\":{\"bytes\":1,\"records\":1,\"sha256\":\"" + h + "\",\"tenants\":1}}," +
		"\"reported\":{\"analyzer_declared\":\"ba7fe7f6aaaef24d85e2db7ede33cc3c3009924863585953eb0fd3648f221bb1\",\"analyzer_recomputed\":\"ba7fe7f6aaaef24d85e2db7ede33cc3c3009924863585953eb0fd3648f221bb1\",\"base_sha256\":\"" + h + "\",\"collapsed\":0,\"config_declared\":\"5c77cc033a580ee8f654fc6def1559713d527f2d3466006ea30735afc7eaecfc\",\"config_recomputed\":\"5c77cc033a580ee8f654fc6def1559713d527f2d3466006ea30735afc7eaecfc\",\"container_sha256\":\"" + h + "\",\"dropped_keys\":0,\"entries\":1,\"format\":\"KURNART1 1.1\",\"input_sha256\":\"" + h + "\",\"keyless_entries\":0,\"keys\":1,\"list\":\"parts-catalog-suggest-v1\",\"mode\":\"ngram\",\"verify_level\":\"full + semantic\"},\"schema\":\"kurn-search-prepared-version/v1\"}\n")
}

func TestManifestPopulatedGoldenAndIncreasingKeys(t *testing.T) {
	m := testValidManifest(t)
	// Keep the exact populated golden as an admitted historical version.
	m.Operational.TestedEngine = "v0.6.0-2-ge11dcf8"
	m.Profile.DiagnosticSHA256 = "dfb84c65873f0b8b711ebb185548fbd7714783b193b1128a184ce089382ec8cd"
	raw, err := marshalManifest(m)
	want := populatedManifestGolden()
	if err != nil || !bytes.Equal(raw, want) {
		t.Fatalf("populated golden mismatch: %v\nwant %s\n got %s", err, want, raw)
	}
	assertIncreasingObjectKeys(t, raw)
	got, gotRaw, err := readManifest(writeManifestFixture(t, raw))
	if err != nil || !bytes.Equal(gotRaw, want) || validateManifest(got) != nil {
		t.Fatalf("populated positive: %v", err)
	}
}

func replaceManifestOnce(t *testing.T, raw []byte, old, replacement string) []byte {
	t.Helper()
	if bytes.Count(raw, []byte(old)) != 1 {
		t.Fatalf("mutation target count=%d for %q", bytes.Count(raw, []byte(old)), old)
	}
	return bytes.Replace(raw, []byte(old), []byte(replacement), 1)
}

func replaceFlatObjectWithNull(t *testing.T, raw []byte, name string) []byte {
	t.Helper()
	marker := []byte("\"" + name + "\":{")
	start := bytes.Index(raw, marker)
	if start < 0 {
		t.Fatalf("missing object %s", name)
	}
	brace := start + len(marker) - 1
	end := bytes.IndexByte(raw[brace:], '}')
	if end < 0 {
		t.Fatalf("unterminated object %s", name)
	}
	end += brace
	result := append([]byte{}, raw[:start]...)
	result = append(result, []byte("\""+name+"\":null")...)
	return append(result, raw[end+1:]...)
}

func assertSemanticManifestValid(t *testing.T, raw []byte) {
	t.Helper()
	var got manifest
	if strictDecode(raw, &got) != nil || validateManifest(got) != nil {
		t.Fatal("underlying typed content is not valid")
	}
}

func TestReadManifestPopulatedClosedAndOrdered(t *testing.T) {
	positive := populatedManifestGolden()
	got, gotRaw, err := readManifest(writeManifestFixture(t, positive))
	if err != nil || !bytes.Equal(gotRaw, positive) || validateManifest(got) != nil {
		t.Fatalf("positive control: %v", err)
	}
	topIndex := bytes.LastIndex(positive, []byte(",\"schema\":"))
	unsortedTop := append([]byte{'{'}, positive[topIndex+1:len(positive)-2]...)
	unsortedTop = append(unsortedTop, ',')
	unsortedTop = append(unsortedTop, positive[1:topIndex]...)
	unsortedTop = append(unsortedTop, '}', '\n')
	type orderingCase struct{ name, ordered, reversed string }
	ordering := []orderingCase{
		{name: "reported", ordered: "\"analyzer_declared\":\"" + analyzerSHA + "\",\"analyzer_recomputed\":\"" + analyzerSHA + "\"", reversed: "\"analyzer_recomputed\":\"" + analyzerSHA + "\",\"analyzer_declared\":\"" + analyzerSHA + "\""},
		{name: "file-identity", ordered: "\"backend-token\":{\"mode\":\"0400\",\"sha256\":\"" + manifestTestHexA + "\"}", reversed: "\"backend-token\":{\"sha256\":\"" + manifestTestHexA + "\",\"mode\":\"0400\"}"},
		{name: "recorded-executable", ordered: "\"kurn\":{\"bytes\":1,\"sha256\":\"" + manifestTestHexA + "\"}", reversed: "\"kurn\":{\"sha256\":\"" + manifestTestHexA + "\",\"bytes\":1}"},
		{name: "asserted-build-info", ordered: "\"kurn\":{\"go_version\":\"go1\",\"main_path\":\"example/main\",\"path\":\"example/main\",\"vcs_modified\":\"absent\",\"vcs_revision\":\"absent\"}", reversed: "\"kurn\":{\"main_path\":\"example/main\",\"go_version\":\"go1\",\"path\":\"example/main\",\"vcs_modified\":\"absent\",\"vcs_revision\":\"absent\"}"},
	}
	mutations := map[string][]byte{"top-level": unsortedTop}
	for _, test := range ordering {
		mutations[test.name] = replaceManifestOnce(t, positive, test.ordered, test.reversed)
	}
	for name, raw := range mutations {
		t.Run(name, func(t *testing.T) {
			assertSemanticManifestValid(t, raw)
			if _, _, err := readManifest(writeManifestFixture(t, raw)); err == nil {
				t.Fatal("canonical reader accepted unsorted document")
			}
			if name == "top-level" {
				prefix := []byte("{\"schema\":\"" + manifestSchema + "\",")
				if !bytes.HasPrefix(raw, prefix) {
					t.Fatal("top-level swap prefix missing")
				}
				restored := append([]byte{'{'}, raw[len(prefix):len(raw)-2]...)
				restored = append(restored, []byte(",\"schema\":\""+manifestSchema+"\"}\n")...)
				if !bytes.Equal(restored, positive) {
					t.Fatal("top-level reordering back did not restore positive bytes")
				}
			} else {
				var test orderingCase
				for _, candidate := range ordering {
					if candidate.name == name {
						test = candidate
					}
				}
				restored := replaceManifestOnce(t, raw, test.reversed, test.ordered)
				if !bytes.Equal(restored, positive) {
					t.Fatal("reordering back did not restore positive bytes")
				}
			}
		})
	}

	unknownTop := append([]byte{}, positive[:len(positive)-2]...)
	unknownTop = append(unknownTop, []byte(",\"unknown\":1}\n")...)
	closed := map[string][]byte{
		"unknown-top":             unknownTop,
		"unknown-nested":          replaceManifestOnce(t, positive, "\"tenant\":\"alpha\"", "\"tenant\":\"alpha\",\"unknown\":1"),
		"required-null":           replaceFlatObjectWithNull(t, positive, "profile"),
		"required-missing":        replaceManifestOnce(t, positive, ",\"schema\":\"kurn-search-prepared-version/v1\"", ""),
		"required-duplicate":      replaceManifestOnce(t, positive, "\"schema\":\"kurn-search-prepared-version/v1\"", "\"schema\":\"kurn-search-prepared-version/v1\",\"schema\":\"kurn-search-prepared-version/v1\""),
		"lone-surrogate":          replaceManifestOnce(t, positive, "\"query\":\"ZS-901\"", "\"query\":\"\\ud800\""),
		"noncanonical-whitespace": append([]byte{' '}, positive...),
		"noncanonical-escaping":   replaceManifestOnce(t, positive, "\"query\":\"ZS-901\"", "\"query\":\"\\u005aS-901\""),
		"extra-line-feed":         append(append([]byte{}, positive...), '\n'),
	}
	for name, raw := range closed {
		t.Run("closed-"+name, func(t *testing.T) {
			if bytes.Equal(raw, positive) {
				t.Fatal("mutation did not hit")
			}
			if _, _, err := readManifest(writeManifestFixture(t, raw)); err == nil {
				t.Fatal("strict reader accepted mutation")
			}
		})
	}
}

func TestManifestBindingTable(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*manifest)
	}{
		{name: "container", mutate: func(m *manifest) { m.Recorded.Artifact.ContainerSHA256 = manifestTestHexB }},
		{name: "input", mutate: func(m *manifest) { m.Reported.InputSHA256 = manifestTestHexB }},
		{name: "entries", mutate: func(m *manifest) { m.Recorded.Source.Records = 2 }},
		{name: "keys", mutate: func(m *manifest) { m.Recorded.Projected.Keys = 2 }},
		{name: "config-declared", mutate: func(m *manifest) { m.Reported.ConfigDeclared = manifestTestHexB }},
		{name: "config-recomputed", mutate: func(m *manifest) { m.Reported.ConfigRecomputed = manifestTestHexB }},
		{name: "operational-config", mutate: func(m *manifest) { m.Operational.ConfigSHA256 = manifestTestHexB }},
		{name: "analyzer-declared", mutate: func(m *manifest) { m.Reported.AnalyzerDeclared = manifestTestHexB }},
		{name: "analyzer-recomputed", mutate: func(m *manifest) { m.Reported.AnalyzerRecomputed = manifestTestHexB }},
		{name: "expected-base", mutate: func(m *manifest) { m.Operational.ExpectedBaseSHA256 = manifestTestHexB }},
		{name: "expected-entries", mutate: func(m *manifest) { m.Operational.ExpectedEntries = 2 }},
		{name: "diagnostic", mutate: func(m *manifest) { m.Profile.DiagnosticSHA256 = manifestTestHexB }},
		{name: "codec", mutate: func(m *manifest) { m.Profile.CodecSHA256 = manifestTestHexB }},
		{name: "search", mutate: func(m *manifest) { m.Profile.SearchSHA256 = manifestTestHexB }},
		{name: "behavior", mutate: func(m *manifest) { m.Profile.BehaviorSHA256 = manifestTestHexB }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			m := testValidManifest(t)
			if err := validateManifest(m); err != nil {
				t.Fatalf("positive control: %v", err)
			}
			test.mutate(&m)
			if err := validateManifest(m); err == nil {
				t.Fatal("accepted binding mutation")
			}
		})
	}
}

func TestVerifyVersionManifestHashCrossbindings(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, string, *manifest)
	}{
		{name: "diagnostic-file-vs-profile", mutate: func(t *testing.T, root string, m *manifest) {
			path := filepath.Join(root, "profile-diagnostic.json")
			if err := os.Chmod(path, 0600); err != nil || os.WriteFile(path, []byte("different diagnostic"), 0600) != nil || os.Chmod(path, 0400) != nil {
				t.Fatalf("diagnostic mutation: %v", err)
			}
			id := m.Recorded.Files["profile-diagnostic.json"]
			id.SHA256 = bytesSHA([]byte("different diagnostic"))
			m.Recorded.Files["profile-diagnostic.json"] = id
		}},
		{name: "artifact-file-vs-containers", mutate: func(t *testing.T, _ string, m *manifest) {
			m.Recorded.Artifact.ContainerSHA256 = manifestTestHexB
			m.Reported.ContainerSHA256 = manifestTestHexB
		}},
		{name: "kurnd-file-vs-executable", mutate: func(t *testing.T, _ string, m *manifest) {
			id := m.Recorded.Executables["kurnd"]
			id.SHA256 = manifestTestHexB
			m.Recorded.Executables["kurnd"] = id
		}},
		{name: "searchd-file-vs-executable", mutate: func(t *testing.T, _ string, m *manifest) {
			id := m.Recorded.Executables["kurn-searchd"]
			id.SHA256 = manifestTestHexB
			m.Recorded.Executables["kurn-searchd"] = id
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root, m, _ := newVerifyVersionFixture(t)
			if _, err := VerifyVersion(root); err != nil {
				t.Fatalf("positive control: %v", err)
			}
			test.mutate(t, root, &m)
			if err := validateManifest(m); err != nil {
				t.Fatalf("mutation failed early typed validation: %v", err)
			}
			writeVerifyManifest(t, root, m)
			if _, err := VerifyVersion(root); err == nil {
				t.Fatal("accepted late byte-inventory crossbinding mutation")
			}
		})
	}
}
