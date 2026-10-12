package localsetup

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/kurn-dev/kurn-search/adapters/partscatalog"
	"github.com/kurn-dev/kurn-search/internal/localproof"
)

// These 289 index bytes were reproduced with the pinned public kurn build
// from the checked catalogid feed/mapping. The independent live S1 generator
// must build the bundle again; this fixture only makes package tests hermetic.
const testIndexHex = "4b55524e4558413185027b226b65795f636f756e74223a312c22706f7374696e67735f6c656e223a312c226172656e615f6c656e223a31322c22616e616c797a6572223a2265336230633434323938666331633134396166626634633839393666623932343237616534316534363439623933346361343935393931623738353262383535222c226275696c64223a7b22626173655f6964223a2263343262646132666136343730363539353037376139313331393334643630643733653339376136663362326337303039643139666561643833646231386430222c22656e7472696573223a312c2264726f707065645f6b657973223a302c226b65796c6573735f656e7472696573223a307d7d653a59574a6a4c5445794d770c0100000000"

func testManifest() map[string]any {
	return map[string]any{
		"v": 2, "sha256": bundleHashes["base.jsonl"], "version_id": "c42bda2fa647",
		"analyzer": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		"mode":     "exact", "config_sha256": ConfigSHA256, "entries": 1, "keys": 1,
	}
}

func jsonBytes(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return append(raw, '\n')
}

func put(t *testing.T, path string, raw []byte) {
	t.Helper()
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func privateTemp(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if os.Chmod(root, 0o700) != nil {
		t.Fatal("private temp chmod")
	}
	return root
}

func fixture(t *testing.T) Options {
	t.Helper()
	parent := privateTemp(t)
	bundle := filepath.Join(parent, "bundle")
	if os.Mkdir(bundle, 0o700) != nil {
		t.Fatal("bundle mkdir")
	}
	idx, err := hex.DecodeString(testIndexHex)
	if err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(bundle, "manifest.json"), jsonBytes(t, testManifest()))
	put(t, filepath.Join(bundle, "config.json"), []byte("{\"analyzer\":{},\"match\":{\"mode\":\"exact\"},\"filterable\":[{\"name\":\"partition_id\",\"path\":\"partition_id\"},{\"name\":\"tenant_id\",\"path\":\"tenant_id\"}]}\n"))
	put(t, filepath.Join(bundle, "base.jsonl"), []byte("{\"id\":\"config-proof\",\"keys\":[\"e:YWJjLTEyMw\"],\"payload\":{\"id\":\"config-proof\",\"identifier\":\"ABC-123\",\"partition_id\":\"partition-proof\",\"tenant_id\":\"tenant-proof\"}}\n"))
	put(t, filepath.Join(bundle, "base.idx"), idx)
	return Options{Bundle: bundle, Out: filepath.Join(parent, "prepared"), KurndAddress: "127.0.0.1:18080", SearchdAddress: "127.0.0.1:18081"}
}

func cliArgs(options Options) []string {
	args := []string{"prepare", "-bundle", options.Bundle, "-out", options.Out, "-kurnd-addr", options.KurndAddress, "-searchd-addr", options.SearchdAddress}
	if options.Recipe != "" {
		args = append(args, "-recipe", options.Recipe)
	}
	return args
}

func reservation(options Options) string {
	return filepath.Join(filepath.Dir(options.Out), "."+filepath.Base(options.Out)+reservationSuffix)
}

func assertRefused(t *testing.T, options Options) {
	t.Helper()
	var out, errOut bytes.Buffer
	if Run(cliArgs(options), &out, &errOut) != 1 || out.Len() != 0 || errOut.String() != "kurn-search-local: refused\n" {
		t.Fatal("refusal protocol differs")
	}
}

func TestPreparePublishedInventory(t *testing.T) {
	options := fixture(t)
	input, err := readBundle(options.Bundle, catalogRecipe())
	if err != nil {
		t.Fatal(err)
	}
	result, err := Prepare(options)
	if err != nil {
		t.Fatal(err)
	}
	want := Result{ConfigSHA256: ConfigSHA256, Directories: 2, Files: 7, PhysicalListName: PhysicalListName, Schema: ResultSchema, Status: "PREPARED"}
	if result != want || !absent(reservation(options)) {
		t.Fatal("inventory or reservation")
	}
	loaded, err := localproof.Load(filepath.Join(options.Out, "kurn-searchd.json"))
	if err != nil || loaded.Root != options.Out || loaded.ConfigSHA256 != ConfigSHA256 ||
		loaded.ListenAddress != options.SearchdAddress || !absent(loaded.RuntimeStatusFile) {
		t.Fatal("published loader")
	}
	after, err := readBundle(options.Bundle, catalogRecipe())
	if err != nil || !reflect.DeepEqual(input, after) {
		t.Fatal("input bundle changed")
	}
	files := fixtureFiles()
	for name, raw := range input {
		files[filepath.Join("data", PhysicalListName, name)] = raw
	}
	files["kurn-searchd.json"], err = configurationBytes(configuration(options.Out, options, catalogRecipe()))
	if err != nil {
		t.Fatal(err)
	}
	if count, dirs, err := checkInventory(options.Out, files, catalogRecipe()); err != nil || count != 7 || dirs != 2 {
		t.Fatal("checked inventory")
	}
}

func TestRunCanonicalPathNeutralResult(t *testing.T) {
	options := fixture(t)
	var out, errOut bytes.Buffer
	if Run(cliArgs(options), &out, &errOut) != 0 || errOut.Len() != 0 {
		t.Fatal("run failed", errOut.String())
	}
	expected := "{\"config_sha256\":\"" + ConfigSHA256 + "\",\"directories\":2,\"files\":7,\"physical_list_name\":\"catalog-identifiers-v1\",\"schema\":\"kurn-search-local-prepare-result/v1\",\"status\":\"PREPARED\"}\n"
	if out.String() != expected {
		t.Fatal("not exact canonical result")
	}
}

func TestRunStrictArguments(t *testing.T) {
	options := fixture(t)
	good := cliArgs(options)
	tests := map[string][]string{
		"missing-flag": good[:7], "unknown-command": append([]string{"reset"}, good[1:]...),
		"unknown-flag":  append([]string{"prepare", "-unknown", options.Bundle}, good[3:]...),
		"repeated-flag": append(append([]string(nil), good[:7]...), "-kurnd-addr", options.SearchdAddress),
		"positional":    append(append([]string(nil), good...), "extra"),
		"empty":         {}, "empty-value": append([]string{"prepare", "-bundle", ""}, good[3:]...),
	}
	for name, args := range tests {
		t.Run(name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			if Run(args, &out, &errOut) != 1 || out.Len() != 0 || errOut.String() != "kurn-search-local: refused\n" {
				t.Fatal("invalid argument accepted or diagnostic leaked")
			}
			if !absent(options.Out) || !absent(reservation(options)) {
				t.Fatal("arguments caused writes")
			}
		})
	}
}

func TestManifestRequiredTypesAndNulls(t *testing.T) {
	fields := []string{"v", "sha256", "version_id", "analyzer", "mode", "config_sha256", "entries", "keys"}
	for _, field := range fields {
		for _, mutation := range []string{"missing", "wrong-type", "null", "wrong-value"} {
			t.Run(field+"/"+mutation, func(t *testing.T) {
				value := testManifest()
				switch mutation {
				case "missing":
					delete(value, field)
				case "wrong-type":
					value[field] = []string{"invalid"}
				case "null":
					value[field] = nil
				case "wrong-value":
					value[field] = "invalid"
				}
				if validateManifest(jsonBytes(t, value), catalogRecipe()) {
					t.Fatal("bad manifest accepted")
				}
			})
		}
	}
}

func TestManifestClosedShape(t *testing.T) {
	raw := jsonBytes(t, testManifest())
	valid := append([]byte(" \n"), raw...)
	if !validateManifest(valid, catalogRecipe()) {
		t.Fatal("valid whitespace refused")
	}
	mutations := map[string][]byte{
		"duplicate":         bytes.Replace(raw, []byte("\"v\":2"), []byte("\"v\":2,\"v\":2"), 1),
		"escaped-duplicate": bytes.Replace(raw, []byte("\"v\":2"), []byte("\"v\":2,\"\\u0076\":2"), 1),
		"unknown":           bytes.Replace(raw, []byte("\"v\":2"), []byte("\"v\":2,\"unknown\":0"), 1),
		"trailing":          append(append([]byte(nil), raw...), []byte("{}")...),
		"array":             []byte("[]"), "null": []byte("null"), "invalid-utf8": {0xff},
		"fractional-integer": bytes.Replace(raw, []byte("\"v\":2"), []byte("\"v\":2.0"), 1),
	}
	for name, value := range mutations {
		t.Run(name, func(t *testing.T) {
			if validateManifest(value, catalogRecipe()) {
				t.Fatal("closed codec accepted mutation")
			}
		})
	}
}

func TestBundleFilesAndBounds(t *testing.T) {
	for _, file := range []string{"manifest.json", "base.jsonl", "base.idx", "config.json"} {
		for _, mutation := range []string{"missing", "corrupt", "hardlink", "symlink", "writable-mode", "directory", "oversize"} {
			t.Run(file+"/"+mutation, func(t *testing.T) {
				options := fixture(t)
				path := filepath.Join(options.Bundle, file)
				switch mutation {
				case "missing":
					if os.Remove(path) != nil {
						t.Fatal("remove")
					}
				case "corrupt":
					raw, _ := os.ReadFile(path)
					raw[len(raw)/2] ^= 1
					put(t, path, raw)
				case "hardlink":
					if os.Link(path, filepath.Join(filepath.Dir(options.Bundle), "linked")) != nil {
						t.Fatal("link")
					}
				case "symlink":
					target := filepath.Join(filepath.Dir(options.Bundle), "target")
					if os.Rename(path, target) != nil || os.Symlink(target, path) != nil {
						t.Fatal("symlink")
					}
				case "writable-mode":
					if os.Chmod(path, 0o666) != nil {
						t.Fatal("chmod")
					}
				case "directory":
					if os.Remove(path) != nil || os.Mkdir(path, 0o700) != nil {
						t.Fatal("directory")
					}
				case "oversize":
					size := int64(maxDataBytes + 1)
					if file == "manifest.json" {
						size = maxManifestBytes + 1
					}
					if os.Truncate(path, size) != nil {
						t.Fatal("truncate")
					}
				}
				assertRefused(t, options)
				if !absent(options.Out) || !absent(reservation(options)) {
					t.Fatal("bundle refusal caused output")
				}
			})
		}
	}
	t.Run("extra-file", func(t *testing.T) {
		options := fixture(t)
		put(t, filepath.Join(options.Bundle, "extra"), []byte("x"))
		assertRefused(t, options)
	})
}

func TestPathAndEndpointRefusals(t *testing.T) {
	for _, address := range []string{"localhost:8080", "[::1]:8080", "0.0.0.0:8080", "127.0.0.1:0", "127.0.0.1:65536", "127.0.0.1:08080", "127.0.0.1:http", "127.0.0.1:+8080"} {
		t.Run(address, func(t *testing.T) {
			options := fixture(t)
			options.KurndAddress = address
			assertRefused(t, options)
		})
	}
	for _, mutation := range []string{"same-port", "relative-out", "relative-bundle", "unclean-out", "out-in-bundle", "bundle-in-out", "parent-mode", "checkout-dir", "checkout-file", "symlink-parent", "symlink-bundle"} {
		t.Run(mutation, func(t *testing.T) {
			options := fixture(t)
			parent := filepath.Dir(options.Out)
			switch mutation {
			case "same-port":
				options.SearchdAddress = options.KurndAddress
			case "relative-out":
				options.Out = "relative"
			case "relative-bundle":
				options.Bundle = "relative"
			case "unclean-out":
				options.Out = parent + "/./prepared"
			case "out-in-bundle":
				options.Out = filepath.Join(options.Bundle, "prepared")
			case "bundle-in-out":
				options.Out = parent
			case "parent-mode":
				if os.Chmod(parent, 0o755) != nil {
					t.Fatal("chmod")
				}
			case "checkout-dir":
				if os.Mkdir(filepath.Join(parent, ".git"), 0o700) != nil {
					t.Fatal("mkdir")
				}
			case "checkout-file":
				put(t, filepath.Join(parent, ".git"), []byte("gitdir: unused"))
			case "symlink-parent":
				link := filepath.Join(privateTemp(t), "parent")
				if os.Symlink(parent, link) != nil {
					t.Fatal("symlink")
				}
				options.Out = filepath.Join(link, "prepared")
			case "symlink-bundle":
				link := filepath.Join(parent, "linked")
				if os.Symlink(options.Bundle, link) != nil {
					t.Fatal("symlink")
				}
				options.Bundle = link
			}
			assertRefused(t, options)
		})
	}
}

func TestExistingObjectsArePreserved(t *testing.T) {
	for _, kind := range []string{"file", "empty-directory", "nonempty-directory", "symlink", "reservation"} {
		t.Run(kind, func(t *testing.T) {
			options := fixture(t)
			target := options.Out
			if kind == "reservation" {
				target = reservation(options)
			}
			switch kind {
			case "file":
				put(t, target, []byte("retain"))
			case "symlink":
				if os.Symlink(options.Bundle, target) != nil {
					t.Fatal("symlink")
				}
			default:
				if os.Mkdir(target, 0o700) != nil {
					t.Fatal("mkdir")
				}
				if kind != "empty-directory" {
					put(t, filepath.Join(target, "retain"), []byte("retain"))
				}
			}
			before, _ := os.Lstat(target)
			assertRefused(t, options)
			after, err := os.Lstat(target)
			if err != nil || !os.SameFile(before, after) {
				t.Fatal("preexisting object removed/replaced")
			}
			if kind == "file" {
				raw, _ := os.ReadFile(target)
				if string(raw) != "retain" {
					t.Fatal("file changed")
				}
			}
			if kind == "nonempty-directory" || kind == "reservation" {
				raw, _ := os.ReadFile(filepath.Join(target, "retain"))
				if string(raw) != "retain" {
					t.Fatal("contents changed")
				}
			}
		})
	}
}

func TestCooperatingHelperExclusion(t *testing.T) {
	options := fixture(t)
	reserved, resume := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := prepare(options, &prepareHooks{afterReserve: func() error { close(reserved); <-resume; return nil }})
		done <- err
	}()
	<-reserved
	_, competitorErr := Prepare(options)
	close(resume)
	firstErr := <-done
	if competitorErr == nil || firstErr != nil {
		t.Fatal("cooperating reservation did not exclude contender")
	}
	// This controlled interleaving is not an adversarial filesystem proof.
}

func TestConcurrentDestinationBeforePublicationPreserved(t *testing.T) {
	options := fixture(t)
	_, err := prepare(options, &prepareHooks{beforePublish: func() error {
		if err := os.Mkdir(options.Out, 0o700); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(options.Out, "retain"), []byte("retain"), 0o600)
	}})
	raw, readErr := os.ReadFile(filepath.Join(options.Out, "retain"))
	if err == nil || readErr != nil || string(raw) != "retain" || !absent(reservation(options)) {
		t.Fatal("concurrent existing destination not preserved")
	}
}

func TestOwnedFailureCleanup(t *testing.T) {
	for _, point := range []string{"after-reserve", "before-publish"} {
		t.Run(point, func(t *testing.T) {
			options := fixture(t)
			before, _ := readBundle(options.Bundle, catalogRecipe())
			hook := func() error { return errors.New("injected") }
			hooks := &prepareHooks{afterReserve: hook}
			if point == "before-publish" {
				hooks = &prepareHooks{beforePublish: hook}
			}
			_, err := prepare(options, hooks)
			after, _ := readBundle(options.Bundle, catalogRecipe())
			if err == nil || !absent(options.Out) || !absent(reservation(options)) || !reflect.DeepEqual(before, after) {
				t.Fatal("failed transaction left residue or changed input")
			}
		})
	}
}

func TestStageFinalConfigurationEquivalence(t *testing.T) {
	options := fixture(t)
	staged, final := configuration(reservation(options), options, catalogRecipe()), configuration(options.Out, options, catalogRecipe())
	if !sameNonPathFields(staged, final) {
		t.Fatal("non-path fields differ")
	}
	left, right := reflect.ValueOf(staged), reflect.ValueOf(final)
	var fields []string
	for index := 0; index < left.NumField(); index++ {
		if !reflect.DeepEqual(left.Field(index).Interface(), right.Field(index).Interface()) {
			fields = append(fields, left.Type().Field(index).Name)
		}
	}
	sort.Strings(fields)
	want := []string{"BackendCredentialFile", "FixtureAuthenticatorFile", "ReadinessFile", "RuntimeStatusFile"}
	if !reflect.DeepEqual(fields, want) {
		t.Fatal("path difference set", fields)
	}
	final.ListenAddress = "127.0.0.1:19999"
	if sameNonPathFields(staged, final) {
		t.Fatal("non-path drift accepted")
	}
}

func TestPublishedValidationFailureCleansOnlyOwnedOutput(t *testing.T) {
	options := fixture(t)
	before, _ := readBundle(options.Bundle, catalogRecipe())
	_, err := prepare(options, &prepareHooks{beforePublish: func() error {
		return os.WriteFile(filepath.Join(reservation(options), "kurn-searchd.json"), []byte("{}\n"), 0o600)
	}})
	after, _ := readBundle(options.Bundle, catalogRecipe())
	if err == nil || !absent(options.Out) || !absent(reservation(options)) || !reflect.DeepEqual(before, after) {
		t.Fatal("post-publication validation failure did not clean owned output")
	}
}

func TestConfigurationPathsAreJSONEscaped(t *testing.T) {
	options := fixture(t)
	options.Out = filepath.Join(filepath.Dir(options.Out), "space and \"quote")
	if _, err := Prepare(options); err != nil {
		t.Fatal("JSON-escaped physical root refused", err)
	}
	value, err := localproof.Load(filepath.Join(options.Out, "kurn-searchd.json"))
	if err != nil || value.Root != options.Out {
		t.Fatal("escaped root does not round-trip through unchanged loader")
	}
}

func TestManualFixtureByteIdentity(t *testing.T) {
	options := fixture(t)
	if _, err := Prepare(options); err != nil {
		t.Fatal(err)
	}
	manual, err := os.ReadFile("../docs/manual-fixtures.md")
	if err != nil {
		t.Fatal(err)
	}
	var blocks []string
	for _, name := range []string{"local-inputs", "service-config"} {
		marker := "<!-- w6s0:" + name + ":start -->"
		part := strings.Split(string(manual), marker)
		if len(part) != 2 {
			t.Fatal("missing or repeated manual block")
		}
		body := strings.SplitN(part[1], "<!-- w6s0:"+name+":end -->", 2)
		if len(body) != 2 {
			t.Fatal("manual end")
		}
		code := strings.SplitN(body[0], "```sh\n", 2)
		if len(code) != 2 {
			t.Fatal("manual shell fence")
		}
		command := strings.SplitN(code[1], "\n```", 2)
		if len(command) != 2 {
			t.Fatal("manual fence end")
		}
		blocks = append(blocks, command[0])
	}
	root := privateTemp(t)
	command := exec.Command("sh", "-e")
	command.Env = []string{"PATH=" + os.Getenv("PATH"), "LANG=C", "LC_ALL=C",
		"KURN_SEARCH_RUN_ROOT=" + root, "KURND_ADDR=" + options.KurndAddress, "KURN_SEARCHD_ADDR=" + options.SearchdAddress}
	command.Stdin = strings.NewReader("umask 077\n" + strings.Join(blocks, "\n"))
	if raw, err := command.CombinedOutput(); err != nil {
		t.Fatalf("manual blocks: %v %s", err, raw)
	}
	for _, name := range []string{"backend-token", "fixture-auth.json", "readiness.json", "kurn-searchd.json"} {
		t.Run(name, func(t *testing.T) {
			manualRaw, err := os.ReadFile(filepath.Join(root, name))
			if err != nil {
				t.Fatal(err)
			}
			manualRaw = bytes.ReplaceAll(manualRaw, []byte(root), []byte(options.Out))
			helperRaw, err := os.ReadFile(filepath.Join(options.Out, name))
			if err != nil || !bytes.Equal(manualRaw, helperRaw) {
				t.Fatal("helper/manual bytes differ")
			}
		})
	}
}

// This gzip/base64 is only a compact representation of the 21805 index bytes
// from two independently executed pinned public builds. Its decoded SHA must
// match the production recipe pin. Live gates rebuild; this does not claim a
// new public build during hermetic package tests.
const partsTestIndexGzip = "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"

func partsFixture(t *testing.T) Options {
	t.Helper()
	parent := privateTemp(t)
	options := Options{Bundle: filepath.Join(parent, "bundle"), Out: filepath.Join(parent, "prepared"),
		KurndAddress: "127.0.0.1:18080", SearchdAddress: "127.0.0.1:18081", Recipe: "parts-catalog"}
	if err := os.Mkdir(options.Bundle, 0o700); err != nil {
		t.Fatal(err)
	}
	recipe, _ := recipeForName(options.Recipe)
	manifest := testManifest()
	manifest["sha256"], manifest["version_id"] = recipe.bundleHashes["base.jsonl"], recipe.bundleHashes["base.jsonl"][:12]
	manifest["config_sha256"], manifest["entries"], manifest["keys"] = recipe.configSHA256, recipe.entries, recipe.keys
	put(t, filepath.Join(options.Bundle, "manifest.json"), jsonBytes(t, manifest))
	put(t, filepath.Join(options.Bundle, "config.json"), []byte(`{"analyzer":{},"match":{"mode":"exact"},"filterable":[{"name":"category","path":"category"},{"name":"manufacturer","path":"manufacturer"},{"name":"partition_id","path":"partition_id"},{"name":"status","path":"status"},{"name":"tenant_id","path":"tenant_id"}]}`+"\n"))
	var fixture struct {
		Records []json.RawMessage `json:"records"`
	}
	if err := json.Unmarshal(partscatalog.NeutralFixture(), &fixture); err != nil {
		t.Fatal(err)
	}
	var base bytes.Buffer
	for _, source := range fixture.Records {
		entries, err := partscatalog.New().Project(source)
		if err != nil || len(entries) != 1 {
			t.Fatal("parts projection", err)
		}
		entry := entries[0]
		row := struct {
			ID      string          `json:"id"`
			Keys    []string        `json:"keys"`
			Payload json.RawMessage `json:"payload"`
		}{entry.EntryID(), entry.Keys(), entry.Payload()}
		base.Write(jsonBytes(t, row))
	}
	put(t, filepath.Join(options.Bundle, "base.jsonl"), base.Bytes())
	compressed, err := base64.StdEncoding.DecodeString(partsTestIndexGzip)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatal(err)
	}
	index, err := io.ReadAll(io.LimitReader(reader, 21806))
	if err != nil || len(index) != 21805 || reader.Close() != nil {
		t.Fatal("parts index fixture", err)
	}
	put(t, filepath.Join(options.Bundle, "base.idx"), index)
	if _, err := readBundle(options.Bundle, recipe); err != nil {
		t.Fatal("hermetic fixture does not match measured bundle", err)
	}
	return options
}

func TestPartsPreparePublishedInventoryAndFixtures(t *testing.T) {
	options := partsFixture(t)
	recipe, _ := recipeForName(options.Recipe)
	before, err := readBundle(options.Bundle, recipe)
	if err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if Run(cliArgs(options), &out, &errOut) != 0 || errOut.Len() != 0 {
		t.Fatal("parts prepare", errOut.String())
	}
	want := Result{ConfigSHA256: recipe.configSHA256, Directories: 2, Files: 7,
		PhysicalListName: "parts-catalog-v1", Schema: ResultSchema, Status: "PREPARED"}
	if !bytes.Equal(out.Bytes(), jsonBytes(t, want)) {
		t.Fatal("parts result protocol")
	}
	config, err := localproof.Load(filepath.Join(options.Out, "kurn-searchd.json"))
	if err != nil || config.AdapterName != "parts-catalog" || config.PhysicalListName != "parts-catalog-v1" ||
		config.ConfigSHA256 != recipe.configSHA256 || config.TimeoutMillis != 30000 ||
		config.MaxInflight != 1 || config.WorstCaseChargeBytes != 1<<30 || len(config.Goldens) != 1 {
		t.Fatal("parts config", err)
	}
	golden := config.Goldens[0]
	if golden.Route != "parts-catalog" || golden.AuthHandle != "client-token-a" || golden.SentinelTenant != "tenant-a" ||
		golden.Query != "GX-200" || golden.Limit != 25 || golden.PartitionID != "" ||
		!golden.ExpectedComplete || !reflect.DeepEqual(golden.ExpectedIDs, []string{"PC-009"}) {
		t.Fatal("parts golden")
	}
	if _, err := localproof.Compose(config); err != nil {
		t.Fatal("parts composition", err)
	}
	expected := recipe.fixtureFiles()
	for name, raw := range before {
		expected[filepath.Join("data", recipe.physicalListName, name)] = raw
	}
	expected["kurn-searchd.json"] = jsonBytes(t, configuration(options.Out, options, recipe))
	if files, dirs, err := checkInventory(options.Out, expected, recipe); err != nil || files != 7 || dirs != 2 {
		t.Fatal("parts inventory", err)
	}
	after, err := readBundle(options.Bundle, recipe)
	if err != nil || !reflect.DeepEqual(before, after) || !absent(reservation(options)) || !absent(config.RuntimeStatusFile) {
		t.Fatal("input/residue")
	}
	assertRefused(t, options)
	afterRetry, err := readBundle(options.Bundle, recipe)
	if err != nil || !reflect.DeepEqual(before, afterRetry) {
		t.Fatal("retry changed input")
	}
}

func TestMultiSelectPrepareReusesPartsBundle(t *testing.T) {
	options := partsFixture(t)
	old, _ := recipeForName("parts-catalog")
	before, err := readBundle(options.Bundle, old)
	if err != nil {
		t.Fatal(err)
	}
	options.Recipe = "parts-catalog-select"
	selected, ok := recipeForName(options.Recipe)
	if !ok || selected.physicalListName != old.physicalListName || selected.configSHA256 != old.configSHA256 ||
		!reflect.DeepEqual(selected.bundleHashes, old.bundleHashes) || selected.entries != old.entries || selected.keys != old.keys {
		t.Fatal("bundle authority changed")
	}
	oldFiles, newFiles := old.fixtureFiles(), selected.fixtureFiles()
	for name, raw := range oldFiles {
		if name == "readiness.json" {
			raw = bytes.Replace(raw, []byte(`"route":"parts-catalog"`), []byte(`"route":"parts-catalog-select"`), 1)
		}
		if !bytes.Equal(raw, newFiles[name]) {
			t.Fatal("non-route fixture drift", name)
		}
	}
	var out, errOut bytes.Buffer
	if Run(cliArgs(options), &out, &errOut) != 0 || errOut.Len() != 0 {
		t.Fatal("select prepare", errOut.String())
	}
	config, err := localproof.Load(filepath.Join(options.Out, "kurn-searchd.json"))
	if err != nil || config.AdapterName != options.Recipe || config.PhysicalListName != old.physicalListName ||
		len(config.Goldens) != 1 || config.Goldens[0].Route != options.Recipe || config.Goldens[0].Query != "GX-200" ||
		!reflect.DeepEqual(config.Goldens[0].ExpectedIDs, []string{"PC-009"}) {
		t.Fatal("select composition inputs", err)
	}
	if _, err := localproof.Compose(config); err != nil {
		t.Fatal(err)
	}
	after, err := readBundle(options.Bundle, selected)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("bundle changed")
	}
	expected := selected.fixtureFiles()
	for name, raw := range before {
		expected[filepath.Join("data", selected.physicalListName, name)] = raw
	}
	expected["kurn-searchd.json"] = jsonBytes(t, configuration(options.Out, options, selected))
	if files, dirs, err := checkInventory(options.Out, expected, selected); err != nil || files != 7 || dirs != 2 {
		t.Fatal("select inventory", err)
	}
	assertRefused(t, options)
	after, err = readBundle(options.Bundle, selected)
	if err != nil || !reflect.DeepEqual(before, after) || !absent(reservation(options)) {
		t.Fatal("absent-only refusal mutation")
	}
}

func TestRecipeSelectionPreservesCatalogBytes(t *testing.T) {
	implicit, explicit := fixture(t), fixture(t)
	explicit.Recipe = "catalog-identifier"
	var first, second, errOut bytes.Buffer
	if Run(cliArgs(implicit), &first, &errOut) != 0 || Run(cliArgs(explicit), &second, &errOut) != 0 ||
		!bytes.Equal(first.Bytes(), second.Bytes()) || errOut.Len() != 0 {
		t.Fatal("catalog selection result differs")
	}
	err := filepath.WalkDir(implicit.Out, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		name, err := filepath.Rel(implicit.Out, path)
		if err != nil {
			return err
		}
		left, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		right, err := os.ReadFile(filepath.Join(explicit.Out, name))
		if err != nil {
			return err
		}
		if name == "kurn-searchd.json" {
			right = bytes.ReplaceAll(right, []byte(explicit.Out), []byte(implicit.Out))
		}
		if !bytes.Equal(left, right) {
			t.Fatalf("catalog bytes changed: %s", name)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestRecipeFlagAndCrossBundleRefusals(t *testing.T) {
	for _, name := range []string{"other", "Parts-Catalog", "parts-catalog ", "parts-catalog/v1"} {
		options := fixture(t)
		options.Recipe = name
		assertRefused(t, options)
		if !absent(options.Out) || !absent(reservation(options)) {
			t.Fatal("unknown recipe wrote")
		}
	}
	for _, builder := range []func(*testing.T) Options{fixture, partsFixture} {
		options := builder(t)
		if options.Recipe == "" {
			options.Recipe = "parts-catalog"
		} else {
			options.Recipe = "catalog-identifier"
		}
		assertRefused(t, options)
		if !absent(options.Out) || !absent(reservation(options)) {
			t.Fatal("cross-recipe wrote")
		}
	}
	options := fixture(t)
	good := cliArgs(options)
	bad := [][]string{
		append(append([]string(nil), good...), "-recipe", ""),
		append(append([]string(nil), good...), "-recipe=parts-catalog"),
		append(append([]string(nil), good...), "-recipe", "catalog-identifier", "-recipe", "parts-catalog"),
		append([]string{"prepare", "-recipe", "catalog-identifier"}, good[3:]...),
	}
	for _, args := range bad {
		var out, errOut bytes.Buffer
		if Run(args, &out, &errOut) != 1 || out.Len() != 0 || errOut.String() != "kurn-search-local: refused\n" ||
			!absent(options.Out) || !absent(reservation(options)) {
			t.Fatal("recipe argument refusal protocol")
		}
	}
}

func TestPartsBundleAndPublishRefusals(t *testing.T) {
	for _, name := range []string{"manifest.json", "base.jsonl", "base.idx", "config.json"} {
		for _, mutation := range []string{"corrupt", "missing", "symlink", "hardlink", "writable"} {
			t.Run(name+"/"+mutation, func(t *testing.T) {
				options := partsFixture(t)
				path := filepath.Join(options.Bundle, name)
				switch mutation {
				case "corrupt":
					raw, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					raw[len(raw)/2] ^= 1
					put(t, path, raw)
				case "missing":
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
				case "symlink":
					target := filepath.Join(filepath.Dir(options.Bundle), "target")
					if os.Rename(path, target) != nil || os.Symlink(target, path) != nil {
						t.Fatal("symlink")
					}
				case "hardlink":
					if os.Link(path, filepath.Join(filepath.Dir(options.Bundle), "linked")) != nil {
						t.Fatal("hardlink")
					}
				case "writable":
					if os.Chmod(path, 0o666) != nil {
						t.Fatal("mode")
					}
				}
				assertRefused(t, options)
				if !absent(options.Out) || !absent(reservation(options)) {
					t.Fatal("refusal left output")
				}
			})
		}
	}
	for _, phase := range []string{"after-reserve", "before-publish", "post-publish-validation"} {
		t.Run(phase, func(t *testing.T) {
			options := partsFixture(t)
			hook := func() error { return errors.New("injected") }
			hooks := &prepareHooks{afterReserve: hook}
			if phase == "before-publish" {
				hooks = &prepareHooks{beforePublish: hook}
			}
			if phase == "post-publish-validation" {
				hooks = &prepareHooks{beforePublish: func() error {
					return os.WriteFile(filepath.Join(reservation(options), "kurn-searchd.json"), []byte("{}\n"), 0o600)
				}}
			}
			if _, err := prepare(options, hooks); err == nil || !absent(options.Out) || !absent(reservation(options)) {
				t.Fatal("failed parts staging left output")
			}
		})
	}
}

func TestPartsManifestCensusIsExact(t *testing.T) {
	options := partsFixture(t)
	recipe, _ := recipeForName(options.Recipe)
	raw, err := os.ReadFile(filepath.Join(options.Bundle, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var base map[string]any
	if json.Unmarshal(raw, &base) != nil {
		t.Fatal("manifest")
	}
	for _, name := range []string{"v", "sha256", "version_id", "analyzer", "mode", "config_sha256", "entries", "keys"} {
		for _, mutation := range []string{"missing", "null", "type", "value"} {
			t.Run(name+"/"+mutation, func(t *testing.T) {
				value := map[string]any{}
				for k, v := range base {
					value[k] = v
				}
				switch mutation {
				case "missing":
					delete(value, name)
				case "null":
					value[name] = nil
				case "type":
					value[name] = []any{}
				case "value":
					value[name] = "wrong"
				}
				if validateManifest(jsonBytes(t, value), recipe) {
					t.Fatal("parts manifest mutation accepted")
				}
			})
		}
	}
}
