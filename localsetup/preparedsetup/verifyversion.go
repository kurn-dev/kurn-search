package preparedsetup

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

type Verification struct {
	ManifestSHA256 string `json:"manifest_sha256"`
	Schema         string `json:"schema"`
	Startable      bool   `json:"startable"`
	Status         string `json:"status"`
	Warning        string `json:"warning,omitempty"`
}

func VerifyVersion(out string) (Verification, error) {
	root, e := os.Lstat(out)
	if e != nil || !root.IsDir() || root.Mode()&os.ModeSymlink != 0 || root.Mode().Perm() != 0700 || !uidOwned(root) {
		return Verification{}, errRefused
	}
	m, raw, e := readManifest(filepath.Join(out, "manifest.json"))
	if e != nil {
		return Verification{}, errRefused
	}
	if validateManifest(m) != nil {
		return Verification{}, errRefused
	}
	expected := map[string]bool{"manifest.json": true, "state": true, "bin": true, "data": true}
	if len(m.Recorded.Files) != len(immutableFileModes) {
		return Verification{}, errRefused
	}
	for name, id := range m.Recorded.Files {
		expectedMode, ok := immutableFileModes[name]
		if !safeRelative(name) || !ok || id.Mode != expectedMode {
			return Verification{}, errRefused
		}
		expected[name] = true
		p := filepath.Join(out, name)
		i, x := os.Lstat(p)
		if x != nil || !i.Mode().IsRegular() || i.Mode()&os.ModeSymlink != 0 || !uidOwned(i) || fmt.Sprintf("%04o", i.Mode().Perm()) != id.Mode {
			return Verification{}, errRefused
		}
		sha, bytes, x := fileSHA(p)
		if x != nil || sha != id.SHA256 {
			return Verification{}, errRefused
		}
		if name == "bin/kurnd" && bytes != m.Recorded.Executables["kurnd"].Bytes || name == "bin/kurn-searchd" && bytes != m.Recorded.Executables["kurn-searchd"].Bytes {
			return Verification{}, errRefused
		}
	}
	if m.Recorded.Files["profile-diagnostic.json"].SHA256 != m.Profile.DiagnosticSHA256 || m.Recorded.Files[m.Recorded.Artifact.Path].SHA256 != m.Recorded.Artifact.ContainerSHA256 || m.Recorded.Files["bin/kurnd"].SHA256 != m.Recorded.Executables["kurnd"].SHA256 || m.Recorded.Files["bin/kurn-searchd"].SHA256 != m.Recorded.Executables["kurn-searchd"].SHA256 {
		return Verification{}, errRefused
	}
	manifestInfo, e := os.Lstat(filepath.Join(out, "manifest.json"))
	if e != nil || !manifestInfo.Mode().IsRegular() || manifestInfo.Mode()&os.ModeSymlink != 0 || manifestInfo.Mode().Perm() != 0400 || !uidOwned(manifestInfo) {
		return Verification{}, errRefused
	}
	for name, mode := range map[string]os.FileMode{"bin": 0500, "data": 0500} {
		i, e := os.Lstat(filepath.Join(out, name))
		if e != nil || !i.IsDir() || i.Mode()&os.ModeSymlink != 0 || i.Mode().Perm() != mode || !uidOwned(i) {
			return Verification{}, errRefused
		}
	}
	var actual []string
	if e := filepath.WalkDir(out, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(out, path)
		if rel != "." {
			actual = append(actual, rel)
		}
		return nil
	}); e != nil {
		return Verification{}, errRefused
	}
	sort.Strings(actual)
	for _, name := range actual {
		if !expected[name] && !strings.HasPrefix(name, "state/") {
			return Verification{}, errRefused
		}
	}
	state := filepath.Join(out, "state")
	si, e := os.Lstat(state)
	if e != nil || !si.IsDir() || si.Mode()&os.ModeSymlink != 0 || si.Mode().Perm() != 0700 || !uidOwned(si) {
		return Verification{}, errRefused
	}
	entries, e := os.ReadDir(state)
	if e != nil {
		return Verification{}, errRefused
	}
	startable := true
	warning := ""
	for _, entry := range entries {
		if entry.Name() != "search-status.json" || entry.Type()&os.ModeSymlink != 0 {
			return Verification{}, errRefused
		}
		i, e := entry.Info()
		if e != nil || !i.Mode().IsRegular() || i.Mode()&os.ModeSymlink != 0 || !uidOwned(i) || i.Mode().Perm() != 0600 || i.Size() > 1<<20 {
			return Verification{}, errRefused
		}
		startable = false
		warning = "status file present: a searchd is running or a previous run left it stale; Load will refuse until it is removed"
	}
	return Verification{ManifestSHA256: bytesSHA(raw), Schema: "kurn-search-prepared-verify-result/v1", Startable: startable, Status: "VERIFIED", Warning: warning}, nil
}

func uidOwned(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(stat.Uid) == os.Getuid()
}
