package fleet

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"sort"

	"github.com/kurn-dev/kurn-search/lifecycle"
)

func Recover(specPath, stateDir, out string) (Result, error) {
	specRaw, err := readRegular(specPath, MaxControlBytes, nil)
	if err != nil {
		return Result{}, errRefused
	}
	var spec RecoverySpec
	if decode(specRaw, MaxControlBytes, &spec) != nil || validateRecoverySpec(spec) != nil {
		return Result{}, errRefused
	}
	checked, err := loadStateDir(stateDir)
	if err != nil || digest(checked.stateRaw) != spec.ExpectedStateSHA256 ||
		spec.RecoveryGeneration <= checked.state.Generation || overlap(out, stateDir) || overlap(out, checked.plan.RegistryRoot) || !cleanAbsolute(out) {
		return Result{}, errRefused
	}
	existing, err := recoveryOutputState(out)
	if err != nil {
		return Result{}, errRefused
	}
	for _, node := range checked.plan.Nodes {
		if overlap(out, node.Root) {
			return Result{}, errRefused
		}
	}
	recoveryActor, err := roleActor(checked.plan.Roles, "recovery-operator")
	if err != nil || recoveryActor != spec.RecoveryOperatorActorID {
		return Result{}, errRefused
	}
	reviewer, err := roleActor(checked.plan.Roles, "evidence-reviewer")
	if err != nil || reviewer != spec.EvidenceReviewerActorID || reviewer == recoveryActor {
		return Result{}, errRefused
	}
	if (spec.Mode == "RESTORE" && spec.TargetArtifactID != checked.state.ArtifactID) ||
		(spec.Mode == "ROLLBACK" && (spec.TargetArtifactID == "" || spec.TargetArtifactID != checked.state.PredecessorArtifactID)) {
		return Result{}, errRefused
	}
	registered, err := lifecycle.VerifyRegistry(checked.plan.RegistryRoot, spec.TargetArtifactID)
	if err != nil || registered.ArtifactID != spec.TargetArtifactID {
		return Result{}, errRefused
	}
	targetPlan := checked.plan
	targetPlan.ArtifactID = spec.TargetArtifactID
	targetBundle, err := registryBundleExpectation(checked.plan.RegistryRoot, spec.TargetArtifactID)
	if err != nil {
		return Result{}, errRefused
	}
	targetPlan.Bundle = targetBundle
	targetSpec := specFromPlan(targetPlan)
	targetEvidence, err := deriveEvidence(targetSpec)
	if err != nil || targetEvidence.ServedStampSHA256 != spec.ExpectedServedStampSHA256 {
		return Result{}, errRefused
	}
	targetPlan.Evidence = targetEvidence
	targetFiles, targetBundleSHA, err := readArtifactBundle(targetPlan)
	if err != nil {
		return Result{}, errRefused
	}
	temporary := filepath.Join(filepath.Dir(out), "."+filepath.Base(out)+".s4-recovery")
	if _, err := os.Lstat(temporary); !errors.Is(err, os.ErrNotExist) || os.Mkdir(temporary, 0o700) != nil {
		return Result{}, errRefused
	}
	ok := false
	defer func() {
		if !ok {
			_ = os.RemoveAll(temporary)
		}
	}()
	for name, raw := range map[string][]byte{
		"audit.json": checked.auditRaw, "observation.json": checked.observationRaw,
		"plan.json": checked.planRaw, "state.json": checked.stateRaw,
	} {
		if writePrivate(filepath.Join(temporary, name), raw) != nil {
			return Result{}, errRefused
		}
	}
	artifactFiles, err := backupArtifact(temporary, checked.plan.RegistryRoot, spec.TargetArtifactID, registered.RegistrySHA256)
	if err != nil {
		return Result{}, errRefused
	}
	nodesRoot := filepath.Join(temporary, "nodes")
	if os.Mkdir(nodesRoot, 0o700) != nil {
		return Result{}, errRefused
	}
	files := make([]BackupFile, 0, 4+len(artifactFiles)+len(checked.state.Nodes)*3)
	for _, item := range []struct {
		name string
		raw  []byte
	}{{"audit.json", checked.auditRaw}, {"observation.json", checked.observationRaw}, {"plan.json", checked.planRaw}, {"state.json", checked.stateRaw}} {
		files = append(files, BackupFile{Mode: 0o600, Path: item.name, SHA256: digest(item.raw)})
	}
	files = append(files, artifactFiles...)
	for _, node := range checked.state.Nodes {
		nodeOut := filepath.Join(nodesRoot, node.ID)
		if _, err := stageNode(nodeOut, targetFiles, targetBundleSHA); err != nil {
			return Result{}, errRefused
		}
		for _, name := range []string{"config.json", "base.jsonl", "base.idx"} {
			raw, err := readRegular(filepath.Join(nodeOut, name), MaxBundleBytes, modePointer(0o600))
			if err != nil {
				return Result{}, errRefused
			}
			files = append(files, BackupFile{Mode: 0o600, Path: filepath.ToSlash(filepath.Join("nodes", node.ID, name)), SHA256: digest(raw)})
		}
		actual, err := verifyNodeRoot(nodeOut)
		if err != nil || actual != targetBundleSHA {
			return Result{}, errRefused
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	backupSHA, err := domainDigest("kurn-search-fleet-backup/v1", files)
	if err != nil {
		return Result{}, errRefused
	}
	status := "RECOVERED"
	if spec.Mode == "ROLLBACK" {
		status = "ROLLED-BACK"
	}
	recovery := Recovery{BackupSHA256: backupSHA, Files: files, Generation: spec.RecoveryGeneration,
		RecoveredNodes: len(checked.state.Nodes), Schema: RecoverySchema, ServedStampSHA256: targetEvidence.ServedStampSHA256,
		StateSHA256: digest(checked.stateRaw), Status: status, StepCount: 4 + len(files), StepDeadlineMillis: spec.StepDeadlineMillis}
	recoveryRaw, err := canonical(recovery)
	if err != nil || writePrivate(filepath.Join(temporary, "recovery.json"), recoveryRaw) != nil {
		return Result{}, errRefused
	}
	if existing {
		if !equalPrivateTrees(temporary, out) || os.RemoveAll(temporary) != nil {
			return Result{}, errRefused
		}
		ok = true
		return Result{BackupSHA256: backupSHA, Decision: status, FleetID: checked.state.FleetID, Generation: spec.RecoveryGeneration,
			Noop: true, PlanSHA256: checked.state.PlanSHA256, RecoveredNodes: len(checked.state.Nodes), Schema: ResultSchema,
			ServedStampSHA256: targetEvidence.ServedStampSHA256, Status: status}, nil
	}
	directory, err := os.Open(temporary)
	if err != nil || directory.Sync() != nil || directory.Close() != nil || os.Rename(temporary, out) != nil {
		return Result{}, errRefused
	}
	ok = true
	return Result{BackupSHA256: backupSHA, Decision: status, FleetID: checked.state.FleetID, Generation: spec.RecoveryGeneration,
		PlanSHA256: checked.state.PlanSHA256, RecoveredNodes: len(checked.state.Nodes), Schema: ResultSchema,
		ServedStampSHA256: targetEvidence.ServedStampSHA256, Status: status}, nil
}

func recoveryOutputState(path string) (bool, error) {
	parent := filepath.Dir(path)
	resolved, err := filepath.EvalSymlinks(parent)
	if err != nil || resolved != parent {
		return false, errRefused
	}
	info, err := os.Lstat(parent)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return false, errRefused
	}
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if _, err := resolvedPrivateDirectory(path); err != nil {
		return false, errRefused
	}
	return true, nil
}

func equalPrivateTrees(left, right string) bool {
	leftInfo, leftErr := os.Lstat(left)
	rightInfo, rightErr := os.Lstat(right)
	if leftErr != nil || rightErr != nil || !leftInfo.IsDir() || !rightInfo.IsDir() ||
		leftInfo.Mode().Perm() != 0o700 || rightInfo.Mode().Perm() != 0o700 {
		return false
	}
	leftEntries, leftErr := os.ReadDir(left)
	rightEntries, rightErr := os.ReadDir(right)
	if leftErr != nil || rightErr != nil || len(leftEntries) != len(rightEntries) {
		return false
	}
	for index, leftEntry := range leftEntries {
		rightEntry := rightEntries[index]
		if leftEntry.Name() != rightEntry.Name() || leftEntry.Type()&os.ModeSymlink != 0 || rightEntry.Type()&os.ModeSymlink != 0 {
			return false
		}
		leftPath, rightPath := filepath.Join(left, leftEntry.Name()), filepath.Join(right, rightEntry.Name())
		if leftEntry.IsDir() || rightEntry.IsDir() {
			if !leftEntry.IsDir() || !rightEntry.IsDir() || !equalPrivateTrees(leftPath, rightPath) {
				return false
			}
			continue
		}
		leftRaw, leftErr := readRegular(leftPath, MaxBundleBytes, modePointer(0o600))
		rightRaw, rightErr := readRegular(rightPath, MaxBundleBytes, modePointer(0o600))
		if leftErr != nil || rightErr != nil || !bytes.Equal(leftRaw, rightRaw) {
			return false
		}
	}
	return true
}

// backupArtifact copies the complete already-verified S3 artifact inventory,
// including its envelope, receipt, record, manifest, and optional delta.  It
// deliberately copies no registry routes and never treats the copy as a new
// registration or authentication event.
func backupArtifact(out, registryRoot, artifactID, expectedRegistrySHA string) ([]BackupFile, error) {
	source := filepath.Join(registryRoot, "artifacts", artifactID)
	recordRaw, err := readRegular(filepath.Join(source, "record.json"), MaxControlBytes, modePointer(0o600))
	if err != nil {
		return nil, errRefused
	}
	var record lifecycle.RegistryRecord
	if decode(recordRaw, MaxControlBytes, &record) != nil || record.ArtifactID != artifactID || len(record.BundleInventory) < 4 || len(record.BundleInventory) > 5 {
		return nil, errRefused
	}
	artifactParent := filepath.Join(out, "artifact")
	artifactOut := filepath.Join(artifactParent, artifactID)
	bundleOut := filepath.Join(artifactOut, "bundle")
	if os.Mkdir(artifactParent, 0o700) != nil || os.Mkdir(artifactOut, 0o700) != nil || os.Mkdir(bundleOut, 0o700) != nil {
		return nil, errRefused
	}
	type copyFile struct {
		path string
		raw  []byte
		sha  string
	}
	values := make([]copyFile, 0, 3+len(record.BundleInventory))
	for _, name := range []string{"envelope.json", "receipt.json", "record.json"} {
		raw := recordRaw
		if name != "record.json" {
			raw, err = readRegular(filepath.Join(source, name), MaxControlBytes, modePointer(0o600))
			if err != nil {
				return nil, errRefused
			}
		}
		sha := digest(raw)
		if (name == "envelope.json" && sha != record.EnvelopeSHA256) || (name == "receipt.json" && sha != record.ReceiptSHA256) {
			return nil, errRefused
		}
		values = append(values, copyFile{path: name, raw: raw, sha: sha})
	}
	previous := ""
	for _, item := range record.BundleInventory {
		if item.Path <= previous || !validSHA(item.SHA256) || filepath.Dir(item.Path) != "bundle" || filepath.Base(item.Path) == "." {
			return nil, errRefused
		}
		raw, readErr := readRegular(filepath.Join(source, filepath.FromSlash(item.Path)), MaxBundleBytes, modePointer(0o600))
		if readErr != nil || digest(raw) != item.SHA256 {
			return nil, errRefused
		}
		values = append(values, copyFile{path: item.Path, raw: raw, sha: item.SHA256})
		previous = item.Path
	}
	files := make([]BackupFile, 0, len(values))
	for _, value := range values {
		destination := filepath.Join(artifactOut, filepath.FromSlash(value.path))
		if writePrivate(destination, value.raw) != nil {
			return nil, errRefused
		}
		files = append(files, BackupFile{Mode: 0o600, Path: filepath.ToSlash(filepath.Join("artifact", artifactID, value.path)), SHA256: value.sha})
	}
	verified, err := lifecycle.VerifyRegistry(registryRoot, artifactID)
	if err != nil || verified.RegistrySHA256 != expectedRegistrySHA {
		return nil, errRefused
	}
	return files, nil
}

func validateRecoverySpec(spec RecoverySpec) error {
	if spec.Schema != RecoverySpecSchema || (spec.Mode != "RESTORE" && spec.Mode != "ROLLBACK") || !validSHA(spec.TargetArtifactID) ||
		!validSHA(spec.ExpectedServedStampSHA256) || !validSHA(spec.ExpectedStateSHA256) ||
		spec.RecoveryGeneration == 0 || !validName(spec.RecoveryOperatorActorID) || !validName(spec.EvidenceReviewerActorID) ||
		spec.RecoveryOperatorActorID == spec.EvidenceReviewerActorID || spec.StepDeadlineMillis < 1 || spec.StepDeadlineMillis > 60_000 {
		return errRefused
	}
	return nil
}
