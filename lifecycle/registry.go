package lifecycle

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
)

type checkedArtifact struct {
	root     string
	record   RegistryRecord
	envelope Envelope
	receipt  Receipt
	result   ArtifactResult
}

func ensureRegistryContainers(root string) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return errRefused
	}
	for _, entry := range entries {
		if entry.Name() != "artifacts" && entry.Name() != "routes" {
			return errRefused
		}
	}
	for _, name := range []string{"artifacts", "routes"} {
		path := filepath.Join(root, name)
		if err := os.Mkdir(path, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return errRefused
		}
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o700 {
			return errRefused
		}
	}
	return nil
}

func validateRegistryLayout(root string) error {
	if _, err := resolvedDirectory(root, 0o700); err != nil {
		return errRefused
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 2 || entries[0].Name() != "artifacts" || entries[1].Name() != "routes" {
		return errRefused
	}
	for _, name := range []string{"artifacts", "routes"} {
		info, err := os.Lstat(filepath.Join(root, name))
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o700 {
			return errRefused
		}
	}
	return nil
}

func verifyRegistry(registryPath, artifactID string) (ArtifactResult, error) {
	root, err := resolvedDirectory(registryPath, 0o700)
	if err != nil || !validSHA256(artifactID) || validateRegistryLayout(root) != nil {
		return ArtifactResult{}, errRefused
	}
	checked, err := verifyArtifactRoot(root, artifactID)
	if err != nil {
		return ArtifactResult{}, errRefused
	}
	return checked.result, nil
}

func verifyArtifactRoot(registryRoot, artifactID string) (checkedArtifact, error) {
	root := filepath.Join(registryRoot, "artifacts", artifactID)
	if _, err := resolvedDirectory(root, 0o700); err != nil {
		return checkedArtifact{}, errRefused
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 4 || entries[0].Name() != "bundle" || entries[1].Name() != "envelope.json" || entries[2].Name() != "receipt.json" || entries[3].Name() != "record.json" {
		return checkedArtifact{}, errRefused
	}
	if _, err := resolvedDirectory(filepath.Join(root, "bundle"), 0o700); err != nil {
		return checkedArtifact{}, errRefused
	}
	recordRaw, err := readModeFile(filepath.Join(root, "record.json"), MaxControlBytes)
	if err != nil {
		return checkedArtifact{}, errRefused
	}
	var record RegistryRecord
	if decodeCanonical(recordRaw, MaxControlBytes, &record) != nil || validateRegistryRecord(record, artifactID) != nil {
		return checkedArtifact{}, errRefused
	}
	envelopeRaw, err := readModeFile(filepath.Join(root, "envelope.json"), MaxControlBytes)
	if err != nil {
		return checkedArtifact{}, errRefused
	}
	var envelope Envelope
	if decodeCanonical(envelopeRaw, MaxControlBytes, &envelope) != nil {
		return checkedArtifact{}, errRefused
	}
	receiptRaw, err := readModeFile(filepath.Join(root, "receipt.json"), MaxVerifierOutput)
	if err != nil {
		return checkedArtifact{}, errRefused
	}
	var receipt Receipt
	if decodeCanonical(receiptRaw, MaxVerifierOutput, &receipt) != nil {
		return checkedArtifact{}, errRefused
	}
	payloadSHA, err := PayloadSHA256(envelope.Payload)
	if err != nil || payloadSHA != record.PayloadSHA256 || digestBytes(envelopeRaw) != record.EnvelopeSHA256 || ArtifactID(payloadSHA) != artifactID ||
		envelope.Schema != EnvelopeSchema || envelope.Proof.Kind != record.ProofKind || digestProof(envelope.Proof.Hex) != record.ProofSHA256 ||
		envelope.Proof.Hex != FixtureProof(payloadSHA) || digestBytes(receiptRaw) != record.ReceiptSHA256 ||
		validateReceipt(receipt, envelope.Payload, payloadSHA, artifactID) != nil {
		return checkedArtifact{}, errRefused
	}
	bundleEntries, err := os.ReadDir(filepath.Join(root, "bundle"))
	if err != nil || len(bundleEntries) != len(record.BundleInventory) {
		return checkedArtifact{}, errRefused
	}
	for index, file := range record.BundleInventory {
		if bundleEntries[index].Name() != filepath.Base(file.Path) || bundleEntries[index].Type()&os.ModeSymlink != 0 {
			return checkedArtifact{}, errRefused
		}
		if sha, err := digestModeFile(filepath.Join(root, filepath.FromSlash(file.Path)), 512<<20); err != nil || sha != file.SHA256 {
			return checkedArtifact{}, errRefused
		}
	}
	if record.CandidateManifestSHA256 != inventoryDigest(record.BundleInventory, "bundle/manifest.json") ||
		envelope.Payload.CandidateSHA256 != record.CandidateSHA256 || envelope.Payload.KurnManifestSHA256 != record.CandidateManifestSHA256 ||
		envelope.Payload.ArtifactGeneration != record.ArtifactGeneration || envelope.Payload.PredecessorArtifactID != record.PredecessorArtifactID ||
		envelope.Payload.RouteNamespace != record.RouteNamespace || envelope.Payload.PublisherID != record.PublisherID ||
		envelope.Payload.VerifierProfileID != record.VerifierProfileID || envelope.Payload.TrustRootID != record.TrustRootID ||
		envelope.Payload.VerifierSHA256 != record.VerifierSHA256 || envelope.Payload.ProofKind != record.ProofKind {
		return checkedArtifact{}, errRefused
	}
	registrySHA, err := registryDigest(recordRaw, envelopeRaw, receiptRaw, root, record.BundleInventory)
	if err != nil {
		return checkedArtifact{}, errRefused
	}
	return checkedArtifact{root: root, record: record, envelope: envelope, receipt: receipt, result: ArtifactResult{
		ArtifactID: artifactID, ArtifactGeneration: record.ArtifactGeneration, RegistrySHA256: registrySHA, State: record.State,
	}}, nil
}

func validateRegistryRecord(record RegistryRecord, artifactID string) error {
	if record.Schema != RegistrySchema || record.State != ArtifactState || record.ArtifactID != artifactID || !validSHA256(artifactID) ||
		record.ArtifactGeneration < 1 || !validSHA256(record.CandidateSHA256) || !validSHA256(record.CandidateManifestSHA256) ||
		!validSHA256(record.PayloadSHA256) || !validSHA256(record.EnvelopeSHA256) || record.ProofKind != FixtureProofKind || !validSHA256(record.ProofSHA256) ||
		!validSHA256(record.ReceiptSHA256) || record.RouteNamespace != FixtureRouteScope || record.PublisherID != FixturePublisherID ||
		record.VerifierProfileID != FixtureProfileID || record.TrustRootID != FixtureTrustRootID || !validSHA256(record.VerifierSHA256) ||
		!inventorySortedUnique(record.BundleInventory) || (record.ArtifactGeneration == 1) != (record.PredecessorArtifactID == "") ||
		(record.PredecessorArtifactID != "" && !validSHA256(record.PredecessorArtifactID)) {
		return errRefused
	}
	return nil
}

func readModeFile(path string, maximum int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || info.Mode()&os.ModeSymlink != 0 || hasMultipleLinks(info) {
		return nil, errRefused
	}
	return readRegular(path, maximum)
}

func digestModeFile(path string, maximum int64) (string, error) {
	raw, err := readModeFile(path, maximum)
	if err != nil {
		return "", errRefused
	}
	return digestBytes(raw), nil
}

func registryDigest(recordRaw, envelopeRaw, receiptRaw []byte, root string, inventory []FileDigest) (string, error) {
	var joined bytes.Buffer
	joined.WriteString("kurn-search immutable registry v1\x00")
	for _, item := range []struct {
		name string
		raw  []byte
	}{{"record.json", recordRaw}, {"envelope.json", envelopeRaw}, {"receipt.json", receiptRaw}} {
		joined.WriteString(item.name)
		joined.WriteByte(0)
		joined.WriteString(digestBytes(item.raw))
		joined.WriteByte('\n')
	}
	for _, file := range inventory {
		raw, err := readModeFile(filepath.Join(root, filepath.FromSlash(file.Path)), 512<<20)
		if err != nil || digestBytes(raw) != file.SHA256 {
			return "", errRefused
		}
		joined.WriteString(file.Path)
		joined.WriteByte(0)
		joined.WriteString(file.SHA256)
		joined.WriteByte('\n')
	}
	return digestBytes(joined.Bytes()), nil
}

func validateLineage(root string, spec RegisterSpec, replayNonce string) error {
	entries, err := os.ReadDir(filepath.Join(root, "artifacts"))
	if err != nil {
		return errRefused
	}
	var predecessor *checkedArtifact
	for _, entry := range entries {
		if !entry.IsDir() || !validSHA256(entry.Name()) {
			return errRefused
		}
		checked, err := verifyArtifactRoot(root, entry.Name())
		if err != nil {
			return errRefused
		}
		if checked.envelope.Payload.ReplayNonce == replayNonce {
			return errRefused
		}
		if checked.record.RouteNamespace != spec.RouteNamespace {
			continue
		}
		if checked.record.ArtifactGeneration == spec.ArtifactGeneration || checked.record.PredecessorArtifactID == spec.PredecessorArtifactID && spec.PredecessorArtifactID != "" {
			return errRefused
		}
		if checked.record.ArtifactID == spec.PredecessorArtifactID {
			copy := checked
			predecessor = &copy
		}
	}
	if spec.FirstRelease {
		if predecessor != nil || len(entries) != 0 {
			return errRefused
		}
		return nil
	}
	if predecessor == nil || predecessor.record.ArtifactGeneration+1 != spec.ArtifactGeneration || predecessor.record.RouteNamespace != spec.RouteNamespace {
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
	return nil
}

func sortedArtifactIDs(root string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(root, "artifacts"))
	if err != nil {
		return nil, errRefused
	}
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || !validSHA256(entry.Name()) {
			return nil, errRefused
		}
		ids = append(ids, entry.Name())
	}
	sort.Strings(ids)
	return ids, nil
}

func decodeRegistryRecord(path string) (RegistryRecord, error) {
	raw, err := readModeFile(path, MaxControlBytes)
	if err != nil {
		return RegistryRecord{}, errRefused
	}
	var record RegistryRecord
	if decodeCanonical(raw, MaxControlBytes, &record) != nil {
		return RegistryRecord{}, errRefused
	}
	return record, nil
}

func registryRecordEqual(left, right RegistryRecord) bool {
	leftRaw, _ := json.Marshal(left)
	rightRaw, _ := json.Marshal(right)
	return bytes.Equal(leftRaw, rightRaw)
}
