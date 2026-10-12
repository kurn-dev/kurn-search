package preparedsetup

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/kurn-dev/kurn-search/adapters/partscatalogsuggest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestConfigOnlyFourPathsChange(t *testing.T) {
	o := Options{KurndAddress: "127.0.0.1:1", SearchdAddress: "127.0.0.1:2"}
	r := verifyReport{Base: bytesSHA([]byte("base")), Entries: 1}
	if !sameConfigExceptPaths(configBytes("/stage", o, r), configBytes("/final", o, r)) {
		t.Fatal("config drift")
	}
	if bytes.Contains(configBytes("/stage", o, r), []byte("trusted")) {
		t.Fatal("trusted")
	}
}

func TestRunStrictDispatch(t *testing.T) {
	for _, args := range [][]string{{}, {"unknown"}, {"verify-version", "-out=x"}, {"verify-version", "-out", "x", "extra"}, {"prepare-artifact", "-profile=parts-catalog-suggest"}} {
		var out, err strings.Builder
		if Run(args, &out, &err) == 0 || out.Len() != 0 || err.String() != "kurn-search-local: refused\n" {
			t.Fatalf("accepted %#v", args)
		}
	}
}

func TestRunSharedTypedFailureDispatch(t *testing.T) {
	args := []string{"prepare-artifact", "-profile", "parts-catalog-suggest", "-source", "/source", "-kurn", "/kurn", "-kurnd", "/kurnd", "-searchd", "/searchd", "-search-checkout", "/search", "-build-dir", "/build", "-out", "/out", "-readiness-tenant", "alpha", "-readiness-partition", "north", "-kurnd-addr", "127.0.0.1:1", "-searchd-addr", "127.0.0.1:2"}
	var stdout, stderr strings.Builder
	calls := 0
	code := runWithPrepare(args, &stdout, &stderr, func(context.Context, Options) (Result, error) {
		calls++
		return Result{}, stateFailure("PARTIAL")
	})
	if code == 0 || calls != 1 || stdout.Len() != 0 || stderr.String() != "{\"schema\":\"kurn-search-prepared-result/v1\",\"status\":\"PARTIAL\"}\n" {
		t.Fatalf("typed dispatch code=%d calls=%d stdout=%q stderr=%q", code, calls, stdout.String(), stderr.String())
	}
}

func prepareArgs(options Options) []string {
	return []string{"prepare-artifact", "-profile", options.Profile, "-source", options.Source, "-kurn", options.Kurn, "-kurnd", options.Kurnd, "-searchd", options.Searchd, "-search-checkout", options.SearchCheckout, "-build-dir", options.BuildDir, "-out", options.Out, "-readiness-tenant", options.ReadinessTenant, "-readiness-partition", options.ReadinessPartition, "-kurnd-addr", options.KurndAddress, "-searchd-addr", options.SearchdAddress}
}

func TestMappingBytesAreProtectedExample(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "examples", "parts-catalog-suggest", "list-mapping.json"))
	if err != nil || !bytes.Equal(raw, mappingBytes) {
		t.Fatalf("mapping drift: %v", err)
	}
}

type prepareTestFixture struct {
	options Options
	deps    prepareDependencies
	root    string
}

func newPrepareTestFixture(t *testing.T) prepareTestFixture {
	t.Helper()
	root := t.TempDir()
	for _, name := range []string{"build-parent", "out-parent"} {
		if err := os.Mkdir(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	source := filepath.Join(root, "source.ndjson")
	line := []byte("{\"category\":\"sentinel\",\"id\":\"A01\",\"identifier\":\"ZS-901\",\"manufacturer\":\"Northstar\",\"partition_id\":\"north\",\"status\":\"active\",\"tenant_id\":\"alpha\"}\n")
	if err := os.WriteFile(source, line, 0600); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	o := Options{Source: source, Kurn: exe, Kurnd: exe, Searchd: exe, SearchCheckout: filepath.Join("..", ".."), BuildDir: filepath.Join(root, "build-parent", "build"), Out: filepath.Join(root, "out-parent", "out"), ReadinessTenant: "alpha", ReadinessPartition: "north", KurndAddress: "127.0.0.1:18080", SearchdAddress: "127.0.0.1:18081", Profile: "parts-catalog-suggest"}
	d := productionPrepareDependencies()
	d.executableAssertion = func(string) (buildAssertion, error) {
		return buildAssertion{GoVersion: "synthetic", MainPath: "synthetic/main", Path: "synthetic/main", VCSRevision: "absent", VCSModified: "absent"}, nil
	}
	d.run = func(_ context.Context, cwd, _ string, args ...string) (commandResult, error) {
		if len(args) == 0 {
			return commandResult{}, errRefused
		}
		value := func(flag string) string {
			for i := 0; i+1 < len(args); i++ {
				if args[i] == flag {
					return args[i+1]
				}
			}
			return ""
		}
		artifact := value("-artifact")
		if args[0] == "build" {
			projected := value("-in")
			input, _, err := fileSHA(projected)
			if err != nil || artifact == "" || os.WriteFile(artifact, []byte("synthetic artifact fixture\n"), 0600) != nil {
				return commandResult{}, errRefused
			}
			container, _, _ := fileSHA(artifact)
			base := strings.Repeat("b", 64)
			stdout := fmt.Sprintf("input sha256 %s (preserve this receipt with the source ref)\nartifact %s: list %s, 1 entries, 1 keys, base %s (mode ngram)\ncontainer sha256 %s\n", input, artifact, listName, base, container)
			return commandResult{stdout: []byte(stdout)}, nil
		}
		if args[0] == "verify" && len(args) == 3 {
			artifact = args[2]
			input, _, err := fileSHA(filepath.Join(cwd, "projected.ndjson"))
			container, _, artifactErr := fileSHA(artifact)
			if err != nil || artifactErr != nil {
				return commandResult{}, errRefused
			}
			base := strings.Repeat("b", 64)
			stdout := fmt.Sprintf("verified %s at level \"full + semantic\"\n  list              %s\n  mode              ngram\n  exact companion   yes (section kind 7)\n  format            KURNART1 1.1\n  entries           1\n  keys              1\n  base id           %s (recomputed from the records)\n  container sha256  %s\nsemantic checks (rebuilt from the records with the engine):\n  config digest     ok  declared %s, recomputed %s\n  analyzer digest   ok  declared %s, recomputed %s\n  index (ngram)     ok  byte-identical to the rebuild\n  exact companion   ok  byte-identical to the rebuild\n  dropped_keys      ok  declared 0, recomputed 0\n  keyless_entries   ok  declared 0, recomputed 0\ndeclared, not verifiable from the artifact:\n  source            \"\"\n  created at        \"\"\n  input sha256      \"%s\"\n  collapsed         0\nnote: verification shows the artifact is self-consistent; it does not authenticate who produced it\n", artifact, listName, base, container, configSHA, configSHA, analyzerSHA, analyzerSHA, input)
			return commandResult{stdout: []byte(stdout)}, nil
		}
		return commandResult{}, errRefused
	}
	return prepareTestFixture{options: o, deps: d, root: root}
}

func registerCapturedTreeCleanup(t *testing.T, path string) {
	t.Helper()
	root, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return
	}
	if err != nil || !root.IsDir() || root.Mode()&os.ModeSymlink != 0 || !uidOwned(root) {
		t.Fatalf("capture cleanup root %s: %v", path, err)
	}
	owned := reservationOwnership{root: root, children: map[string]os.FileInfo{}}
	for _, name := range []string{"bin", "data"} {
		child, childErr := os.Lstat(filepath.Join(path, name))
		if os.IsNotExist(childErr) {
			continue
		}
		if childErr != nil || !child.IsDir() || child.Mode()&os.ModeSymlink != 0 || !uidOwned(child) {
			t.Fatalf("capture cleanup child %s/%s: %v", path, name, childErr)
		}
		owned.children[name] = child
	}
	t.Cleanup(func() {
		if err := removeOwned(path, owned); err != nil && !absent(path) {
			t.Errorf("captured cleanup %s: %v", path, err)
		}
	})
}

func registerFixtureCleanup(t *testing.T, options Options) {
	t.Helper()
	for _, path := range []string{
		options.BuildDir,
		options.Out,
		filepath.Join(filepath.Dir(options.BuildDir), "."+filepath.Base(options.BuildDir)+reservationSuffix),
		filepath.Join(filepath.Dir(options.Out), "."+filepath.Base(options.Out)+reservationSuffix),
	} {
		registerCapturedTreeCleanup(t, path)
	}
}

func prepareFailureState(err error) string {
	var state *stateError
	if errors.As(err, &state) {
		return state.state
	}
	if err != nil {
		return "REFUSED"
	}
	return ""
}

func TestPrepareSharedPipelineFinalLoadAndVerifyPositive(t *testing.T) {
	f := newPrepareTestFixture(t)
	loadCalls, verifyCalls := 0, 0
	realLoad, realVerify := f.deps.load, f.deps.verify
	f.deps.load = func(path string) error { loadCalls++; return realLoad(path) }
	f.deps.verify = func(path string) (Verification, error) { verifyCalls++; return realVerify(path) }
	result, err := prepareWithDependencies(context.Background(), f.options, f.deps)
	registerFixtureCleanup(t, f.options)
	if err != nil || result.Status != "PREPARED" || result.Records != 1 || result.Tenants != 1 || loadCalls != 2 || verifyCalls != 1 {
		t.Fatalf("positive shared pipeline: %#v %v load=%d verify=%d", result, err, loadCalls, verifyCalls)
	}
	for _, path := range []string{f.options.BuildDir, f.options.Out} {
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("missing published %s: %v", path, err)
		}
	}
	for _, path := range []string{filepath.Join(filepath.Dir(f.options.BuildDir), "."+filepath.Base(f.options.BuildDir)+reservationSuffix), filepath.Join(filepath.Dir(f.options.Out), "."+filepath.Base(f.options.Out)+reservationSuffix)} {
		if !absent(path) {
			t.Fatalf("residual reservation %s", path)
		}
	}
}

func TestPreparePublicationFaultDispositionMatrix(t *testing.T) {
	cases := []struct {
		name, want string
		inject     func(*prepareTestFixture, *int)
		wantBuild  bool
		wantOut    bool
	}{
		{name: "final-config-rename", want: "REFUSED", inject: func(f *prepareTestFixture, hits *int) {
			real := f.deps.rename
			f.deps.rename = func(a, b string) error {
				if filepath.Base(a) == ".final-config" {
					(*hits)++
					return errors.New("injected final config rename")
				}
				return real(a, b)
			}
		}},
		{name: "artifact-sync", want: "REFUSED", inject: func(f *prepareTestFixture, hits *int) {
			f.deps.syncFile = func(string) error { (*hits)++; return errors.New("injected artifact sync") }
		}},
		{name: "b-rename", want: "REFUSED", inject: func(f *prepareTestFixture, hits *int) {
			real := f.deps.rename
			f.deps.rename = func(a, b string) error {
				if b == f.options.BuildDir {
					(*hits)++
					return errors.New("injected B rename")
				}
				return real(a, b)
			}
		}},
		{name: "b-parent-sync", want: "UNCERTAIN", wantBuild: true, inject: func(f *prepareTestFixture, hits *int) {
			real := f.deps.syncDir
			parent := filepath.Dir(f.options.BuildDir)
			f.deps.syncDir = func(path string) error {
				if path == parent {
					(*hits)++
					return errors.New("injected B parent sync")
				}
				return real(path)
			}
		}},
		{name: "o-rename", want: "PARTIAL", wantBuild: true, inject: func(f *prepareTestFixture, hits *int) {
			real := f.deps.rename
			f.deps.rename = func(a, b string) error {
				if b == f.options.Out {
					(*hits)++
					return errors.New("injected O rename")
				}
				return real(a, b)
			}
		}},
		{name: "o-parent-sync", want: "UNCERTAIN", wantBuild: true, wantOut: true, inject: func(f *prepareTestFixture, hits *int) {
			real := f.deps.syncDir
			parent := filepath.Dir(f.options.Out)
			f.deps.syncDir = func(path string) error {
				if path == parent {
					(*hits)++
					return errors.New("injected O parent sync")
				}
				return real(path)
			}
		}},
		{name: "final-load", want: "REFUSED_AFTER_PUBLICATION", wantBuild: true, wantOut: true, inject: func(f *prepareTestFixture, hits *int) {
			real := f.deps.load
			calls := 0
			f.deps.load = func(path string) error {
				calls++
				if calls == 2 {
					(*hits)++
					return errors.New("injected final Load")
				}
				return real(path)
			}
		}},
		{name: "final-verify", want: "REFUSED_AFTER_PUBLICATION", wantBuild: true, wantOut: true, inject: func(f *prepareTestFixture, hits *int) {
			f.deps.verify = func(string) (Verification, error) {
				(*hits)++
				return Verification{}, errors.New("injected final VerifyVersion")
			}
		}},
	}
	for _, syncName := range []string{"state", "data", "bin", "otmp", "btmp"} {
		name := syncName
		cases = append(cases, struct {
			name, want string
			inject     func(*prepareTestFixture, *int)
			wantBuild  bool
			wantOut    bool
		}{name: "sync-" + name, want: "REFUSED", inject: func(f *prepareTestFixture, hits *int) {
			real := f.deps.syncDir
			f.deps.syncDir = func(path string) error {
				btmp := filepath.Join(filepath.Dir(f.options.BuildDir), "."+filepath.Base(f.options.BuildDir)+reservationSuffix)
				otmp := filepath.Join(filepath.Dir(f.options.Out), "."+filepath.Base(f.options.Out)+reservationSuffix)
				targets := map[string]string{"state": filepath.Join(otmp, "state"), "data": filepath.Join(otmp, "data"), "bin": filepath.Join(otmp, "bin"), "otmp": otmp, "btmp": btmp}
				if path == targets[name] {
					(*hits)++
					return errors.New("injected directory sync")
				}
				return real(path)
			}
		}})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newPrepareTestFixture(t)
			hits := 0
			tc.inject(&f, &hits)
			result, err := prepareWithDependencies(context.Background(), f.options, f.deps)
			registerFixtureCleanup(t, f.options)
			btmp := filepath.Join(filepath.Dir(f.options.BuildDir), "."+filepath.Base(f.options.BuildDir)+reservationSuffix)
			otmp := filepath.Join(filepath.Dir(f.options.Out), "."+filepath.Base(f.options.Out)+reservationSuffix)
			if result.Status == "PREPARED" || hits != 1 || prepareFailureState(err) != tc.want || absent(f.options.BuildDir) == tc.wantBuild || absent(f.options.Out) == tc.wantOut || !absent(btmp) || !absent(otmp) {
				t.Fatalf("outcome=%s result=%#v err=%v hits=%d B=%t O=%t Btmp=%t Otmp=%t", prepareFailureState(err), result, err, hits, !absent(f.options.BuildDir), !absent(f.options.Out), !absent(btmp), !absent(otmp))
			}
		})
	}
}

func TestPrepareCleanupFailureRetainedRemnantsPrecedence(t *testing.T) {
	for _, kind := range []string{"descriptor-chmod", "final-binding-recheck", "removal"} {
		t.Run(kind, func(t *testing.T) {
			f := newPrepareTestFixture(t)
			realRename, realRemove := f.deps.rename, f.deps.removeOwned
			otmp := filepath.Join(filepath.Dir(f.options.Out), "."+filepath.Base(f.options.Out)+reservationSuffix)
			btmp := filepath.Join(filepath.Dir(f.options.BuildDir), "."+filepath.Base(f.options.BuildDir)+reservationSuffix)
			f.deps.rename = func(a, b string) error {
				if b == f.options.BuildDir {
					return errors.New("injected B rename")
				}
				return realRename(a, b)
			}
			faultHits := 0
			f.deps.removeOwned = func(path string, owned reservationOwnership) error {
				if path != otmp {
					return realRemove(path, owned)
				}
				operations := productionCleanupOperations()
				switch kind {
				case "descriptor-chmod":
					operations.chmod = func(*os.File, os.FileMode) error { faultHits++; return errors.New("injected descriptor chmod") }
				case "final-binding-recheck":
					realLstat := operations.lstat
					rootChecks := 0
					operations.lstat = func(path string) (os.FileInfo, error) {
						if path == otmp {
							rootChecks++
							if rootChecks == 2 {
								faultHits++
								return nil, errors.New("injected final binding recheck")
							}
						}
						return realLstat(path)
					}
				case "removal":
					operations.removeAll = func(string) error { faultHits++; return errors.New("injected removal") }
				}
				return removeOwnedWith(path, owned, operations)
			}
			result, err := prepareWithDependencies(context.Background(), f.options, f.deps)
			registerFixtureCleanup(t, f.options)
			if result.Status == "PREPARED" || prepareFailureState(err) != "REFUSED_RETAINED_REMNANTS" || faultHits != 1 || absent(otmp) || !absent(btmp) || !absent(f.options.BuildDir) || !absent(f.options.Out) {
				t.Fatalf("cleanup precedence result=%#v err=%v hits=%d Btmp=%t Otmp=%t", result, err, faultHits, !absent(btmp), !absent(otmp))
			}
		})
	}
}

func TestRunActualPrepareCleanupFailureTypedDispatch(t *testing.T) {
	f := newPrepareTestFixture(t)
	realRename, realRemove := f.deps.rename, f.deps.removeOwned
	otmp := filepath.Join(filepath.Dir(f.options.Out), "."+filepath.Base(f.options.Out)+reservationSuffix)
	f.deps.rename = func(a, b string) error {
		if b == f.options.BuildDir {
			return errors.New("injected B rename")
		}
		return realRename(a, b)
	}
	faultHits := 0
	f.deps.removeOwned = func(path string, owned reservationOwnership) error {
		if path != otmp {
			return realRemove(path, owned)
		}
		operations := productionCleanupOperations()
		operations.removeAll = func(string) error { faultHits++; return errors.New("injected removal") }
		return removeOwnedWith(path, owned, operations)
	}
	var stdout, stderr strings.Builder
	code := runWithPrepare(prepareArgs(f.options), &stdout, &stderr, func(ctx context.Context, options Options) (Result, error) {
		return prepareWithDependencies(ctx, options, f.deps)
	})
	registerFixtureCleanup(t, f.options)
	if code == 0 || faultHits != 1 || stdout.Len() != 0 || stderr.String() != "{\"schema\":\"kurn-search-prepared-result/v1\",\"status\":\"REFUSED_RETAINED_REMNANTS\"}\n" || strings.Contains(stderr.String(), f.options.Source) || strings.Contains(stderr.String(), "PREPARED\"") || absent(otmp) {
		t.Fatalf("actual typed dispatch code=%d hits=%d stdout=%q stderr=%q Otmp=%t", code, faultHits, stdout.String(), stderr.String(), !absent(otmp))
	}
}

func TestPrepareCleanupRefusesForeignRootReplacement(t *testing.T) {
	f := newPrepareTestFixture(t)
	realRename := f.deps.rename
	otmp := filepath.Join(filepath.Dir(f.options.Out), "."+filepath.Base(f.options.Out)+reservationSuffix)
	original := otmp + ".original"
	var foreignBefore os.FileInfo
	f.deps.rename = func(a, b string) error {
		if b == f.options.BuildDir {
			if err := os.Rename(otmp, original); err != nil || os.Mkdir(otmp, 0700) != nil || os.WriteFile(filepath.Join(otmp, "marker"), []byte("foreign"), 0600) != nil {
				return fmt.Errorf("foreign fixture: %w", err)
			}
			foreignBefore, _ = os.Lstat(otmp)
			return errors.New("injected B rename after replacement")
		}
		return realRename(a, b)
	}
	result, err := prepareWithDependencies(context.Background(), f.options, f.deps)
	registerCapturedTreeCleanup(t, original)
	registerCapturedTreeCleanup(t, otmp)
	foreignAfter, statErr := os.Lstat(otmp)
	marker, readErr := os.ReadFile(filepath.Join(otmp, "marker"))
	if result.Status == "PREPARED" || prepareFailureState(err) != "REFUSED_RETAINED_REMNANTS" || statErr != nil || foreignBefore == nil || !os.SameFile(foreignBefore, foreignAfter) || string(marker) != "foreign" || readErr != nil || foreignAfter.Mode().Perm() != 0700 {
		t.Fatalf("foreign root result=%#v err=%v stat=%v marker=%q/%v", result, err, statErr, marker, readErr)
	}
}

func TestPrepareRefusesDescendantHeldStdoutBeforePublication(t *testing.T) {
	f := newPrepareTestFixture(t)
	script, handshake, pidfile := filepath.Join(f.root, "held-builder"), filepath.Join(f.root, "held"), filepath.Join(f.root, "pid")
	body := "#!/bin/sh\n( printf held > '" + handshake + "'; exec /bin/sleep 5 ) &\nprintf '%s' \"$!\" > '" + pidfile + "'\nwhile [ ! -f '" + handshake + "' ]; do :; done\nprintf partial-valid-looking-output\n"
	if err := os.WriteFile(script, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		raw, err := os.ReadFile(pidfile)
		if err != nil {
			return
		}
		pid, err := strconv.Atoi(string(raw))
		if err != nil || pid <= 0 {
			return
		}
		_ = syscall.Kill(pid, syscall.SIGTERM)
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Errorf("builder descendant %d did not terminate", pid)
	})
	f.deps.run = func(ctx context.Context, cwd, _ string, _ ...string) (commandResult, error) {
		return runCommandWithLimits(ctx, cwd, script, commandLimits{timeout: 2 * time.Second, waitDelay: 50 * time.Millisecond})
	}
	result, err := prepareWithDependencies(context.Background(), f.options, f.deps)
	registerFixtureCleanup(t, f.options)
	if result.Status == "PREPARED" || !errors.Is(err, exec.ErrWaitDelay) || !absent(f.options.BuildDir) || !absent(f.options.Out) {
		t.Fatalf("WaitDelay reached publication: %#v %v B=%t O=%t", result, err, !absent(f.options.BuildDir), !absent(f.options.Out))
	}
}

// The default selection is osa1-060, persisted explicitly; the legacy route
// binds the empty policy with the legacy identity (no policy member,
// threshold 650,000, the legacy profile digests); explicit policies select
// themselves; a bound value is never accepted as an option name.
func TestPrepareDefaultSelectsOSA1G60AndLegacyRoute(t *testing.T) {
	def := newPrepareTestFixture(t)
	result, err := prepareWithDependencies(context.Background(), def.options, def.deps)
	registerFixtureCleanup(t, def.options)
	if err != nil || result.Status != "PREPARED" {
		t.Fatalf("default pipeline: %#v %v", result, err)
	}
	config, _ := os.ReadFile(filepath.Join(def.options.Out, "kurn-searchd.json"))
	manifest, _ := os.ReadFile(filepath.Join(def.options.Out, "manifest.json"))
	diagnostic, _ := os.ReadFile(filepath.Join(def.options.Out, "profile-diagnostic.json"))
	inflight := def.options.MaxInflight
	if inflight == 0 {
		inflight = defaultMaxInflight
	}
	if !strings.Contains(string(config), `"max_queue":`+strconv.Itoa(defaultQueueFactor*inflight)) || !strings.Contains(string(manifest), `"max_queue":`+strconv.Itoa(defaultQueueFactor*inflight)) {
		t.Fatalf("default queue bound not persisted:\n%s\n%s", config, manifest)
	}
	if !strings.Contains(string(config), `"policy":"osa1-060"`) || !strings.Contains(string(manifest), `"policy":"osa1-060"`) ||
		!strings.Contains(string(diagnostic), `"policy": "osa1-060"`) || !strings.Contains(string(diagnostic), `"threshold_millionths": 600000`) {
		t.Fatalf("default selection not persisted:\n%s\n%s", config, diagnostic)
	}
	if v, err := VerifyVersion(def.options.Out); err != nil || v.Status != "VERIFIED" {
		t.Fatalf("verify-version default: %+v %v", v, err)
	}
	legacy := newPrepareTestFixture(t)
	legacy.options.Policy = PolicyLegacy
	result, err = prepareWithDependencies(context.Background(), legacy.options, legacy.deps)
	registerFixtureCleanup(t, legacy.options)
	if err != nil || result.Status != "PREPARED" {
		t.Fatalf("legacy pipeline: %#v %v", result, err)
	}
	config, _ = os.ReadFile(filepath.Join(legacy.options.Out, "kurn-searchd.json"))
	manifest, _ = os.ReadFile(filepath.Join(legacy.options.Out, "manifest.json"))
	diagnostic, _ = os.ReadFile(filepath.Join(legacy.options.Out, "profile-diagnostic.json"))
	if strings.Contains(string(config), `"policy"`) || strings.Contains(string(manifest), `"policy"`) || strings.Contains(string(diagnostic), `"policy"`) ||
		!strings.Contains(string(diagnostic), `"threshold_millionths": 650000`) ||
		!strings.Contains(string(manifest), `"behavior_sha256":"ef4c10f157ecdace26c0a468d401de7ba581aa02fc15dd467ace2383a354ee7d"`) ||
		!strings.Contains(string(manifest), `"codec_sha256":"34fe514caebf72a602620927a6fb28f7a00f3b4aba5e9df9a12861e120b46972"`) {
		t.Fatalf("legacy route changed identity:\n%s\n%s", config, diagnostic)
	}
	if v, err := VerifyVersion(legacy.options.Out); err != nil || v.Status != "VERIFIED" {
		t.Fatalf("verify-version legacy: %+v %v", v, err)
	}
	queued := newPrepareTestFixture(t)
	queued.options.MaxQueue = 7
	result, err = prepareWithDependencies(context.Background(), queued.options, queued.deps)
	registerFixtureCleanup(t, queued.options)
	if err != nil || result.Status != "PREPARED" {
		t.Fatalf("max_queue pipeline: %#v %v", result, err)
	}
	config, _ = os.ReadFile(filepath.Join(queued.options.Out, "kurn-searchd.json"))
	manifest, _ = os.ReadFile(filepath.Join(queued.options.Out, "manifest.json"))
	if !strings.Contains(string(config), `"max_queue":7`) || !strings.Contains(string(manifest), `"max_queue":7`) {
		t.Fatalf("explicit queue bound not persisted:\n%s", config)
	}
	if v, err := VerifyVersion(queued.options.Out); err != nil || v.Status != "VERIFIED" {
		t.Fatalf("verify-version max_queue: %+v %v", v, err)
	}
	over := newPrepareTestFixture(t)
	over.options.MaxQueue = maxMaxQueue + 1
	if _, err := prepareWithDependencies(context.Background(), over.options, over.deps); err == nil {
		t.Fatal("over-range max_queue prepared")
	}
	registerFixtureCleanup(t, over.options)
	for _, name := range []string{"", PolicyLegacy, partscatalogsuggest.PolicyOSA1, partscatalogsuggest.PolicyOSA1G60, partscatalogsuggest.PolicyOSA1G60Union} {
		if _, ok := selectPolicy(name); !ok {
			t.Fatalf("selectPolicy(%q) refused", name)
		}
	}
	if _, ok := selectPolicy("nope"); ok {
		t.Fatal("unknown policy selected")
	}
}

// A version prepared with the opt-in policy binds it coherently: the searchd
// configuration names it, the manifest records it, the profile diagnostic
// carries it and the version verifies; the mapping and artifact are the
// default ones. An unknown policy is refused before anything is built.
func TestPrepareBindsOptInPolicy(t *testing.T) {
	f := newPrepareTestFixture(t)
	f.options.Policy = partscatalogsuggest.PolicyOSA1
	result, err := prepareWithDependencies(context.Background(), f.options, f.deps)
	registerFixtureCleanup(t, f.options)
	if err != nil || result.Status != "PREPARED" {
		t.Fatalf("policy pipeline: %#v %v", result, err)
	}
	config, err := os.ReadFile(filepath.Join(f.options.Out, "kurn-searchd.json"))
	if err != nil || !strings.Contains(string(config), `"policy":"osa1"`) {
		t.Fatalf("config: %v %s", err, config)
	}
	manifest, err := os.ReadFile(filepath.Join(f.options.Out, "manifest.json"))
	if err != nil || !strings.Contains(string(manifest), `"policy":"osa1"`) {
		t.Fatalf("manifest: %v", err)
	}
	diagnostic, err := os.ReadFile(filepath.Join(f.options.Out, "profile-diagnostic.json"))
	if err != nil || !strings.Contains(string(diagnostic), `"policy": "osa1"`) || !strings.Contains(string(diagnostic), `"threshold_millionths": 450000`) {
		t.Fatalf("diagnostic: %v", err)
	}
	if v, err := VerifyVersion(f.options.Out); err != nil || v.Status != "VERIFIED" {
		t.Fatalf("verify-version: %+v %v", v, err)
	}
	bad := newPrepareTestFixture(t)
	bad.options.Policy = "nope"
	if _, err := prepareWithDependencies(context.Background(), bad.options, bad.deps); err == nil {
		t.Fatal("unknown policy prepared")
	}
	registerFixtureCleanup(t, bad.options)
}
