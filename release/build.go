package release

import (
	"context"
	"debug/buildinfo"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/kurn-dev/kurn-search/internal/processbound"
)

const (
	maxSpecBytes      = 64 << 10
	maxCandidateBytes = 1 << 20
	maxEvidenceBytes  = 1 << 20
	maxBundleFile     = 512 << 20
	requiredGoVersion = "go1.27.0"
)

func build(ctx context.Context, specPath, sourcePath, outputPath string) (Result, error) {
	specAbs, err := cleanAbsolute(specPath)
	if err != nil {
		return Result{}, errRefused
	}
	sourceAbs, err := cleanAbsolute(sourcePath)
	if err != nil {
		return Result{}, errRefused
	}
	outputAbs, parent, err := validateAbsentOutput(outputPath)
	if err != nil || requireResolved(specAbs) != nil || requireResolved(sourceAbs) != nil || sameFile(specAbs, sourceAbs) {
		return Result{}, errRefused
	}
	specRaw, err := readRegular(specAbs, maxSpecBytes)
	if err != nil {
		return Result{}, errRefused
	}
	var spec Spec
	if decodeCanonical(specRaw, maxSpecBytes, &spec) != nil || validateSpec(spec) != nil {
		return Result{}, errRefused
	}
	if requireResolved(spec.Kurn.Path) != nil || sameFile(specAbs, spec.Kurn.Path) || sameFile(sourceAbs, spec.Kurn.Path) || pathOverlap(outputAbs, specAbs) || pathOverlap(outputAbs, sourceAbs) || pathOverlap(outputAbs, spec.Kurn.Path) {
		return Result{}, errRefused
	}
	sourceRaw, err := readRegular(sourceAbs, spec.Snapshot.MaxBytes)
	if err != nil {
		return Result{}, errRefused
	}
	projection, err := projectCatalog(sourceRaw, spec)
	if err != nil {
		return Result{}, errRefused
	}
	kurnIdentity, err := inspectKurn(spec.Kurn.Path)
	if err != nil {
		return Result{}, errRefused
	}

	var predecessor *verifiedCandidate
	if spec.Bundle.Predecessor != nil {
		if pathOverlap(outputAbs, spec.Bundle.Predecessor.Path) || sameFile(specAbs, spec.Bundle.Predecessor.Path) || sameFile(sourceAbs, spec.Bundle.Predecessor.Path) {
			return Result{}, errRefused
		}
		checked, err := verifyRoot(spec.Bundle.Predecessor.Path)
		if err != nil || checked.result.CandidateSHA256 != spec.Bundle.Predecessor.CandidateSHA256 {
			return Result{}, errRefused
		}
		predecessor = &checked
	}

	temporary, err := os.MkdirTemp(parent, "."+filepath.Base(outputAbs)+".tmp-")
	if err != nil {
		return Result{}, errRefused
	}
	published := false
	defer func() {
		if !published {
			_ = os.RemoveAll(temporary)
		}
	}()
	if err := os.Chmod(temporary, 0o700); err != nil {
		return Result{}, errRefused
	}
	kurnSnapshot := filepath.Join(temporary, ".kurn")
	if err := snapshotKurn(spec.Kurn.Path, kurnSnapshot, kurnIdentity); err != nil {
		return Result{}, errRefused
	}
	executionSpec := spec
	executionSpec.Kurn.Path = kurnSnapshot
	privateDir := filepath.Join(temporary, "private")
	if err := os.Mkdir(privateDir, 0o700); err != nil {
		return Result{}, errRefused
	}
	feedPath := filepath.Join(privateDir, "feed.ndjson")
	mappingPath := filepath.Join(privateDir, "mapping.json")
	if err := writePrivate(feedPath, projection.feed); err != nil {
		return Result{}, errRefused
	}
	if err := writePrivate(mappingPath, projection.mapping); err != nil {
		return Result{}, errRefused
	}
	if err := runDryRun(ctx, executionSpec, mappingPath, feedPath, projection); err != nil {
		return Result{}, errRefused
	}
	bundleDir := filepath.Join(temporary, "bundle")
	previousBundle := ""
	if predecessor != nil {
		previousBundle = filepath.Join(temporary, ".predecessor")
		if err := snapshotPredecessor(*predecessor, previousBundle); err != nil {
			return Result{}, errRefused
		}
	}
	buildOutput, err := runKurnBuild(ctx, executionSpec, mappingPath, feedPath, bundleDir, previousBundle)
	if err != nil {
		return Result{}, errRefused
	}
	if err := normalizeBundleModes(bundleDir); err != nil {
		return Result{}, errRefused
	}
	var predecessorLink *PredecessorEvidence
	if predecessor != nil {
		predecessorLink = &PredecessorEvidence{CandidateSHA256: predecessor.result.CandidateSHA256, BaseSHA256: predecessor.candidate.Bundle.BaseSHA256}
	}
	bundle, err := verifyBundle(bundleDir, projection, predecessor, predecessorLink)
	if err != nil || bundle.AnalyzerSHA256 != spec.Bundle.AnalyzerSHA256 || bundle.Mode != spec.Bundle.Mode {
		return Result{}, errRefused
	}
	if verifyKurnBuildOutput(buildOutput, bundleDir, bundle, predecessorLink) != nil {
		return Result{}, errRefused
	}
	if predecessor != nil {
		current, err := verifyRoot(spec.Bundle.Predecessor.Path)
		if err != nil || current.result != predecessor.result || !reflect.DeepEqual(current.candidate, predecessor.candidate) ||
			!reflect.DeepEqual(current.evidence, predecessor.evidence) || os.RemoveAll(previousBundle) != nil {
			return Result{}, errRefused
		}
	}
	currentKurn, err := inspectKurn(spec.Kurn.Path)
	if err != nil || currentKurn != kurnIdentity || os.Remove(kurnSnapshot) != nil {
		return Result{}, errRefused
	}
	profileSHA, err := descriptorIdentity(spec, bundle.ConfigSHA256)
	if err != nil {
		return Result{}, errRefused
	}
	evidence := PrivateEvidence{
		Schema:     EvidenceSchema,
		Snapshot:   SnapshotEvidence{ID: spec.Snapshot.ID, SHA256: projection.snapshotSHA256, Records: projection.entries, Bytes: int64(len(sourceRaw))},
		FeedSHA256: digestBytes(projection.feed), MappingSHA256: digestBytes(projection.mapping),
		PhysicalList: spec.Bundle.PhysicalList, Golden: spec.Golden, Policy: spec.Policy,
	}
	evidence.Predecessor = predecessorLink
	evidenceRaw, err := marshalCanonical(evidence)
	if err != nil || len(evidenceRaw) > maxEvidenceBytes {
		return Result{}, errRefused
	}
	evidencePath := filepath.Join(privateDir, "evidence.json")
	if err := writePrivate(evidencePath, evidenceRaw); err != nil {
		return Result{}, errRefused
	}
	files, err := candidateFiles(temporary, predecessor != nil, digestBytes(evidenceRaw))
	if err != nil {
		return Result{}, errRefused
	}
	relation := "not-equal"
	if bundle.BaseSHA256 == S1InitialBaseSHA256 {
		relation = "equal"
	}
	candidate := Candidate{
		Schema: CandidateSchema, State: CandidateState, Adapter: spec.Adapter,
		Kurn: kurnIdentity, Bundle: bundle, ProfileSHA256: profileSHA, S1Relation: relation,
		Identities: identityFrame(spec, bundle, profileSHA), Files: files,
	}
	candidateRaw, err := marshalCanonical(candidate)
	if err != nil || len(candidateRaw) > maxCandidateBytes {
		return Result{}, errRefused
	}
	if err := writePrivate(filepath.Join(temporary, "candidate.json"), candidateRaw); err != nil {
		return Result{}, errRefused
	}
	checked, err := verifyRoot(temporary)
	if err != nil || checked.result.CandidateSHA256 != digestBytes(candidateRaw) {
		return Result{}, errRefused
	}
	if err := ctx.Err(); err != nil {
		return Result{}, errRefused
	}
	if _, err := os.Lstat(outputAbs); !errors.Is(err, os.ErrNotExist) {
		return Result{}, errRefused
	}
	if err := os.Rename(temporary, outputAbs); err != nil {
		return Result{}, errRefused
	}
	published = true
	return checked.result, nil
}

func snapshotKurn(source, destination string, expected CandidateKurn) error {
	raw, err := readRegular(source, maxBundleFile)
	if err != nil || writePrivate(destination, raw) != nil || os.Chmod(destination, 0o700) != nil {
		return errRefused
	}
	actual, err := inspectKurn(destination)
	if err != nil || actual != expected {
		return errRefused
	}
	return nil
}

func validateAbsentOutput(path string) (string, string, error) {
	absolute, err := cleanAbsolute(path)
	if err != nil || absolute == string(filepath.Separator) {
		return "", "", errRefused
	}
	if _, err := os.Lstat(absolute); !errors.Is(err, os.ErrNotExist) {
		return "", "", errRefused
	}
	parent := filepath.Dir(absolute)
	info, err := os.Lstat(parent)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", "", errRefused
	}
	resolved, err := filepath.EvalSymlinks(parent)
	if err != nil || resolved != parent {
		return "", "", errRefused
	}
	return absolute, parent, nil
}

func pathOverlap(left, right string) bool {
	a, errA := cleanAbsolute(left)
	b, errB := cleanAbsolute(right)
	if errA != nil || errB != nil {
		return true
	}
	return a == b || strings.HasPrefix(a, b+string(filepath.Separator)) || strings.HasPrefix(b, a+string(filepath.Separator))
}

func inspectKurn(path string) (CandidateKurn, error) {
	sha, err := digestFile(path, maxBundleFile)
	if err != nil || sha != KurnBinarySHA256 {
		return CandidateKurn{}, errRefused
	}
	info, err := buildinfo.ReadFile(path)
	if err != nil {
		return CandidateKurn{}, errRefused
	}
	settings := map[string]string{}
	for _, setting := range info.Settings {
		settings[setting.Key] = setting.Value
	}
	if settings["vcs.revision"] != KurnSourceRevision || settings["vcs.modified"] != "false" ||
		info.GoVersion != requiredGoVersion || info.Path != "github.com/kurn-dev/kurn/cmd/kurn" || info.Main.Path != "github.com/kurn-dev/kurn" {
		return CandidateKurn{}, errRefused
	}
	return CandidateKurn{
		SHA256: sha, SourceRevision: KurnSourceRevision, CompatibilityLabel: KurnCompatibilityLabel,
		GoVersion: info.GoVersion, ModulePath: info.Main.Path, CommandPath: info.Path,
	}, nil
}

func runDryRun(parent context.Context, spec Spec, mappingPath, feedPath string, projection projectionResult) error {
	output, err := runBounded(parent, time.Duration(spec.Kurn.DeadlineMillis)*time.Millisecond, filepath.Dir(mappingPath), spec.Kurn.Path,
		"ingest", "-dry-run", "-mapping", mappingPath, "-in", feedPath,
		"-n", fmt.Sprint(projection.entries), "-samples", "0", "-skip-bad", "0", "-max-empty-rate", "0", "-json")
	if err != nil {
		return errRefused
	}
	return verifyDryRunOutput(output, projection)
}

func verifyDryRunOutput(output []byte, projection projectionResult) error {
	var report struct {
		Sampled        int               `json:"sampled"`
		Records        int               `json:"records"`
		Filtered       int               `json:"filtered"`
		Bad            int               `json:"bad"`
		DistinctIDs    int               `json:"distinct_ids"`
		DuplicateIDs   int               `json:"duplicate_ids"`
		RawKeys        int               `json:"raw_keys"`
		AnalyzedKeys   int               `json:"analyzed_keys"`
		EmptyKeys      int               `json:"empty_keys"`
		KeylessEntries int               `json:"keyless_entries"`
		EmptyKeyRate   float64           `json:"empty_key_rate"`
		EstMemoryBytes int64             `json:"est_memory_bytes"`
		Samples        []json.RawMessage `json:"samples"`
	}
	if decodeStrict(output, MaxProcessOutput, &report) != nil || report.Sampled != projection.entries ||
		report.Records != projection.entries || report.Filtered != 0 || report.Bad != 0 ||
		report.DistinctIDs != projection.entries || report.DuplicateIDs != 0 ||
		report.RawKeys != projection.keys || report.AnalyzedKeys != projection.keys || report.EmptyKeys != 0 ||
		report.KeylessEntries != 0 || report.EmptyKeyRate != 0 || report.EstMemoryBytes != int64(projection.keys)*133 || len(report.Samples) != 0 {
		return errRefused
	}
	return nil
}

func runKurnBuild(parent context.Context, spec Spec, mappingPath, feedPath, outputPath, previousPath string) ([]byte, error) {
	args := []string{"build", "-mapping", mappingPath, "-in", feedPath, "-out", outputPath, "-skip-bad", "0"}
	if previousPath != "" {
		args = append(args, "-prev", previousPath)
	}
	return runBounded(parent, time.Duration(spec.Kurn.DeadlineMillis)*time.Millisecond, filepath.Dir(mappingPath), spec.Kurn.Path, args...)
}

func snapshotPredecessor(source verifiedCandidate, destination string) error {
	if err := os.Mkdir(destination, 0o700); err != nil {
		return errRefused
	}
	copied := 0
	for _, item := range source.candidate.Files {
		if item.Class != "bundle" {
			continue
		}
		if !strings.HasPrefix(item.Path, "bundle/") || !validSHA256(item.SHA256) {
			return errRefused
		}
		name := strings.TrimPrefix(item.Path, "bundle/")
		if name == "" || filepath.Base(name) != name {
			return errRefused
		}
		raw, err := readRegular(filepath.Join(source.root, filepath.FromSlash(item.Path)), maxBundleFile)
		if err != nil || digestBytes(raw) != item.SHA256 || writePrivate(filepath.Join(destination, name), raw) != nil {
			return errRefused
		}
		copied++
	}
	want := 4
	if source.evidence.Predecessor != nil {
		want = 5
	}
	if copied != want {
		return errRefused
	}
	bundle, err := verifyBundle(destination, source.projection, nil, source.evidence.Predecessor)
	if err != nil || !reflect.DeepEqual(bundle, source.candidate.Bundle) {
		return errRefused
	}
	return nil
}

func verifyKurnBuildOutput(raw []byte, outputPath string, bundle BundleIdentity, predecessor *PredecessorEvidence) error {
	if len(raw) == 0 || raw[len(raw)-1] != '\n' {
		return errRefused
	}
	lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	want := []string{fmt.Sprintf("bundle %s: %d entries, %d keys, base %s (mode %s)", outputPath, bundle.Entries, bundle.Keys, bundle.BaseSHA256[:12], bundle.Mode)}
	if predecessor != nil {
		want = append(want, fmt.Sprintf("delta vs %s: +%d ~%d -%d", predecessor.BaseSHA256[:12], bundle.DeltaAdds, bundle.DeltaUpdates, bundle.DeltaDeletes))
	}
	if !reflect.DeepEqual(lines, want) {
		return errRefused
	}
	return nil
}

func runBounded(parent context.Context, timeout time.Duration, directory, path string, args ...string) ([]byte, error) {
	output, err := processbound.Run(parent, timeout, directory, path, MaxProcessOutput, args...)
	if err != nil {
		return nil, errRefused
	}
	return output, nil
}

func writePrivate(path string, raw []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return errRefused
	}
	ok := false
	defer func() {
		file.Close()
		if !ok {
			_ = os.Remove(path)
		}
	}()
	if _, err := file.Write(raw); err != nil || file.Sync() != nil || file.Close() != nil || os.Chmod(path, 0o600) != nil {
		return errRefused
	}
	ok = true
	return nil
}

func normalizeBundleModes(root string) error {
	if err := os.Chmod(root, 0o700); err != nil {
		return errRefused
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return errRefused
	}
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() {
			return errRefused
		}
		if err := os.Chmod(filepath.Join(root, entry.Name()), 0o600); err != nil {
			return errRefused
		}
	}
	return nil
}

func candidateFiles(root string, delta bool, evidenceSHA string) ([]CandidateFile, error) {
	names := []string{"bundle/base.idx", "bundle/base.jsonl", "bundle/config.json", "bundle/manifest.json"}
	if delta {
		names = append(names, "bundle/delta.jsonl")
	}
	sort.Strings(names)
	files := make([]CandidateFile, 0, len(names)+3)
	for _, name := range names {
		sha, err := digestFile(filepath.Join(root, filepath.FromSlash(name)), maxBundleFile)
		if err != nil {
			return nil, errRefused
		}
		files = append(files, CandidateFile{Path: name, Class: "bundle", SHA256: sha})
	}
	files = append(files,
		CandidateFile{Path: "private/evidence.json", Class: "private-evidence-seal", SHA256: evidenceSHA},
		CandidateFile{Path: "private/feed.ndjson", Class: "private-material"},
		CandidateFile{Path: "private/mapping.json", Class: "private-material"},
	)
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}

func identityFrame(spec Spec, bundle BundleIdentity, profileSHA string) []Identity {
	return []Identity{
		{Role: "behavioral", Name: "adapter-behavior", SHA256: spec.Adapter.BehaviorSHA256, Producer: "admitted-adapter-driver", Verifier: "release-descriptor-reconstruction", Exposure: "path-neutral"},
		{Role: "operational-compatibility", Name: "adapter-profile", SHA256: profileSHA, Producer: "release-descriptor-reconstruction", Verifier: "candidate-verify", Exposure: "path-neutral-digest-only"},
		{Role: "artifact-integrity", Name: "kurn-bundle", SHA256: bundle.BaseSHA256, Producer: "pinned-kurn-build", Verifier: "independent-bundle-verifier", Exposure: "path-neutral"},
		{Role: "artifact-authenticity", Name: "release-authenticity", Status: "ABSENT-REQUIRES-W5-S3", Producer: "none", Verifier: "candidate-state", Exposure: "path-neutral-status-only"},
		{Role: "deployed-state", Name: "deployment-generation", Status: "ABSENT", Producer: "none", Verifier: "candidate-state", Exposure: "path-neutral-status-only"},
		{Role: "served-query", Name: "served-stamp", Status: "ABSENT", Producer: "none", Verifier: "candidate-state", Exposure: "path-neutral-status-only"},
	}
}
