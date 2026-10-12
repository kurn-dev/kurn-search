package preparedsetup

import (
	"io"
	"os"
	"path/filepath"
	"syscall"
)

const reservationSuffix = ".prepared-setup"

type reservationOwnership struct {
	root     os.FileInfo
	children map[string]os.FileInfo
}

type cleanupOperations struct {
	chmod     func(*os.File, os.FileMode) error
	lstat     func(string) (os.FileInfo, error)
	removeAll func(string) error
}

func productionCleanupOperations() cleanupOperations {
	return cleanupOperations{
		chmod:     func(file *os.File, mode os.FileMode) error { return file.Chmod(mode) },
		lstat:     os.Lstat,
		removeAll: os.RemoveAll,
	}
}

func physicalPrivateParent(path string) bool {
	i, e := os.Lstat(path)
	if e != nil || !i.IsDir() || i.Mode()&os.ModeSymlink != 0 || i.Mode().Perm() != 0700 {
		return false
	}
	if s, ok := i.Sys().(*syscall.Stat_t); ok && int(s.Uid) != os.Getuid() {
		return false
	}
	return true
}
func absent(path string) bool { _, e := os.Lstat(path); return os.IsNotExist(e) }

func reserve(target string) (string, reservationOwnership, error) {
	if !filepath.IsAbs(target) || !physicalPrivateParent(filepath.Dir(target)) || !absent(target) {
		return "", reservationOwnership{}, errRefused
	}
	tmp := filepath.Join(filepath.Dir(target), "."+filepath.Base(target)+reservationSuffix)
	if !absent(tmp) || os.Mkdir(tmp, 0700) != nil {
		return "", reservationOwnership{}, errRefused
	}
	i, e := os.Lstat(tmp)
	if e != nil {
		return "", reservationOwnership{}, errRefused
	}
	return tmp, reservationOwnership{root: i, children: map[string]os.FileInfo{}}, nil
}

func captureOwnedDirectory(root, name string, owned *reservationOwnership) error {
	if owned == nil || name == "" || filepath.Base(name) != name {
		return errRefused
	}
	i, err := os.Lstat(filepath.Join(root, name))
	if err != nil || !i.IsDir() || i.Mode()&os.ModeSymlink != 0 || !uidOwned(i) {
		return errRefused
	}
	owned.children[name] = i
	return nil
}

func openOwnedDirectory(path string, captured os.FileInfo) (*os.File, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, errRefused
	}
	f := os.NewFile(uintptr(fd), path)
	current, err := f.Stat()
	if err != nil || !current.IsDir() || !uidOwned(current) || !os.SameFile(current, captured) {
		_ = f.Close()
		return nil, errRefused
	}
	return f, nil
}

func removeOwned(path string, owned reservationOwnership) error {
	return removeOwnedWith(path, owned, productionCleanupOperations())
}

func removeOwnedWith(path string, owned reservationOwnership, operations cleanupOperations) error {
	i, e := operations.lstat(path)
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil || !i.IsDir() || i.Mode()&os.ModeSymlink != 0 || !uidOwned(i) || owned.root == nil || !os.SameFile(i, owned.root) {
		return errRefused
	}
	root, e := openOwnedDirectory(path, owned.root)
	if e != nil {
		return errRefused
	}
	defer root.Close()
	for name, captured := range owned.children {
		childPath := filepath.Join(path, name)
		current, err := operations.lstat(childPath)
		if err != nil || !current.IsDir() || current.Mode()&os.ModeSymlink != 0 || !uidOwned(current) || !os.SameFile(current, captured) {
			return errRefused
		}
		child, err := openOwnedDirectory(childPath, captured)
		if err != nil {
			return errRefused
		}
		if err := operations.chmod(child, 0700); err != nil {
			_ = child.Close()
			return errRefused
		}
		if err := child.Close(); err != nil {
			return errRefused
		}
	}
	for name, captured := range owned.children {
		current, err := operations.lstat(filepath.Join(path, name))
		if err != nil || !current.IsDir() || current.Mode()&os.ModeSymlink != 0 || !uidOwned(current) || !os.SameFile(current, captured) {
			return errRefused
		}
	}
	current, e := operations.lstat(path)
	if e != nil || !current.IsDir() || current.Mode()&os.ModeSymlink != 0 || !uidOwned(current) || !os.SameFile(current, owned.root) {
		return errRefused
	}
	if operations.removeAll(path) != nil {
		return errRefused
	}
	return nil
}
func writeFile(path string, raw []byte, mode os.FileMode) error {
	f, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return errRefused
	}
	ok := false
	defer func() {
		if !ok {
			_ = f.Close()
		}
	}()
	if n, e := f.Write(raw); e != nil || n != len(raw) || f.Sync() != nil || f.Chmod(mode) != nil || f.Close() != nil {
		return errRefused
	}
	ok = true
	return nil
}
func copyFile(dst, src string, mode os.FileMode) error {
	in, e := os.Open(src)
	if e != nil {
		return errRefused
	}
	defer in.Close()
	out, e := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return errRefused
	}
	if _, e = io.Copy(out, in); e != nil || out.Sync() != nil || out.Chmod(mode) != nil || out.Close() != nil {
		return errRefused
	}
	return nil
}
func syncDir(path string) error {
	f, e := os.Open(path)
	if e != nil {
		return errRefused
	}
	defer f.Close()
	if f.Sync() != nil {
		return errRefused
	}
	return nil
}

func syncFile(path string) error {
	f, e := os.Open(path)
	if e != nil {
		return errRefused
	}
	defer f.Close()
	if f.Sync() != nil {
		return errRefused
	}
	return nil
}

func publish(staged, target string) error {
	return publishWith(staged, target, os.Rename, syncDir)
}

func publishWith(staged, target string, rename func(string, string) error, syncDirectory func(string) error) error {
	if !absent(target) || rename(staged, target) != nil {
		return errRefused
	}
	return syncDirectory(filepath.Dir(target))
}
