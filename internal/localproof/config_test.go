package localproof

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadClosedPrivateConfig(t *testing.T) {
	path := writeFixture(t)
	config, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if config.AdapterName != "catalog-identifier" || config.KurnSourceRevision != KurnSourceRevision || config.AttestedEngine != KurnCompatibilityLabel ||
		config.KurnEndpoint != "http://127.0.0.1:1" ||
		!config.Rate.Unlimited || config.BackendCredential != "backend-token" || len(config.Goldens) != 1 ||
		config.Goldens[0].SentinelTenant != "tenant-neutral" {
		t.Fatalf("resolved config = %+v", config)
	}
	if config.RuntimeStatusFile != filepath.Join(filepath.Dir(path), "status.json") {
		t.Fatalf("status path = %q", config.RuntimeStatusFile)
	}
}

func TestLoadRefusesClosedSurfaceMutations(t *testing.T) {
	mutations := map[string]func(map[string]any){
		"schema":       func(value map[string]any) { value["schema"] = "wrong" },
		"engine":       func(value map[string]any) { value["kurn_source_revision"] = "0000000000000000000000000000000000000000" },
		"engine-label": func(value map[string]any) { value["attested_engine"] = KurnCompatibilityLabel },
		"listen-host":  func(value map[string]any) { value["listen_address"] = "localhost:0" },
		"listen-any":   func(value map[string]any) { value["listen_address"] = "0.0.0.0:0" },
		"backend-host": func(value map[string]any) { value["kurn_endpoint"] = "http://localhost:1" },
		"backend-tls":  func(value map[string]any) { value["kurn_endpoint"] = "https://127.0.0.1:1" },
		"rate": func(value map[string]any) {
			value["rate"] = map[string]any{"unlimited": true, "rate_per_sec": 1}
		},
		"route": func(value map[string]any) {
			value["adapter"].(map[string]any)["name"] = "other"
		},
		"unknown": func(value map[string]any) { value["extra"] = true },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			path := writeFixture(t)
			var value map[string]any
			raw, _ := os.ReadFile(path)
			if json.Unmarshal(raw, &value) != nil {
				t.Fatal("fixture parse")
			}
			mutate(value)
			raw, _ = json.Marshal(value)
			if os.WriteFile(path, raw, 0o600) != nil {
				t.Fatal("fixture mutation")
			}
			if _, err := Load(path); !errors.Is(err, errConfig) {
				t.Fatalf("Load() error = %v", err)
			}
		})
	}
}

func TestLoadRefusesDuplicateSurrogateAndUnsafeFiles(t *testing.T) {
	for name, mutate := range map[string]func(t *testing.T, path string){
		"duplicate": func(t *testing.T, path string) {
			raw, _ := os.ReadFile(path)
			field := []byte(`"schema":"` + ConfigSchema + `"`)
			raw = bytes.Replace(raw, field, append(append([]byte(nil), field...), append([]byte(","), field...)...), 1)
			_ = os.WriteFile(path, raw, 0o600)
		},
		"surrogate": func(t *testing.T, path string) {
			raw, _ := os.ReadFile(path)
			raw = bytes.Replace(raw, []byte(`"catalog-identifier"`), []byte(`"\uD800"`), 1)
			_ = os.WriteFile(path, raw, 0o600)
		},
		"mode": func(t *testing.T, path string) { _ = os.Chmod(path, 0o644) },
	} {
		t.Run(name, func(t *testing.T) {
			path := writeFixture(t)
			mutate(t, path)
			if _, err := Load(path); !errors.Is(err, errConfig) {
				t.Fatalf("Load() error = %v", err)
			}
		})
	}
}

func TestLoadRefusesFileAndCodecBoundaryMutations(t *testing.T) {
	for name, mutate := range map[string]func(t *testing.T, path string){
		"relative-config": func(t *testing.T, path string) {
			if _, err := Load(filepath.Base(path)); !errors.Is(err, errConfig) {
				t.Fatalf("Load(relative) error = %v", err)
			}
		},
		"unsafe-root": func(t *testing.T, path string) {
			_ = os.Chmod(filepath.Dir(path), 0o755)
		},
		"trailing-document": func(t *testing.T, path string) {
			raw, _ := os.ReadFile(path)
			_ = os.WriteFile(path, append(raw, []byte(`{}`)...), 0o600)
		},
		"wrong-scalar-type": func(t *testing.T, path string) {
			raw, _ := os.ReadFile(path)
			raw = bytes.Replace(raw, []byte(`"max_inflight":1`), []byte(`"max_inflight":"1"`), 1)
			_ = os.WriteFile(path, raw, 0o600)
		},
		"config-symlink": func(t *testing.T, path string) {
			link := filepath.Join(filepath.Dir(path), "config-link.json")
			if os.Symlink(path, link) != nil {
				t.Fatal("symlink")
			}
			if _, err := Load(link); !errors.Is(err, errConfig) {
				t.Fatalf("Load(symlink) error = %v", err)
			}
		},
		"credential-symlink": func(t *testing.T, path string) {
			root := filepath.Dir(path)
			link := filepath.Join(root, "credential-link")
			if os.Symlink(filepath.Join(root, "backend-token"), link) != nil {
				t.Fatal("symlink")
			}
			mutateConfigFile(t, path, func(value map[string]any) { value["backend_credential_file"] = link })
		},
		"missing-readiness": func(t *testing.T, path string) {
			mutateConfigFile(t, path, func(value map[string]any) {
				value["readiness_file"] = filepath.Join(filepath.Dir(path), "missing.json")
			})
		},
		"unsafe-auth-mode": func(t *testing.T, path string) {
			var value map[string]any
			raw, _ := os.ReadFile(path)
			_ = json.Unmarshal(raw, &value)
			_ = os.Chmod(value["fixture_authenticator_file"].(string), 0o644)
		},
		"preexisting-status": func(t *testing.T, path string) {
			if os.WriteFile(filepath.Join(filepath.Dir(path), "status.json"), []byte(`{}`), 0o600) != nil {
				t.Fatal("status")
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			path := writeFixture(t)
			mutate(t, path)
			if name == "relative-config" || name == "config-symlink" {
				return
			}
			if _, err := Load(path); !errors.Is(err, errConfig) {
				t.Fatalf("Load() error = %v", err)
			}
		})
	}
}

func TestRunRequiresExactlyOneConfigArgument(t *testing.T) {
	path := writeFixture(t)
	for name, args := range map[string][]string{
		"none":      nil,
		"unknown":   {"-unknown"},
		"duplicate": {"-config", path, "-config", path},
		"trailing":  {"-config", path, "extra"},
		"empty":     {"-config", ""},
	} {
		t.Run(name, func(t *testing.T) {
			var stderr bytes.Buffer
			if code := Run(args, nil, &stderr); code != ExitConfig || stderr.String() != "kurn-searchd: configuration refused\n" {
				t.Fatalf("Run() = %d, %q", code, stderr.String())
			}
		})
	}
}

func mutateConfigFile(t *testing.T, path string, mutate func(map[string]any)) {
	t.Helper()
	var value map[string]any
	raw, _ := os.ReadFile(path)
	if json.Unmarshal(raw, &value) != nil {
		t.Fatal("fixture parse")
	}
	mutate(value)
	raw, _ = json.Marshal(value)
	if os.WriteFile(path, raw, 0o600) != nil {
		t.Fatal("fixture mutation")
	}
}

func TestComposeRateBoundaryPrecedesBackendIO(t *testing.T) {
	config, err := Load(writeFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	config.Rate = rateAttestation(16)
	if _, err := Compose(config); !errors.Is(err, errConfig) {
		t.Fatalf("rate 16 error = %v", err)
	}
	config.Rate = rateAttestation(32)
	service, err := Compose(config)
	if err != nil {
		t.Fatalf("rate 32 composition error = %v", err)
	}
	if err := service.Ready(context.Background()); err == nil {
		t.Fatal("rate 32 unexpectedly reached unavailable backend")
	}
}

func TestRunRefusalAndStatusExclusion(t *testing.T) {
	var stderr bytes.Buffer
	if code := Run(nil, nil, &stderr); code != ExitConfig || stderr.String() != "kurn-searchd: configuration refused\n" {
		t.Fatalf("Run() = %d, %q", code, stderr.String())
	}
	root := t.TempDir()
	_ = os.Chmod(root, 0o700)
	path := filepath.Join(root, "status")
	if err := writeStatus(path, "127.0.0.1:1234"); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("status mode = %o", info.Mode().Perm())
	}
	if err := writeStatus(path, "127.0.0.1:1234"); err == nil {
		t.Fatal("status replacement accepted")
	}
}

func rateAttestation(value int) searchRate {
	return searchRate{RatePerSecond: value}
}

// Aliases keep the test's boundary intent readable without exporting the
// service package from the proof config API.
type searchRate = struct {
	RatePerSecond int
	Unlimited     bool
}

func writeFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if os.Chmod(root, 0o700) != nil {
		t.Fatal("chmod root")
	}
	write := func(name string, value any) string {
		path := filepath.Join(root, name)
		raw, err := json.Marshal(value)
		if err != nil || os.WriteFile(path, raw, 0o600) != nil {
			t.Fatal("write fixture")
		}
		return path
	}
	credential := filepath.Join(root, "backend-token")
	if os.WriteFile(credential, []byte("backend-token"), 0o600) != nil {
		t.Fatal("write credential")
	}
	auth := write("auth.json", map[string]any{
		"schema":      AuthenticatorSchema,
		"credentials": []any{map[string]any{"handle": "client-token", "tenant": "tenant-neutral"}},
	})
	readiness := write("readiness.json", map[string]any{
		"schema": ReadinessSchema,
		"goldens": []any{map[string]any{
			"name": "positive", "route": "catalog-identifier", "auth_handle": "client-token",
			"sentinel_tenant": "tenant-neutral", "query": "ABC-123", "limit": 25,
			"expected_ids": []any{}, "expected_complete": true,
		}},
	})
	return write("config.json", map[string]any{
		"schema": ConfigSchema, "listen_address": "127.0.0.1:0",
		"runtime_status_file": filepath.Join(root, "status.json"),
		"kurn_endpoint":       "http://127.0.0.1:1", "backend_credential_file": credential,
		"kurn_source_revision": KurnSourceRevision, "rate": map[string]any{"unlimited": true, "rate_per_sec": 0},
		"adapter": map[string]any{
			"name": "catalog-identifier", "physical_list_name": "catalog-identifiers-v1",
			"config_sha256":  "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			"timeout_millis": 30000, "max_inflight": 1, "worst_case_charge_bytes": 1073741824,
		},
		"fixture_authenticator_file": auth, "readiness_file": readiness,
	})
}

func TestPartsRecipeSelectionAndGoldenRoute(t *testing.T) {
	for _, name := range []string{"catalog-identifier", "parts-catalog", "parts-catalog-discover", "parts-catalog-select", "parts-catalog-suggest"} {
		for _, route := range []string{"catalog-identifier", "parts-catalog", "parts-catalog-discover", "parts-catalog-select", "parts-catalog-suggest", "other"} {
			t.Run(name+"/"+route, func(t *testing.T) {
				path := writeFixture(t)
				mutateConfigFile(t, path, func(value map[string]any) {
					value["adapter"].(map[string]any)["name"] = name
					if name == "parts-catalog" || name == "parts-catalog-select" {
						value["adapter"].(map[string]any)["physical_list_name"] = "parts-catalog-v1"
					} else if name == "parts-catalog-discover" {
						value["adapter"].(map[string]any)["physical_list_name"] = "parts-catalog-discover-v1"
					} else if name == "parts-catalog-suggest" {
						value["adapter"].(map[string]any)["physical_list_name"] = "parts-catalog-suggest-v1"
					}
				})
				mutateConfigFile(t, filepath.Join(filepath.Dir(path), "readiness.json"), func(value map[string]any) {
					golden := value["goldens"].([]any)[0].(map[string]any)
					golden["route"] = route
					golden["query"] = "GX-200"
					golden["expected_ids"] = []string{"PC-009"}
				})
				config, err := Load(path)
				if route != name {
					if !errors.Is(err, errConfig) {
						t.Fatal("another recipe golden accepted", err)
					}
					return
				}
				if err != nil || config.AdapterName != name || config.Goldens[0].Route != name {
					t.Fatal("matching recipe/golden refused", err)
				}
				service, err := Compose(config)
				if err != nil || service == nil {
					t.Fatal("recipe composition", err)
				}
				// Composition itself performs no network I/O. The endpoint is
				// deliberately unavailable; live readiness is a separate gate.
				config.AdapterName = "other"
				if _, err := Compose(config); !errors.Is(err, errConfig) {
					t.Fatal("direct unknown recipe accepted", err)
				}
			})
		}
	}
}

func TestPartsRecipeStrictConfigName(t *testing.T) {
	for _, value := range []any{nil, "", "Parts-Catalog", "parts-catalog ", []string{"parts-catalog"}, 1} {
		path := writeFixture(t)
		mutateConfigFile(t, path, func(config map[string]any) { config["adapter"].(map[string]any)["name"] = value })
		if _, err := Load(path); !errors.Is(err, errConfig) {
			t.Fatal("invalid adapter name accepted", value, err)
		}
	}
}
