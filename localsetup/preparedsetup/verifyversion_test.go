package preparedsetup

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/kurn-dev/kurn-search/adapters/partscatalogsuggest"
	"github.com/kurn-dev/kurn-search/internal/localproof"
)

func TestVerifyVersionRefusesMissingManifest(t *testing.T) {
	d := t.TempDir()
	os.Chmod(d, 0700)
	if _, e := VerifyVersion(d); e == nil {
		t.Fatal("accepted")
	}
	os.Mkdir(filepath.Join(d, "state"), 0700)
	if _, e := VerifyVersion(d); e == nil {
		t.Fatal("accepted")
	}
}

func TestVerifyVersionExactInventoryAndStatus(t *testing.T) {
	root, _, _ := newVerifyVersionFixture(t)
	got, err := VerifyVersion(root)
	if err != nil || !got.Startable {
		t.Fatalf("verify: %#v %v", got, err)
	}
	status := filepath.Join(root, "state", "search-status.json")
	if err := os.WriteFile(status, []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err = VerifyVersion(root)
	if err != nil || got.Startable || got.Warning == "" {
		t.Fatalf("status warning: %#v %v", got, err)
	}
	if err := os.WriteFile(filepath.Join(root, "state", "extra"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyVersion(root); err == nil {
		t.Fatal("accepted extra state file")
	}
}

func newVerifyVersionFixture(t *testing.T) (string, manifest, map[string][]byte) {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"state", "bin", "data"} {
		if err := os.Mkdir(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	m := testValidManifest(t)
	files := map[string][]byte{
		"kurn-searchd.json":          []byte("config"),
		"backend-token":              []byte("token"),
		"fixture-auth.json":          []byte("auth"),
		"readiness.json":             []byte("ready"),
		"kurnd-api-keys":             []byte("keys"),
		"client-credentials.json":    []byte("client"),
		"profile-diagnostic.json":    nil,
		"bin/kurnd":                  []byte("kurnd"),
		"bin/kurn-searchd":           []byte("searchd"),
		"data/" + listName + ".kurn": []byte("artifact"),
	}
	descriptor, _ := partscatalogsuggest.NewDescriptor(partscatalogsuggest.OperationalBinding{PhysicalListName: listName, ConfigSHA256: configSHA, TestedEngines: []string{localproof.KurnCompatibilityLabel}, TimeoutMillis: 30000, MaxInflight: 1, WorstCaseChargeBytes: 1073741824})
	files["profile-diagnostic.json"] = descriptor.DiagnosticJSON()
	for name, raw := range files {
		mode := os.FileMode(0400)
		if name == "bin/kurnd" || name == "bin/kurn-searchd" {
			mode = 0500
		}
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, raw, 0600); err != nil || os.Chmod(path, mode) != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		m.Recorded.Files[name] = fileIdentity{SHA256: bytesSHA(raw), Mode: fmt.Sprintf("%04o", mode)}
	}
	m.Profile.DiagnosticSHA256 = m.Recorded.Files["profile-diagnostic.json"].SHA256
	m.Recorded.Artifact.ContainerSHA256 = m.Recorded.Files["data/"+listName+".kurn"].SHA256
	m.Reported.ContainerSHA256 = m.Recorded.Artifact.ContainerSHA256
	m.Recorded.Executables["kurnd"] = byteIdentity{SHA256: m.Recorded.Files["bin/kurnd"].SHA256, Bytes: int64(len(files["bin/kurnd"]))}
	m.Recorded.Executables["kurn-searchd"] = byteIdentity{SHA256: m.Recorded.Files["bin/kurn-searchd"].SHA256, Bytes: int64(len(files["bin/kurn-searchd"]))}
	for _, dir := range []string{"bin", "data"} {
		if err := os.Chmod(filepath.Join(root, dir), 0500); err != nil {
			t.Fatal(err)
		}
	}
	writeVerifyManifest(t, root, m)
	registerCapturedTreeCleanup(t, root)
	return root, m, files
}

func writeVerifyManifest(t *testing.T, root string, m manifest) {
	t.Helper()
	raw, err := marshalManifest(m)
	path := filepath.Join(root, "manifest.json")
	if info, statErr := os.Lstat(path); statErr == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || !uidOwned(info) || os.Chmod(path, 0600) != nil {
			t.Fatal("unsafe manifest rewrite")
		}
	}
	if err != nil || os.WriteFile(path, raw, 0400) != nil || os.Chmod(path, 0400) != nil {
		t.Fatal(err)
	}
}

func TestVerifyVersionRefusesWrongFixedFileModes(t *testing.T) {
	for name, want := range immutableFileModes {
		name, want := name, want
		t.Run(name, func(t *testing.T) {
			root, m, _ := newVerifyVersionFixture(t)
			wrong := "0500"
			mode := os.FileMode(0500)
			if want == wrong {
				wrong, mode = "0400", 0400
			}
			if err := os.Chmod(filepath.Join(root, name), mode); err != nil {
				t.Fatal(err)
			}
			id := m.Recorded.Files[name]
			id.Mode = wrong
			m.Recorded.Files[name] = id
			writeVerifyManifest(t, root, m)
			if _, err := VerifyVersion(root); err == nil {
				t.Fatalf("accepted %s mode %s", name, wrong)
			}
		})
	}
}

func TestVerifyVersionRefusesWrongCopiedExecutableSizes(t *testing.T) {
	for _, name := range []string{"kurnd", "kurn-searchd"} {
		t.Run(name, func(t *testing.T) {
			root, m, files := newVerifyVersionFixture(t)
			recorded := m.Recorded.Executables[name]
			recorded.Bytes = int64(len(files["bin/"+name])) + 1
			m.Recorded.Executables[name] = recorded
			writeVerifyManifest(t, root, m)
			if _, err := VerifyVersion(root); err == nil {
				t.Fatalf("accepted wrong copied executable size for %s", name)
			}
		})
	}
}
