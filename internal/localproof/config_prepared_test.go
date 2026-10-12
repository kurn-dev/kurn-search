package localproof

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kurn-dev/kurn-search/adapters/partscatalogsuggest"
	"github.com/kurn-dev/kurn-search/backend/kurnhttp"
)

// Prepared-schema tests build a complete prepared runtime root in a test
// temporary directory. Literal values below are written independently of the
// unexported constants in config.go so that drift in either is caught. No
// kurnd or kurn-searchd process is started; the wiring test uses an
// in-process httptest backend on ephemeral loopback.

const (
	testPreparedList    = "parts-catalog-suggest-v1"
	testPreparedConfig  = "5c77cc033a580ee8f654fc6def1559713d527f2d3466006ea30735afc7eaecfc"
	testPreparedEntries = 24
	testPreparedA01     = `{"category":"sentinel","id":"A01","identifier":"ZS-901","manufacturer":"Northstar","partition_id":"north","status":"active","tenant_id":"alpha"}`
)

var (
	testPreparedBase   = strings.Repeat("7e", 32)
	testPreparedToken  = strings.Repeat("0123456789abcdef", 4)
	testPreparedHandle = strings.Repeat("fe", 32)
	preparedPathKeys   = []string{"runtime_status_file", "backend_credential_file", "fixture_authenticator_file", "readiness_file"}
)

func testPreparedDiagnostic(t *testing.T, engines ...string) []byte {
	t.Helper()
	if len(engines) == 0 {
		engines = []string{"v0.6.0-1-gf7423cd"}
	}
	descriptor, err := partscatalogsuggest.NewDescriptor(partscatalogsuggest.OperationalBinding{
		PhysicalListName: testPreparedList, ConfigSHA256: testPreparedConfig, TestedEngines: engines,
		TimeoutMillis: 30000, MaxInflight: 1, WorstCaseChargeBytes: 1073741824,
	})
	if err != nil {
		t.Fatal(err)
	}
	raw := descriptor.DiagnosticJSON()
	if len(raw) < 1 || len(raw) > 1<<20 {
		t.Fatalf("diagnostic size %d outside 1..1MiB", len(raw))
	}
	return raw
}

func testPreparedDocument(root, endpoint string) map[string]any {
	return map[string]any{
		"schema":                     "kurn-search-prepared-local/v1",
		"listen_address":             "127.0.0.1:0",
		"runtime_status_file":        filepath.Join(root, "state", "search-status.json"),
		"kurn_endpoint":              endpoint,
		"backend_credential_file":    filepath.Join(root, "backend-token"),
		"kurn_source_revision":       "f7423cd8cbb8165caf8aa750f56586a0ee621249",
		"rate":                       map[string]any{"unlimited": true, "rate_per_sec": 0},
		"fixture_authenticator_file": filepath.Join(root, "fixture-auth.json"),
		"readiness_file":             filepath.Join(root, "readiness.json"),
		"admission":                  "prepared_full",
		"expected_base_sha256":       testPreparedBase,
		"expected_entries":           testPreparedEntries,
		"adapter": map[string]any{
			"name": "parts-catalog-suggest", "physical_list_name": testPreparedList,
			"config_sha256": testPreparedConfig, "timeout_millis": 30000, "max_inflight": 1,
			"worst_case_charge_bytes": 1073741824,
		},
	}
}

func writeTestPreparedDocument(t *testing.T, root string, document map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "kurn-searchd.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// writeTestPreparedRoot creates root (0700), state/ (0700, empty), the three
// control files and the diagnostic (0400), and the config (0600 so that
// mutation helpers can rewrite it). It returns the config path.
func writeTestPreparedRoot(t *testing.T, root, endpoint string) string {
	t.Helper()
	return writeTestPreparedRootWith(t, root, testPreparedDocument(root, endpoint), testPreparedDiagnostic(t))
}

// writeTestPreparedRootWith is writeTestPreparedRoot with an explicit
// configuration document and diagnostic bytes.
func writeTestPreparedRootWith(t *testing.T, root string, document map[string]any, diagnostic []byte) string {
	t.Helper()
	for _, dir := range []string{root, filepath.Join(root, "state")} {
		if err := os.Mkdir(dir, 0o700); err != nil || os.Chmod(dir, 0o700) != nil {
			t.Fatal("mkdir", dir, err)
		}
	}
	write := func(name string, raw []byte) {
		if err := os.WriteFile(filepath.Join(root, name), raw, 0o400); err != nil || os.Chmod(filepath.Join(root, name), 0o400) != nil {
			t.Fatal("write", name, err)
		}
	}
	marshal := func(value any) []byte {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	write("backend-token", []byte(testPreparedToken))
	write("fixture-auth.json", marshal(map[string]any{
		"schema": AuthenticatorSchema, "credentials": []any{map[string]any{"handle": testPreparedHandle, "tenant": "alpha"}},
	}))
	write("readiness.json", marshal(map[string]any{
		"schema": ReadinessSchema,
		"goldens": []any{map[string]any{
			"name": "prepared-ready", "route": "parts-catalog-suggest", "auth_handle": testPreparedHandle,
			"sentinel_tenant": "alpha", "query": "ZS-901", "partition_id": "north", "limit": 1,
			"expected_ids": []string{"A01"}, "expected_complete": true,
		}},
	}))
	write("profile-diagnostic.json", diagnostic)
	return writeTestPreparedDocument(t, root, document)
}

func newTestPreparedRoot(t *testing.T) string {
	t.Helper()
	parent := t.TempDir()
	if os.Chmod(parent, 0o700) != nil {
		t.Fatal("chmod parent")
	}
	return writeTestPreparedRoot(t, filepath.Join(parent, "out"), "http://127.0.0.1:1")
}

func requireLoadRefused(t *testing.T, path string) {
	t.Helper()
	if config, err := Load(path); !errors.Is(err, errConfig) || !reflect.DeepEqual(config, Config{}) {
		t.Fatalf("Load() = %+v, %v; want refusal", config, err)
	}
}

func rewriteRaw(t *testing.T, path string, edit func([]byte) []byte) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	edited := edit(raw)
	if bytes.Equal(raw, edited) {
		t.Fatal("raw edit did not change the document")
	}
	if os.WriteFile(path, edited, 0o600) != nil {
		t.Fatal("rewrite")
	}
}

func TestPreparedLoadPositive(t *testing.T) {
	path := newTestPreparedRoot(t)
	root := filepath.Dir(path)
	config, err := Load(path)
	if err != nil {
		t.Fatalf("prepared Load: %v", err)
	}
	want := testPreparedDiagnostic(t)
	if config.Admission != kurnhttp.AdmissionPreparedFull || config.ExpectedBaseSHA256 != testPreparedBase ||
		config.ExpectedEntries != testPreparedEntries || !bytes.Equal(config.ProfileDiagnostic, want) ||
		config.Root != root || config.RuntimeStatusFile != filepath.Join(root, "state", "search-status.json") ||
		config.AdapterName != "parts-catalog-suggest" || config.PhysicalListName != testPreparedList ||
		config.ConfigSHA256 != testPreparedConfig || config.TimeoutMillis != 30000 || config.MaxInflight != 1 ||
		config.WorstCaseChargeBytes != 1073741824 || !config.Rate.Unlimited || config.Rate.RatePerSecond != 0 ||
		config.AttestedEngine != "v0.6.0-1-gf7423cd" || config.KurnSourceRevision != "f7423cd8cbb8165caf8aa750f56586a0ee621249" ||
		config.BackendCredential != testPreparedToken || len(config.Goldens) != 1 || config.Goldens[0].ExpectedIDs[0] != "A01" {
		t.Fatalf("resolved prepared config = %+v", config)
	}
	if PreparedConfigSchema != "kurn-search-prepared-local/v1" || PreparedAdmission != "prepared_full" ||
		PreparedDiagnosticFile != "profile-diagnostic.json" {
		t.Fatal("frozen prepared names changed")
	}

	// The diagnostic is a private copy: mutating it neither changes the file
	// nor a later Load, and two Loads do not share backing storage.
	config.ProfileDiagnostic[0] ^= 0xff
	again, err := Load(path)
	if err != nil || !bytes.Equal(again.ProfileDiagnostic, want) {
		t.Fatal("diagnostic aliasing across Loads", err)
	}
	onDisk, _ := os.ReadFile(filepath.Join(root, "profile-diagnostic.json"))
	if !bytes.Equal(onDisk, want) {
		t.Fatal("diagnostic file changed")
	}

	// Entry-count boundaries.
	for _, entries := range []int{1, 100000} {
		mutateConfigFile(t, path, func(value map[string]any) { value["expected_entries"] = entries })
		if config, err := Load(path); err != nil || config.ExpectedEntries != uint64(entries) {
			t.Fatalf("entries %d refused: %v", entries, err)
		}
	}
}

func TestPreparedComposePositiveWithoutServing(t *testing.T) {
	config, err := Load(newTestPreparedRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	// Compose builds the service only; it neither listens nor reaches the
	// deliberately unavailable endpoint (matching the existing tests).
	service, err := Compose(config)
	if err != nil || service == nil {
		t.Fatalf("prepared Compose: %v", err)
	}
	if _, err := os.Lstat(config.RuntimeStatusFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("Compose wrote a status file")
	}
}

// testPreparedBackend is an in-process stand-in for a prepared kurnd that
// answers the readiness golden (ZS-901 → A01) with the given evidence.
func testPreparedBackend(t *testing.T, header []string, served, basis string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/batch-query" || r.Header.Get("Authorization") != "Bearer "+testPreparedToken {
			t.Errorf("unexpected backend request %s auth=%t", r.URL.Path, r.Header.Get("Authorization") != "")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var request struct {
			Checks []struct {
				Query  string          `json:"q"`
				Lists  []string        `json:"lists"`
				Filter json.RawMessage `json:"filter"`
			} `json:"checks"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode: %v", err)
			return
		}
		results := make([]any, len(request.Checks))
		for index, check := range request.Checks {
			candidates := []any{}
			if check.Query == "ZS-901" {
				candidates = append(candidates, map[string]any{
					"list": testPreparedList, "entry_id": "A01", "score": 100, "key": "ZS-901",
					"payload": json.RawMessage(testPreparedA01),
				})
			}
			result := map[string]any{
				"candidates": candidates, "versions": map[string]string{testPreparedList: served},
				"filter":       check.Filter,
				"filter_stats": map[string]any{testPreparedList: map[string]int{"evaluated": 1, "rejected": 0}},
				"took_us":      1,
			}
			if basis != "" {
				result["version_bases"] = map[string]string{testPreparedList: basis}
			}
			results[index] = result
		}
		for _, value := range header {
			w.Header().Add("Kurn-Artifact-Verification", value)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"results": results, "took_us": 1})
	}))
	t.Cleanup(server.Close)
	return server
}

func TestPreparedComposeWiresFullAdmissionIntoReadiness(t *testing.T) {
	served := testPreparedBase + "@24+j0+c" + testPreparedConfig
	for name, test := range map[string]struct {
		header []string
		served string
		basis  string
		ready  bool
	}{
		"full-verified":  {[]string{"full"}, served, "verified-base-id", true},
		"trusted":        {[]string{"trusted"}, served, "verified-base-id", false},
		"no-header":      {nil, served, "verified-base-id", false},
		"declared-basis": {[]string{"full"}, served, "declared-base-id", false},
		"no-bases":       {[]string{"full"}, served, "", false},
		"other-base":     {[]string{"full"}, strings.Repeat("ab", 32) + "@24+j0+c" + testPreparedConfig, "verified-base-id", false},
		"other-entries":  {[]string{"full"}, testPreparedBase + "@25+j0+c" + testPreparedConfig, "verified-base-id", false},
	} {
		t.Run(name, func(t *testing.T) {
			backend := testPreparedBackend(t, test.header, test.served, test.basis)
			parent := t.TempDir()
			_ = os.Chmod(parent, 0o700)
			path := writeTestPreparedRoot(t, filepath.Join(parent, "out"), backend.URL)
			config, err := Load(path)
			if err != nil {
				t.Fatalf("Load with loopback endpoint %q: %v", backend.URL, err)
			}
			service, err := Compose(config)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			err = service.Ready(ctx)
			if test.ready && err != nil {
				t.Fatalf("full evidence readiness failed: %v", err)
			}
			if !test.ready && err == nil {
				t.Fatal("readiness passed without full prepared evidence")
			}
		})
	}

	// Contrast: the identical downgraded backend is ready for a legacy
	// composition, proving the refusal comes from the prepared policy.
	backend := testPreparedBackend(t, []string{"trusted"}, served, "declared-base-id")
	parent := t.TempDir()
	_ = os.Chmod(parent, 0o700)
	config, err := Load(writeTestPreparedRoot(t, filepath.Join(parent, "out"), backend.URL))
	if err != nil {
		t.Fatal(err)
	}
	config.Admission, config.ExpectedBaseSHA256, config.ExpectedEntries, config.ProfileDiagnostic = kurnhttp.AdmissionLegacy, "", 0, nil
	service, err := Compose(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Ready(context.Background()); err != nil {
		t.Fatalf("legacy composition against the same backend: %v", err)
	}
}

func TestPreparedRefusesMissingAndNullMembers(t *testing.T) {
	top := []string{"schema", "listen_address", "runtime_status_file", "kurn_endpoint", "backend_credential_file",
		"kurn_source_revision", "rate", "adapter", "fixture_authenticator_file", "readiness_file",
		"admission", "expected_base_sha256", "expected_entries"}
	adapter := []string{"name", "physical_list_name", "config_sha256", "timeout_millis", "max_inflight", "worst_case_charge_bytes"}
	rate := []string{"unlimited", "rate_per_sec"}
	if got := len(testPreparedDocument("/r", "http://127.0.0.1:1")); got != len(top) {
		t.Fatalf("fixture has %d members", got)
	}
	for _, name := range top {
		for _, null := range []bool{false, true} {
			t.Run(map[bool]string{false: "missing/", true: "null/"}[null]+name, func(t *testing.T) {
				path := newTestPreparedRoot(t)
				mutateConfigFile(t, path, func(value map[string]any) {
					if null {
						value[name] = nil
					} else {
						delete(value, name)
					}
				})
				requireLoadRefused(t, path)
			})
		}
	}
	for object, names := range map[string][]string{"adapter": adapter, "rate": rate} {
		for _, name := range names {
			for _, null := range []bool{false, true} {
				t.Run(map[bool]string{false: "missing/", true: "null/"}[null]+object+"."+name, func(t *testing.T) {
					path := newTestPreparedRoot(t)
					mutateConfigFile(t, path, func(value map[string]any) {
						nested := value[object].(map[string]any)
						if null {
							nested[name] = nil
						} else {
							delete(nested, name)
						}
					})
					requireLoadRefused(t, path)
				})
			}
		}
	}
}

func TestPreparedRefusesUnknownDuplicateAndMalformedMembers(t *testing.T) {
	cases := map[string]func(t *testing.T, path string){
		"unknown-top": func(t *testing.T, path string) {
			mutateConfigFile(t, path, func(v map[string]any) { v["extra"] = true })
		},
		"unknown-adapter": func(t *testing.T, path string) {
			mutateConfigFile(t, path, func(v map[string]any) { v["adapter"].(map[string]any)["extra"] = 1 })
		},
		"unknown-rate": func(t *testing.T, path string) {
			mutateConfigFile(t, path, func(v map[string]any) { v["rate"].(map[string]any)["burst"] = 1 })
		},
		"diagnostic-member": func(t *testing.T, path string) {
			mutateConfigFile(t, path, func(v map[string]any) {
				v["profile_diagnostic_file"] = filepath.Join(filepath.Dir(path), "profile-diagnostic.json")
			})
		},
		"case-variant": func(t *testing.T, path string) {
			mutateConfigFile(t, path, func(v map[string]any) { v["Admission"] = v["admission"]; delete(v, "admission") })
		},
		"case-variant-extra": func(t *testing.T, path string) {
			mutateConfigFile(t, path, func(v map[string]any) { v["ADMISSION"] = "prepared_full" })
		},
		"duplicate-admission": func(t *testing.T, path string) {
			rewriteRaw(t, path, func(raw []byte) []byte {
				return bytes.Replace(raw, []byte(`"admission":"prepared_full"`), []byte(`"admission":"prepared_full","admission":"prepared_full"`), 1)
			})
		},
		"duplicate-entries-differing": func(t *testing.T, path string) {
			rewriteRaw(t, path, func(raw []byte) []byte {
				return bytes.Replace(raw, []byte(`"expected_entries":24`), []byte(`"expected_entries":24,"expected_entries":25`), 1)
			})
		},
		"duplicate-adapter-name": func(t *testing.T, path string) {
			rewriteRaw(t, path, func(raw []byte) []byte {
				return bytes.Replace(raw, []byte(`"name":"parts-catalog-suggest"`), []byte(`"name":"parts-catalog-suggest","name":"parts-catalog-suggest"`), 1)
			})
		},
		"duplicate-rate-member": func(t *testing.T, path string) {
			rewriteRaw(t, path, func(raw []byte) []byte {
				return bytes.Replace(raw, []byte(`"unlimited":true`), []byte(`"unlimited":true,"unlimited":true`), 1)
			})
		},
		"duplicate-schema-legacy-last": func(t *testing.T, path string) {
			rewriteRaw(t, path, func(raw []byte) []byte {
				return bytes.Replace(raw, []byte(`"schema":"kurn-search-prepared-local/v1"`),
					[]byte(`"schema":"kurn-search-prepared-local/v1","schema":"`+ConfigSchema+`"`), 1)
			})
		},
		"duplicate-schema-prepared-last": func(t *testing.T, path string) {
			rewriteRaw(t, path, func(raw []byte) []byte {
				return bytes.Replace(raw, []byte(`"schema":"kurn-search-prepared-local/v1"`),
					[]byte(`"schema":"`+ConfigSchema+`","schema":"kurn-search-prepared-local/v1"`), 1)
			})
		},
		"trailing-document": func(t *testing.T, path string) {
			rewriteRaw(t, path, func(raw []byte) []byte { return append(raw, []byte(`{}`)...) })
		},
		"lone-surrogate": func(t *testing.T, path string) {
			rewriteRaw(t, path, func(raw []byte) []byte {
				return bytes.Replace(raw, []byte(`"listen_address":"127.0.0.1:0"`), []byte(`"listen_address":"\uD800"`), 1)
			})
		},
		"rate-not-object": func(t *testing.T, path string) {
			mutateConfigFile(t, path, func(v map[string]any) { v["rate"] = true })
		},
		"adapter-not-object": func(t *testing.T, path string) {
			mutateConfigFile(t, path, func(v map[string]any) { v["adapter"] = []any{} })
		},
	}
	values := map[string]any{
		"admission=trusted":    "prepared_trusted",
		"admission=upper":      "PREPARED_FULL",
		"admission=empty":      "",
		"admission=number":     1,
		"base=upper":           strings.ToUpper(testPreparedBase),
		"base=short":           testPreparedBase[:63],
		"base=long":            testPreparedBase + "0",
		"base=nonhex":          strings.Repeat("zz", 32),
		"base=spaced":          " " + testPreparedBase[1:],
		"base=empty":           "",
		"base=number":          7,
		"entries=zero":         0,
		"entries=over":         100001,
		"entries=negative":     -1,
		"entries=fraction":     1.5,
		"entries=string":       "24",
		"entries=bool":         true,
		"schema=legacy-schema": ConfigSchema,
	}
	for name, value := range values {
		member := map[string]string{"admission": "admission", "base": "expected_base_sha256", "entries": "expected_entries", "schema": "schema"}[strings.SplitN(name, "=", 2)[0]]
		cases["value/"+name] = func(t *testing.T, path string) {
			mutateConfigFile(t, path, func(v map[string]any) { v[member] = value })
		}
	}
	cases["entries=exponent"] = func(t *testing.T, path string) {
		rewriteRaw(t, path, func(raw []byte) []byte {
			return bytes.Replace(raw, []byte(`"expected_entries":24`), []byte(`"expected_entries":2.4e1`), 1)
		})
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			path := newTestPreparedRoot(t)
			mutate(t, path)
			requireLoadRefused(t, path)
		})
	}
}

func TestLegacySchemaRejectsPreparedMembers(t *testing.T) {
	for name, members := range map[string]map[string]any{
		"admission":            {"admission": "prepared_full"},
		"expected_base_sha256": {"expected_base_sha256": testPreparedBase},
		"expected_entries":     {"expected_entries": 24},
		"all-three":            {"admission": "prepared_full", "expected_base_sha256": testPreparedBase, "expected_entries": 24},
	} {
		t.Run(name, func(t *testing.T) {
			path := writeFixture(t)
			mutateConfigFile(t, path, func(value map[string]any) {
				for key, member := range members {
					value[key] = member
				}
			})
			requireLoadRefused(t, path)
		})
	}
	// A complete prepared document relabelled with the legacy schema is a
	// legacy document with unknown members: refused.
	path := newTestPreparedRoot(t)
	mutateConfigFile(t, path, func(value map[string]any) { value["schema"] = ConfigSchema })
	requireLoadRefused(t, path)
	// The same document stripped of the three members loads as legacy and
	// carries no prepared policy, even though its root has state/ and a
	// diagnostic file.
	mutateConfigFile(t, path, func(value map[string]any) {
		delete(value, "admission")
		delete(value, "expected_base_sha256")
		delete(value, "expected_entries")
	})
	legacy, err := Load(path)
	if err != nil || legacy.Admission != kurnhttp.AdmissionLegacy || legacy.ExpectedBaseSHA256 != "" ||
		legacy.ExpectedEntries != 0 || legacy.ProfileDiagnostic != nil {
		t.Fatalf("legacy load of a stripped document = %+v, %v", legacy, err)
	}
}

func TestPreparedFixedBindingMismatches(t *testing.T) {
	for name, mutate := range map[string]func(map[string]any){
		"adapter-parts-catalog": func(v map[string]any) {
			v["adapter"].(map[string]any)["name"] = "parts-catalog"
			v["adapter"].(map[string]any)["physical_list_name"] = "parts-catalog-v1"
		},
		"adapter-catalog-identifier": func(v map[string]any) { v["adapter"].(map[string]any)["name"] = "catalog-identifier" },
		"adapter-select":             func(v map[string]any) { v["adapter"].(map[string]any)["name"] = "parts-catalog-select" },
		"physical-list": func(v map[string]any) {
			v["adapter"].(map[string]any)["physical_list_name"] = "parts-catalog-suggest-v2"
		},
		"config-sha":   func(v map[string]any) { v["adapter"].(map[string]any)["config_sha256"] = strings.Repeat("a", 64) },
		"timeout-low":  func(v map[string]any) { v["adapter"].(map[string]any)["timeout_millis"] = 29999 },
		"timeout-high": func(v map[string]any) { v["adapter"].(map[string]any)["timeout_millis"] = 30001 },
		"max-inflight": func(v map[string]any) { v["adapter"].(map[string]any)["max_inflight"] = 65 },
		"charge-low":   func(v map[string]any) { v["adapter"].(map[string]any)["worst_case_charge_bytes"] = 1073741823 },
		"charge-high":  func(v map[string]any) { v["adapter"].(map[string]any)["worst_case_charge_bytes"] = 1073741825 },
		"rate-limited": func(v map[string]any) {
			v["rate"] = map[string]any{"unlimited": false, "rate_per_sec": 1000}
		},
		"kurn-revision": func(v map[string]any) { v["kurn_source_revision"] = strings.Repeat("0", 40) },
	} {
		t.Run(name, func(t *testing.T) {
			path := newTestPreparedRoot(t)
			mutateConfigFile(t, path, mutate)
			requireLoadRefused(t, path)
		})
	}
}

func TestPreparedStateDirectoryRules(t *testing.T) {
	for name, mutate := range map[string]func(t *testing.T, root, path string){
		"missing": func(t *testing.T, root, _ string) { _ = os.Remove(filepath.Join(root, "state")) },
		"symlink": func(t *testing.T, root, _ string) {
			target := filepath.Join(filepath.Dir(root), "real-state")
			if os.Mkdir(target, 0o700) != nil || os.Chmod(target, 0o700) != nil ||
				os.Remove(filepath.Join(root, "state")) != nil || os.Symlink(target, filepath.Join(root, "state")) != nil {
				t.Fatal("symlink state")
			}
		},
		"file": func(t *testing.T, root, _ string) {
			replaceState(t, root, func(p string) error { return os.WriteFile(p, nil, 0o600) })
		},
		"mode-0750": func(t *testing.T, root, _ string) { _ = os.Chmod(filepath.Join(root, "state"), 0o750) },
		"mode-0755": func(t *testing.T, root, _ string) { _ = os.Chmod(filepath.Join(root, "state"), 0o755) },
		"mode-0500": func(t *testing.T, root, _ string) { _ = os.Chmod(filepath.Join(root, "state"), 0o500) },
		"mode-0300": func(t *testing.T, root, _ string) { _ = os.Chmod(filepath.Join(root, "state"), 0o300) },
		"sticky": func(t *testing.T, root, _ string) {
			_ = os.Chmod(filepath.Join(root, "state"), os.ModeSticky|0o700)
		},
		"status-present": func(t *testing.T, root, _ string) {
			if os.WriteFile(filepath.Join(root, "state", "search-status.json"), []byte(`{}`), 0o600) != nil {
				t.Fatal("status")
			}
		},
		"status-dangling-symlink": func(t *testing.T, root, _ string) {
			if os.Symlink(filepath.Join(root, "nowhere"), filepath.Join(root, "state", "search-status.json")) != nil {
				t.Fatal("status symlink")
			}
		},
		"status-path-root": func(t *testing.T, root, path string) {
			mutateConfigFile(t, path, func(v map[string]any) { v["runtime_status_file"] = filepath.Join(root, "search-status.json") })
		},
		"status-path-other-name": func(t *testing.T, root, path string) {
			mutateConfigFile(t, path, func(v map[string]any) { v["runtime_status_file"] = filepath.Join(root, "state", "status.json") })
		},
		"status-path-unclean": func(t *testing.T, root, path string) {
			mutateConfigFile(t, path, func(v map[string]any) {
				v["runtime_status_file"] = root + "/state/../state/search-status.json"
			})
		},
		"status-path-nested": func(t *testing.T, root, path string) {
			if os.Mkdir(filepath.Join(root, "state", "sub"), 0o700) != nil {
				t.Fatal("mkdir")
			}
			mutateConfigFile(t, path, func(v map[string]any) {
				v["runtime_status_file"] = filepath.Join(root, "state", "sub", "search-status.json")
			})
		},
	} {
		t.Run(name, func(t *testing.T) {
			path := newTestPreparedRoot(t)
			mutate(t, filepath.Dir(path), path)
			requireLoadRefused(t, path)
		})
	}
}

func replaceState(t *testing.T, root string, create func(string) error) {
	t.Helper()
	state := filepath.Join(root, "state")
	if os.Remove(state) != nil || create(state) != nil {
		t.Fatal("replace state")
	}
}

type testFileInfo struct {
	os.FileInfo
	sys any
}

func (info testFileInfo) Sys() any { return info.sys }

func TestPreparedStateOwnershipPredicate(t *testing.T) {
	dir := t.TempDir()
	info, err := os.Lstat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !ownedByCurrentUser(info) {
		t.Fatal("test-owned directory not recognized as owned by the current user")
	}
	type uidStruct struct{ Uid uint32 }
	type gidOnly struct{ Gid uint32 }
	type signedUID struct{ Uid int32 }
	current := uint32(os.Getuid())
	for name, test := range map[string]struct {
		sys  any
		want bool
	}{
		"nil":          {nil, false},
		"non-pointer":  {uidStruct{Uid: current}, false},
		"nil-pointer":  {(*uidStruct)(nil), false},
		"no-uid-field": {&gidOnly{Gid: current}, false},
		"signed-uid":   {&signedUID{Uid: int32(current)}, false},
		"other-uid":    {&uidStruct{Uid: current + 1}, false},
		"same-uid":     {&uidStruct{Uid: current}, true},
	} {
		t.Run(name, func(t *testing.T) {
			if got := ownedByCurrentUser(testFileInfo{FileInfo: info, sys: test.sys}); got != test.want {
				t.Fatalf("ownedByCurrentUser = %t, want %t", got, test.want)
			}
		})
	}
	// A state directory owned by another uid is refused through Load's
	// predicate; exercised here directly because tests cannot chown.
	if validatePreparedState(dir, filepath.Join(dir, "state", "search-status.json")) == nil {
		t.Fatal("missing state accepted")
	}
}

func TestPreparedDiagnosticFileRules(t *testing.T) {
	diagnostic := func(root string) string { return filepath.Join(root, "profile-diagnostic.json") }
	replace := func(t *testing.T, root string, raw []byte, mode os.FileMode) {
		t.Helper()
		if os.Remove(diagnostic(root)) != nil || os.WriteFile(diagnostic(root), raw, mode) != nil || os.Chmod(diagnostic(root), mode) != nil {
			t.Fatal("replace diagnostic")
		}
	}
	for name, mutate := range map[string]func(t *testing.T, root string){
		"missing": func(t *testing.T, root string) { _ = os.Remove(diagnostic(root)) },
		"symlink": func(t *testing.T, root string) {
			target := filepath.Join(filepath.Dir(root), "diag-target.json")
			if os.WriteFile(target, testPreparedDiagnostic(t), 0o400) != nil || os.Remove(diagnostic(root)) != nil ||
				os.Symlink(target, diagnostic(root)) != nil {
				t.Fatal("symlink diagnostic")
			}
		},
		"directory": func(t *testing.T, root string) {
			if os.Remove(diagnostic(root)) != nil || os.Mkdir(diagnostic(root), 0o700) != nil {
				t.Fatal("dir diagnostic")
			}
		},
		"mode-0440": func(t *testing.T, root string) { _ = os.Chmod(diagnostic(root), 0o440) },
		"mode-0404": func(t *testing.T, root string) { _ = os.Chmod(diagnostic(root), 0o404) },
		"mode-0644": func(t *testing.T, root string) { _ = os.Chmod(diagnostic(root), 0o644) },
		"mode-0410": func(t *testing.T, root string) { _ = os.Chmod(diagnostic(root), 0o410) },
		"empty":     func(t *testing.T, root string) { replace(t, root, nil, 0o400) },
		"oversize":  func(t *testing.T, root string) { replace(t, root, bytes.Repeat([]byte{' '}, 1<<20+1), 0o400) },
	} {
		t.Run(name, func(t *testing.T) {
			path := newTestPreparedRoot(t)
			mutate(t, filepath.Dir(path))
			requireLoadRefused(t, path)
		})
	}

	// Load admits any bounded private bytes; Compose then requires byte
	// equality with the rebuilt descriptor's DiagnosticJSON.
	good := testPreparedDiagnostic(t)
	for name, raw := range map[string][]byte{
		"one-byte-changed": func() []byte { b := bytes.Clone(good); b[len(b)/2] ^= 0x01; return b }(),
		"trailing-lf":      append(bytes.Clone(good), '\n'),
		"truncated":        good[:len(good)-1],
		"compact":          func() []byte { var out bytes.Buffer; _ = json.Compact(&out, good); return out.Bytes() }(),
		"other-engine":     testPreparedDiagnostic(t, "v0.6.0"),
		"extra-engine":     testPreparedDiagnostic(t, "v0.6.0-1-gf7423cd", "v0.6.1"),
		"one-byte":         {'{'},
		"exactly-1MiB":     bytes.Repeat([]byte{' '}, 1<<20),
	} {
		t.Run("compose/"+name, func(t *testing.T) {
			if bytes.Equal(raw, good) {
				t.Fatal("mutation is not a change")
			}
			path := newTestPreparedRoot(t)
			replace(t, filepath.Dir(path), raw, 0o400)
			config, err := Load(path)
			if err != nil || !bytes.Equal(config.ProfileDiagnostic, raw) {
				t.Fatalf("bounded diagnostic refused at Load: %v", err)
			}
			if service, err := Compose(config); !errors.Is(err, errConfig) || service != nil {
				t.Fatalf("Compose accepted a drifted diagnostic: %v", err)
			}
		})
	}
	// 0600 is private too and loads (mode & 0o077 == 0).
	path := newTestPreparedRoot(t)
	_ = os.Chmod(diagnostic(filepath.Dir(path)), 0o600)
	if _, err := Load(path); err != nil {
		t.Fatalf("0600 diagnostic refused: %v", err)
	}
}

func TestPreparedComposeRefusesInMemoryDrift(t *testing.T) {
	base, err := Load(newTestPreparedRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Compose(base); err != nil {
		t.Fatalf("unmodified prepared Compose: %v", err)
	}
	for name, mutate := range map[string]func(*Config){
		"diagnostic-byte":  func(c *Config) { c.ProfileDiagnostic = bytes.Clone(c.ProfileDiagnostic); c.ProfileDiagnostic[0] ^= 1 },
		"diagnostic-nil":   func(c *Config) { c.ProfileDiagnostic = nil },
		"physical-list":    func(c *Config) { c.PhysicalListName = "other-v1" },
		"config-sha":       func(c *Config) { c.ConfigSHA256 = strings.Repeat("a", 64) },
		"timeout":          func(c *Config) { c.TimeoutMillis = 10000 },
		"inflight":         func(c *Config) { c.MaxInflight = 65 },
		"charge":           func(c *Config) { c.WorstCaseChargeBytes = 1 },
		"rate-limited":     func(c *Config) { c.Rate.Unlimited, c.Rate.RatePerSecond = false, 1000 },
		"engine":           func(c *Config) { c.AttestedEngine = "v0.6.0" },
		"revision":         func(c *Config) { c.KurnSourceRevision = strings.Repeat("0", 40) },
		"adapter":          func(c *Config) { c.AdapterName = "parts-catalog" },
		"base-upper":       func(c *Config) { c.ExpectedBaseSHA256 = strings.ToUpper(c.ExpectedBaseSHA256) },
		"entries-zero":     func(c *Config) { c.ExpectedEntries = 0 },
		"entries-over":     func(c *Config) { c.ExpectedEntries = 100001 },
		"status-elsewhere": func(c *Config) { c.RuntimeStatusFile = filepath.Join(c.Root, "status.json") },
		"unknown-policy":   func(c *Config) { c.Admission = 7 },
		"policy-mismatch":  func(c *Config) { c.Policy = partscatalogsuggest.PolicyOSA1G60 },
		"legacy-with-diag": func(c *Config) {
			c.Admission = kurnhttp.AdmissionLegacy
			c.ExpectedBaseSHA256, c.ExpectedEntries = "", 0
		},
		"legacy-with-base": func(c *Config) {
			c.Admission = kurnhttp.AdmissionLegacy
			c.ProfileDiagnostic, c.ExpectedEntries = nil, 0
		},
		"legacy-with-count": func(c *Config) {
			c.Admission = kurnhttp.AdmissionLegacy
			c.ProfileDiagnostic, c.ExpectedBaseSHA256 = nil, ""
		},
	} {
		t.Run(name, func(t *testing.T) {
			config := base
			config.ProfileDiagnostic = bytes.Clone(base.ProfileDiagnostic)
			mutate(&config)
			if service, err := Compose(config); !errors.Is(err, errConfig) || service != nil {
				t.Fatalf("Compose accepted drift: %v", err)
			}
		})
	}
}

func TestPreparedStagedAndFinalPathEquivalence(t *testing.T) {
	parent := t.TempDir()
	if os.Chmod(parent, 0o700) != nil {
		t.Fatal("chmod parent")
	}
	staged := filepath.Join(parent, ".out.prepared-setup")
	final := filepath.Join(parent, "out")
	stagedPath := writeTestPreparedRoot(t, staged, "http://127.0.0.1:1")
	stagedRaw, _ := os.ReadFile(stagedPath)
	stagedConfig, err := Load(stagedPath)
	if err != nil {
		t.Fatalf("staged Load: %v", err)
	}

	// Rewrite ONLY the four path members for the final root, then publish by
	// renaming the whole root.
	finalPath := writeTestPreparedDocument(t, staged, testPreparedDocument(final, "http://127.0.0.1:1"))
	finalRaw, _ := os.ReadFile(finalPath)
	var stagedDoc, finalDoc map[string]json.RawMessage
	if json.Unmarshal(stagedRaw, &stagedDoc) != nil || json.Unmarshal(finalRaw, &finalDoc) != nil || len(stagedDoc) != len(finalDoc) {
		t.Fatal("documents")
	}
	var differing []string
	for key, value := range stagedDoc {
		if !bytes.Equal(value, finalDoc[key]) {
			differing = append(differing, key)
		}
	}
	if len(differing) != len(preparedPathKeys) {
		t.Fatalf("staged/final differ in %v", differing)
	}
	for _, key := range preparedPathKeys {
		if bytes.Equal(stagedDoc[key], finalDoc[key]) {
			t.Fatalf("path member %s did not change", key)
		}
	}
	if os.Rename(staged, final) != nil {
		t.Fatal("publish rename")
	}
	finalConfig, err := Load(filepath.Join(final, "kurn-searchd.json"))
	if err != nil {
		t.Fatalf("final Load: %v", err)
	}
	if service, err := Compose(finalConfig); err != nil || service == nil {
		t.Fatalf("final Compose: %v", err)
	}

	// The resolved configs are identical except for the root and the four
	// root-relative paths.
	if stagedConfig.Root != staged || finalConfig.Root != final ||
		stagedConfig.RuntimeStatusFile != filepath.Join(staged, "state", "search-status.json") ||
		finalConfig.RuntimeStatusFile != filepath.Join(final, "state", "search-status.json") ||
		finalConfig.BackendCredentialFile != filepath.Join(final, "backend-token") {
		t.Fatal("resolved paths")
	}
	normalize := func(c Config) Config {
		c.Root, c.RuntimeStatusFile, c.BackendCredentialFile = "", "", ""
		c.Authenticator = nil
		return c
	}
	if !reflect.DeepEqual(normalize(stagedConfig), normalize(finalConfig)) {
		t.Fatalf("staged and final resolve differently:\n%+v\n%+v", normalize(stagedConfig), normalize(finalConfig))
	}

	// The staged document no longer resolves once its root is gone.
	requireLoadRefused(t, stagedPath)
}

func TestLegacyPositivesUnchangedAlongsidePrepared(t *testing.T) {
	config, err := Load(writeFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	if config.Admission != kurnhttp.AdmissionLegacy || config.ExpectedBaseSHA256 != "" || config.ExpectedEntries != 0 || config.ProfileDiagnostic != nil {
		t.Fatalf("legacy config gained prepared policy: %+v", config)
	}
	if service, err := Compose(config); err != nil || service == nil {
		t.Fatalf("legacy Compose: %v", err)
	}

	// The legacy schema still serves parts-catalog-suggest under the legacy
	// policy with any operational binding it accepted before.
	path := writeFixture(t)
	mutateConfigFile(t, path, func(value map[string]any) {
		value["adapter"].(map[string]any)["name"] = "parts-catalog-suggest"
		value["adapter"].(map[string]any)["physical_list_name"] = "parts-catalog-suggest-v1"
		value["adapter"].(map[string]any)["timeout_millis"] = 10000
		value["rate"] = map[string]any{"unlimited": false, "rate_per_sec": 32}
	})
	mutateConfigFile(t, filepath.Join(filepath.Dir(path), "readiness.json"), func(value map[string]any) {
		value["goldens"].([]any)[0].(map[string]any)["route"] = "parts-catalog-suggest"
	})
	legacySuggest, err := Load(path)
	if err != nil || legacySuggest.Admission != kurnhttp.AdmissionLegacy || legacySuggest.ProfileDiagnostic != nil {
		t.Fatalf("legacy suggest: %+v, %v", legacySuggest, err)
	}
	if service, err := Compose(legacySuggest); err != nil || service == nil {
		t.Fatalf("legacy suggest Compose: %v", err)
	}
	// Legacy still ignores a missing rate_per_sec member when unlimited, as
	// before; only the prepared schema requires every member.
	path = writeFixture(t)
	mutateConfigFile(t, path, func(value map[string]any) { value["rate"] = map[string]any{"unlimited": true} })
	if _, err := Load(path); err != nil {
		t.Fatalf("legacy rate without rate_per_sec: %v", err)
	}
}

// An in-range admission limit loads, but Compose binds it through the profile
// diagnostic: a config declaring a different limit than its version's
// diagnostic is refused.
// The optional adapter.max_queue member bounds the admission queue: absent
// selects the service default, 1..4096 is carried into the composed
// service, and 0, over-range, a string or null are refused.
// Engine authority: a bound configuration names its revision and is served
// with that revision's label. The test fixtures bind the previously admitted
// pair (an existing version), which must keep loading under its own label;
// the current pair loads under the current label; an unknown revision is
// refused; a mixed pair (old revision, diagnostic built for the current
// label) is refused by the diagnostic binding.
func TestPreparedEngineAuthorityPairs(t *testing.T) {
	parent := t.TempDir()
	_ = os.Chmod(parent, 0o700)
	if EngineAuthorities[1] != (EngineAuthority{Revision: "06c97f9fe695e29ce5adfc05f502edd68f5790e2", Label: "v0.7.0"}) {
		t.Fatal("previous prepared release authority was not preserved")
	}
	previous := EngineAuthorities[len(EngineAuthorities)-1]
	if previous.Revision != "f7423cd8cbb8165caf8aa750f56586a0ee621249" || previous.Label != "v0.6.0-1-gf7423cd" ||
		EngineAuthorities[0].Revision != KurnSourceRevision || EngineAuthorities[0].Label != KurnCompatibilityLabel || KurnSourceRevision == previous.Revision {
		t.Fatalf("engine authorities: %+v", EngineAuthorities)
	}
	// Existing version (fixture): previous pair, previous label.
	existing, err := Load(writeTestPreparedRoot(t, filepath.Join(parent, "existing"), "http://127.0.0.1:1"))
	if err != nil || existing.KurnSourceRevision != previous.Revision || existing.AttestedEngine != previous.Label {
		t.Fatalf("existing version: %v %q %q", err, existing.KurnSourceRevision, existing.AttestedEngine)
	}
	if _, err := Compose(existing); err != nil {
		t.Fatalf("existing version compose: %v", err)
	}
	for _, authority := range EngineAuthorities[1:] {
		root := filepath.Join(parent, "previous-"+authority.Revision)
		document := testPreparedDocument(root, "http://127.0.0.1:1")
		document["kurn_source_revision"] = authority.Revision
		path := writeTestPreparedRootWith(t, root, document, testPreparedDiagnostic(t, authority.Label))
		bound, err := Load(path)
		if err != nil || bound.AttestedEngine != authority.Label {
			t.Fatalf("previous authority %s: %v", authority.Revision, err)
		}
		if _, err := Compose(bound); err != nil {
			t.Fatalf("previous authority compose %s: %v", authority.Revision, err)
		}
	}
	// Current pair: the same root with the current revision and a diagnostic
	// rebuilt for the current label.
	currentRoot := filepath.Join(parent, "current")
	document := testPreparedDocument(currentRoot, "http://127.0.0.1:1")
	document["kurn_source_revision"] = KurnSourceRevision
	currentPath := writeTestPreparedRootWith(t, currentRoot, document, testPreparedDiagnostic(t, KurnCompatibilityLabel))
	current, err := Load(currentPath)
	if err != nil || current.AttestedEngine != KurnCompatibilityLabel {
		t.Fatalf("current pair: %v %q", err, current.AttestedEngine)
	}
	if _, err := Compose(current); err != nil {
		t.Fatalf("current pair compose: %v", err)
	}
	// Unknown revision.
	unknownRoot := filepath.Join(parent, "unknown")
	document = testPreparedDocument(unknownRoot, "http://127.0.0.1:1")
	document["kurn_source_revision"] = strings.Repeat("0", 40)
	requireLoadRefused(t, writeTestPreparedRootWith(t, unknownRoot, document, testPreparedDiagnostic(t, KurnCompatibilityLabel)))
	// Mixed pair: previous revision with a diagnostic for the current label.
	mixedRoot := filepath.Join(parent, "mixed")
	document = testPreparedDocument(mixedRoot, "http://127.0.0.1:1")
	mixed, err := Load(writeTestPreparedRootWith(t, mixedRoot, document, testPreparedDiagnostic(t, KurnCompatibilityLabel)))
	if err == nil {
		if _, err := Compose(mixed); err == nil {
			t.Fatal("mixed engine pair composed")
		}
	}
}

func TestPreparedMaxQueueMember(t *testing.T) {
	parent := t.TempDir()
	_ = os.Chmod(parent, 0o700)
	for _, test := range []struct {
		raw      string
		accepted bool
		want     int
	}{
		{"", true, 0}, {`,"max_queue":1`, true, 1}, {`,"max_queue":4096`, true, 4096}, {`,"max_queue":16`, true, 16},
		{`,"max_queue":0`, false, 0}, {`,"max_queue":4097`, false, 0}, {`,"max_queue":"8"`, false, 0}, {`,"max_queue":null`, false, 0}, {`,"max_queue":1.5`, false, 0},
	} {
		root := filepath.Join(parent, "out-"+strings.NewReplacer(`"`, "", ",", "", ":", "-", ".", "_").Replace(test.raw))
		path := writeTestPreparedRoot(t, root, "http://127.0.0.1:1")
		if test.raw != "" {
			rewriteRaw(t, path, func(raw []byte) []byte {
				edited := bytes.Replace(raw, []byte(`"max_inflight":1`), []byte(`"max_inflight":1`+test.raw), 1)
				if bytes.Equal(edited, raw) {
					t.Fatalf("fixture lacks the max_inflight member: %s", raw)
				}
				return edited
			})
		}
		config, err := Load(path)
		if test.accepted != (err == nil) {
			t.Fatalf("max_queue %q: accepted %v err %v", test.raw, err == nil, err)
		}
		if err == nil && config.MaxQueue != test.want {
			t.Fatalf("max_queue %q: bound %d", test.raw, config.MaxQueue)
		}
	}
}

func TestPreparedMaxInflightBoundThroughDiagnostic(t *testing.T) {
	path := newTestPreparedRoot(t)
	mutateConfigFile(t, path, func(v map[string]any) { v["adapter"].(map[string]any)["max_inflight"] = 8 })
	config, err := Load(path)
	if err != nil || config.MaxInflight != 8 {
		t.Fatalf("in-range max_inflight refused at Load: %v", err)
	}
	if service, err := Compose(config); !errors.Is(err, errConfig) || service != nil {
		t.Fatalf("Compose accepted a max_inflight that differs from the diagnostic: %v", err)
	}
}
