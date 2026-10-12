package preparedsetup

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReservationIsAbsentOnlyAndInodeBound(t *testing.T) {
	p := t.TempDir()
	os.Chmod(p, 0700)
	target := filepath.Join(p, "out")
	tmp, owned, e := reserve(target)
	if e != nil {
		t.Fatal(e)
	}
	if _, _, e := reserve(target); e == nil {
		t.Fatal("reused")
	}
	foreign := filepath.Join(p, "foreign")
	os.Rename(tmp, foreign)
	os.Mkdir(tmp, 0700)
	registerCapturedTreeCleanup(t, foreign)
	registerCapturedTreeCleanup(t, tmp)
	if removeOwned(tmp, owned) == nil {
		t.Fatal("foreign removed")
	}
	if _, e := os.Stat(tmp); e != nil {
		t.Fatal(e)
	}
}

func TestOwnedLockedReservationCleanup(t *testing.T) {
	parent := t.TempDir()
	if err := os.Chmod(parent, 0700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(parent, "out")
	root, owned, err := reserve(target)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"bin", "data"} {
		path := filepath.Join(root, name)
		if err := os.Mkdir(path, 0700); err != nil || os.WriteFile(filepath.Join(path, "file"), []byte(name), 0600) != nil || captureOwnedDirectory(root, name, &owned) != nil || os.Chmod(filepath.Join(path, "file"), 0400) != nil || os.Chmod(path, 0500) != nil {
			t.Fatalf("fixture %s: %v", name, err)
		}
	}
	if err := removeOwned(root, owned); err != nil || !absent(root) || !absent(target) {
		t.Fatalf("owned cleanup: %v", err)
	}
}

func TestOwnedCleanupRefusesCapturedChildReplacementAndSymlink(t *testing.T) {
	for _, kind := range []string{"directory", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			parent := t.TempDir()
			if err := os.Chmod(parent, 0700); err != nil {
				t.Fatal(err)
			}
			root, owned, err := reserve(filepath.Join(parent, "out"))
			if err != nil {
				t.Fatal(err)
			}
			child := filepath.Join(root, "bin")
			if err := os.Mkdir(child, 0700); err != nil || captureOwnedDirectory(root, "bin", &owned) != nil || os.Rename(child, child+".original") != nil {
				t.Fatal(err)
			}
			external := filepath.Join(parent, "external")
			if err := os.Mkdir(external, 0700); err != nil || os.WriteFile(filepath.Join(external, "marker"), []byte("foreign"), 0600) != nil {
				t.Fatal(err)
			}
			externalBefore, err := os.Lstat(external)
			if err != nil {
				t.Fatal(err)
			}
			markerBefore, err := os.Lstat(filepath.Join(external, "marker"))
			if err != nil {
				t.Fatal(err)
			}
			if kind == "directory" {
				if err := os.Mkdir(child, 0700); err != nil || os.WriteFile(filepath.Join(child, "marker"), []byte("foreign"), 0400) != nil || os.Chmod(child, 0500) != nil {
					t.Fatal(err)
				}
			} else if err := os.Symlink(external, child); err != nil {
				t.Fatal(err)
			}
			before, _ := os.Lstat(child)
			rootBefore, _ := os.Lstat(root)
			if kind == "directory" {
				cleanupOwnership := reservationOwnership{root: rootBefore, children: map[string]os.FileInfo{"bin": before}}
				t.Cleanup(func() {
					if err := removeOwned(root, cleanupOwnership); err != nil && !absent(root) {
						t.Errorf("captured directory replacement cleanup: %v", err)
					}
				})
			} else {
				symlinkBefore := before
				t.Cleanup(func() {
					current, err := os.Lstat(child)
					if err == nil && current.Mode()&os.ModeSymlink != 0 && os.SameFile(current, symlinkBefore) {
						_ = os.Remove(child)
					}
					if err := removeOwned(root, reservationOwnership{root: rootBefore, children: map[string]os.FileInfo{}}); err != nil && !absent(root) {
						t.Errorf("captured symlink replacement cleanup: %v", err)
					}
				})
			}
			registerCapturedTreeCleanup(t, external)
			if err := removeOwned(root, owned); err == nil {
				t.Fatal("removed replacement child")
			}
			after, err := os.Lstat(child)
			if err != nil || !os.SameFile(before, after) || before.Mode() != after.Mode() {
				t.Fatalf("replacement changed: %v", err)
			}
			markerRoot := child
			if kind == "symlink" {
				markerRoot = external
			}
			if raw, err := os.ReadFile(filepath.Join(markerRoot, "marker")); err != nil || string(raw) != "foreign" {
				t.Fatalf("foreign marker: %q/%v", raw, err)
			}
			externalAfter, externalErr := os.Lstat(external)
			markerAfter, markerErr := os.Lstat(filepath.Join(external, "marker"))
			if externalErr != nil || markerErr != nil || !os.SameFile(externalBefore, externalAfter) || externalBefore.Mode() != externalAfter.Mode() || !os.SameFile(markerBefore, markerAfter) || markerBefore.Mode() != markerAfter.Mode() {
				t.Fatalf("external target changed: external=%v marker=%v", externalErr, markerErr)
			}
		})
	}
}
