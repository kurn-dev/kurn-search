package localsetup

import (
	"bytes"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/kurn-dev/kurn-search/internal/localproof"
)

// Prepare requires an exclusively owned, quiescent destination parent.
// The fixed sibling reservation excludes cooperating Prepare invocations.
// Ordinary os.Rename is NOT an OS-enforced no-replace primitive against
// arbitrary noncooperating same-user filesystem mutation.
func Prepare(options Options) (Result, error) {
	return prepare(options, nil)
}

// Instance-local controlled interleavings, not a public concurrency API.
type prepareHooks struct {
	afterReserve  func() error
	beforePublish func() error
}

func prepare(options Options, hooks *prepareHooks) (Result, error) {
	refuse := func() (Result, error) { return Result{}, errRefused }
	recipe, known := recipeForName(options.Recipe)
	if !known || !validateOptions(options) {
		return refuse()
	}
	bundle, err := readBundle(options.Bundle, recipe)
	if err != nil {
		return refuse()
	}
	temporary := filepath.Join(filepath.Dir(options.Out), "."+filepath.Base(options.Out)+reservationSuffix)
	if os.Mkdir(temporary, 0o700) != nil {
		return refuse()
	}
	owned, err := os.Lstat(temporary)
	if err != nil {
		return refuse()
	}
	published, success := false, false
	defer func() {
		if success {
			return
		}
		path := temporary
		if published {
			path = options.Out
		}
		removeOwned(path, owned)
	}()
	if os.Chmod(temporary, 0o700) != nil || !absent(options.Out) {
		return refuse()
	}
	if hooks != nil && hooks.afterReserve != nil && hooks.afterReserve() != nil {
		return refuse()
	}
	if !absent(options.Out) {
		return refuse()
	}
	data := filepath.Join(temporary, "data")
	list := filepath.Join(data, recipe.physicalListName)
	for _, dir := range []string{data, list} {
		if os.Mkdir(dir, 0o700) != nil || os.Chmod(dir, 0o700) != nil {
			return refuse()
		}
	}
	files := recipe.fixtureFiles()
	for name, raw := range bundle {
		files[filepath.Join("data", recipe.physicalListName, name)] = raw
	}
	staged, final := configuration(temporary, options, recipe), configuration(options.Out, options, recipe)
	if !sameNonPathFields(staged, final) {
		return refuse()
	}
	stagedRaw, err := configurationBytes(staged)
	if err != nil {
		return refuse()
	}
	files["kurn-searchd.json"] = stagedRaw
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if writePrivate(filepath.Join(temporary, name), files[name]) != nil {
			return refuse()
		}
	}
	if _, err := localproof.Load(filepath.Join(temporary, "kurn-searchd.json")); err != nil {
		return refuse()
	}
	finalRaw, err := configurationBytes(final)
	if err != nil {
		return refuse()
	}
	// Only our reserved private staging file is replaced; never user input.
	finalStagePath := filepath.Join(temporary, ".final-config")
	if writePrivate(finalStagePath, finalRaw) != nil ||
		os.Rename(finalStagePath, filepath.Join(temporary, "kurn-searchd.json")) != nil {
		return refuse()
	}
	for _, dir := range []string{list, data, temporary} {
		if syncDirectory(dir) != nil {
			return refuse()
		}
	}
	if hooks != nil && hooks.beforePublish != nil && hooks.beforePublish() != nil {
		return refuse()
	}
	if !physicalDirectory(filepath.Dir(options.Out), true) || !absent(options.Out) ||
		os.Rename(temporary, options.Out) != nil {
		return refuse()
	}
	published = true
	if _, err := localproof.Load(filepath.Join(options.Out, "kurn-searchd.json")); err != nil {
		return refuse()
	}
	files["kurn-searchd.json"] = finalRaw
	fileCount, dirCount, err := checkInventory(options.Out, files, recipe)
	if err != nil || syncDirectory(filepath.Dir(options.Out)) != nil {
		return refuse()
	}
	success = true
	return Result{ConfigSHA256: recipe.configSHA256, Directories: dirCount, Files: fileCount,
		PhysicalListName: recipe.physicalListName, Schema: ResultSchema, Status: "PREPARED"}, nil
}

func removeOwned(path string, owned os.FileInfo) {
	current, err := os.Lstat(path)
	if err == nil && current.IsDir() && os.SameFile(current, owned) {
		_ = os.RemoveAll(path)
	}
}

func writePrivate(path string, raw []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return errRefused
	}
	defer file.Close()
	if file.Chmod(0o600) != nil {
		return errRefused
	}
	if n, err := file.Write(raw); err != nil || n != len(raw) || file.Sync() != nil {
		return errRefused
	}
	if file.Close() != nil {
		return errRefused
	}
	return nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return errRefused
	}
	defer directory.Close()
	if directory.Sync() != nil {
		return errRefused
	}
	return directory.Close()
}

func checkInventory(root string, expected map[string][]byte, recipe recipe) (int, int, error) {
	files, directories := 0, 0
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return errRefused
		}
		info, err := entry.Info()
		if err != nil {
			return errRefused
		}
		name, err := filepath.Rel(root, path)
		if err != nil {
			return errRefused
		}
		if entry.IsDir() {
			if info.Mode().Perm() != 0o700 {
				return errRefused
			}
			if name == "." {
				return nil
			}
			if name != "data" && name != filepath.Join("data", recipe.physicalListName) {
				return errRefused
			}
			directories++
			return nil
		}
		wanted, ok := expected[name]
		if !ok || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
			return errRefused
		}
		raw, err := boundedFile(path, maxDataBytes)
		if err != nil || !bytes.Equal(raw, wanted) {
			return errRefused
		}
		files++
		return nil
	})
	if err != nil || files != len(expected) || directories != 2 {
		return 0, 0, errRefused
	}
	return files, directories, nil
}

// Run is the exact CLI protocol. Four required flag/value pairs and optional
// -recipe are admitted;
// duplicate flags, equals-form flags, environment and stdin defaults are absent.
func Run(args []string, stdout, stderr io.Writer) int {
	refuse := func() int {
		if stderr != nil {
			_, _ = io.WriteString(stderr, "kurn-search-local: refused\n")
		}
		return 1
	}
	if stdout == nil || stderr == nil || (len(args) != 9 && len(args) != 11) || args[0] != "prepare" {
		return refuse()
	}
	values := map[string]string{}
	for index := 1; index < len(args); index += 2 {
		name, value := args[index], args[index+1]
		switch name {
		case "-bundle", "-out", "-kurnd-addr", "-searchd-addr", "-recipe":
		default:
			return refuse()
		}
		if value == "" {
			return refuse()
		}
		if _, exists := values[name]; exists {
			return refuse()
		}
		values[name] = value
	}
	for _, required := range []string{"-bundle", "-out", "-kurnd-addr", "-searchd-addr"} {
		if values[required] == "" {
			return refuse()
		}
	}
	result, err := Prepare(Options{Bundle: values["-bundle"], Out: values["-out"],
		KurndAddress: values["-kurnd-addr"], SearchdAddress: values["-searchd-addr"], Recipe: values["-recipe"]})
	if err != nil {
		return refuse()
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return refuse()
	}
	raw = append(raw, '\n')
	n, err := io.Copy(stdout, bytes.NewReader(raw))
	if err != nil || n != int64(len(raw)) {
		return refuse()
	}
	return 0
}
