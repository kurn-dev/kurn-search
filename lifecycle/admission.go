package lifecycle

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/kurn-dev/kurn-search/release"
)

func register(ctx context.Context, specPath, candidatePath, envelopePath, registryPath string) (ArtifactResult, error) {
	if ctx == nil {
		return ArtifactResult{}, errRefused
	}
	specFile, err := resolvedRegular(specPath, MaxControlBytes)
	if err != nil {
		return ArtifactResult{}, errRefused
	}
	envelopeFile, err := resolvedRegular(envelopePath, MaxControlBytes)
	if err != nil {
		return ArtifactResult{}, errRefused
	}
	candidateRoot, err := resolvedDirectory(candidatePath, 0o700)
	if err != nil {
		return ArtifactResult{}, errRefused
	}
	registryRoot, err := resolvedDirectory(registryPath, 0o700)
	if err != nil || pathsOverlap(candidateRoot, registryRoot) || pathsOverlap(specFile, registryRoot) || pathsOverlap(envelopeFile, registryRoot) {
		return ArtifactResult{}, errRefused
	}
	specRaw, err := readModeFile(specFile, MaxControlBytes)
	if err != nil {
		return ArtifactResult{}, errRefused
	}
	var spec RegisterSpec
	if decodeCanonical(specRaw, MaxControlBytes, &spec) != nil || validateRegisterSpec(spec) != nil {
		return ArtifactResult{}, errRefused
	}
	if pathsOverlap(spec.Verifier.Path, registryRoot) || pathsOverlap(spec.Verifier.Path, candidateRoot) {
		return ArtifactResult{}, errRefused
	}
	verifierSHA, err := DigestExecutable(spec.Verifier.Path)
	if err != nil || verifierSHA != spec.Verifier.SHA256 {
		return ArtifactResult{}, errRefused
	}
	releaseResult, err := release.Verify(candidateRoot)
	if err != nil || releaseResult.State != release.CandidateState || releaseResult.CandidateSHA256 != spec.CandidateSHA256 {
		return ArtifactResult{}, errRefused
	}
	candidateRaw, err := readRegular(filepath.Join(candidateRoot, "candidate.json"), MaxControlBytes)
	if err != nil || digestBytes(candidateRaw) != spec.CandidateSHA256 {
		return ArtifactResult{}, errRefused
	}
	var candidate release.Candidate
	if json.Unmarshal(candidateRaw, &candidate) != nil {
		return ArtifactResult{}, errRefused
	}
	inventory := candidateBundleInventory(candidate)
	if !equalInventory(inventory, spec.BundleInventory) {
		return ArtifactResult{}, errRefused
	}
	manifestSHA := inventoryDigest(inventory, "bundle/manifest.json")
	if manifestSHA == "" || manifestSHA != spec.CandidateManifestSHA256 {
		return ArtifactResult{}, errRefused
	}
	envelopeRaw, err := readModeFile(envelopeFile, MaxControlBytes)
	if err != nil {
		return ArtifactResult{}, errRefused
	}
	var envelope Envelope
	if decodeCanonical(envelopeRaw, MaxControlBytes, &envelope) != nil || validateEnvelope(envelope, spec, candidate, inventory) != nil {
		return ArtifactResult{}, errRefused
	}
	payloadSHA, err := PayloadSHA256(envelope.Payload)
	if err != nil {
		return ArtifactResult{}, errRefused
	}
	artifactID := ArtifactID(payloadSHA)
	if artifactID == "" {
		return ArtifactResult{}, errRefused
	}
	receiptRaw, err := runVerifier(ctx, spec, envelopeRaw)
	if err != nil {
		return ArtifactResult{}, errRefused
	}
	var receipt Receipt
	if decodeCanonical(receiptRaw, MaxVerifierOutput, &receipt) != nil || validateReceipt(receipt, envelope.Payload, payloadSHA, artifactID) != nil {
		return ArtifactResult{}, errRefused
	}
	record := RegistryRecord{
		Schema: RegistrySchema, State: ArtifactState, ArtifactID: artifactID,
		ArtifactGeneration: spec.ArtifactGeneration, PredecessorArtifactID: spec.PredecessorArtifactID,
		CandidateSHA256: spec.CandidateSHA256, CandidateManifestSHA256: spec.CandidateManifestSHA256,
		PayloadSHA256: payloadSHA, EnvelopeSHA256: digestBytes(envelopeRaw), ProofKind: envelope.Proof.Kind, ProofSHA256: digestProof(envelope.Proof.Hex),
		ReceiptSHA256: digestBytes(receiptRaw), RouteNamespace: spec.RouteNamespace,
		PublisherID: spec.PublisherID, VerifierProfileID: spec.Verifier.ProfileID,
		TrustRootID: spec.Verifier.TrustRootID, VerifierSHA256: spec.Verifier.SHA256,
		BundleInventory: append([]FileDigest(nil), inventory...),
	}
	recordRaw, err := marshalCanonical(record)
	if err != nil {
		return ArtifactResult{}, errRefused
	}
	if err := ensureRegistryContainers(registryRoot); err != nil {
		return ArtifactResult{}, errRefused
	}
	artifactsRoot := filepath.Join(registryRoot, "artifacts")
	target := filepath.Join(artifactsRoot, artifactID)
	if _, err := os.Lstat(target); err == nil {
		checked, verifyErr := verifyArtifactRoot(registryRoot, artifactID)
		if verifyErr != nil || !artifactMatches(checked.root, candidateRoot, inventory, envelopeRaw, receiptRaw, recordRaw) {
			return ArtifactResult{}, errRefused
		}
		checked.result.Noop = true
		return checked.result, nil
	} else if !os.IsNotExist(err) {
		return ArtifactResult{}, errRefused
	}
	if validateLineage(registryRoot, spec, envelope.Payload.ReplayNonce) != nil {
		return ArtifactResult{}, errRefused
	}
	if ctx.Err() != nil {
		return ArtifactResult{}, errRefused
	}
	temporary, err := os.MkdirTemp(artifactsRoot, ".artifact.tmp-")
	if err != nil || os.Chmod(temporary, 0o700) != nil {
		if temporary != "" {
			_ = os.RemoveAll(temporary)
		}
		return ArtifactResult{}, errRefused
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.RemoveAll(temporary)
		}
	}()
	if err := os.Mkdir(filepath.Join(temporary, "bundle"), 0o700); err != nil {
		return ArtifactResult{}, errRefused
	}
	total := int64(len(envelopeRaw) + len(receiptRaw) + len(recordRaw))
	for _, file := range inventory {
		if ctx.Err() != nil {
			return ArtifactResult{}, errRefused
		}
		raw, err := readRegular(filepath.Join(candidateRoot, filepath.FromSlash(file.Path)), spec.Policy.MaxArtifactBytes)
		if err != nil || digestBytes(raw) != file.SHA256 {
			return ArtifactResult{}, errRefused
		}
		total += int64(len(raw))
		if total > spec.Policy.MaxArtifactBytes || writePrivate(filepath.Join(temporary, filepath.Base(file.Path)), raw) != nil {
			return ArtifactResult{}, errRefused
		}
		// Move each copied file into the fixed bundle directory without changing bytes.
		if os.Rename(filepath.Join(temporary, filepath.Base(file.Path)), filepath.Join(temporary, "bundle", filepath.Base(file.Path))) != nil {
			return ArtifactResult{}, errRefused
		}
		if again, err := digestFile(filepath.Join(candidateRoot, filepath.FromSlash(file.Path)), spec.Policy.MaxArtifactBytes); err != nil || again != file.SHA256 {
			return ArtifactResult{}, errRefused
		}
	}
	for name, raw := range map[string][]byte{"envelope.json": envelopeRaw, "receipt.json": receiptRaw, "record.json": recordRaw} {
		if writePrivate(filepath.Join(temporary, name), raw) != nil {
			return ArtifactResult{}, errRefused
		}
	}
	if ctx.Err() != nil {
		return ArtifactResult{}, errRefused
	}
	if err := syncDirectory(filepath.Join(temporary, "bundle")); err != nil || syncDirectory(temporary) != nil || os.Rename(temporary, target) != nil || syncDirectory(artifactsRoot) != nil {
		return ArtifactResult{}, errRefused
	}
	keep = true
	checked, err := verifyArtifactRoot(registryRoot, artifactID)
	if err != nil {
		return ArtifactResult{}, errRefused
	}
	return checked.result, nil
}

func validateRegisterSpec(spec RegisterSpec) error {
	if spec.Schema != RegisterSpecSchema || !validSHA256(spec.CandidateSHA256) || !validSHA256(spec.CandidateManifestSHA256) ||
		spec.ArtifactGeneration < 1 || spec.ArtifactGeneration > spec.Policy.MaxGeneration || !inventorySortedUnique(spec.BundleInventory) ||
		spec.RouteNamespace != FixtureRouteScope || spec.PublisherID != FixturePublisherID || spec.ProofKind != FixtureProofKind ||
		spec.Verifier.Protocol != VerifierProtocol || spec.Verifier.ProfileID != FixtureProfileID || spec.Verifier.TrustRootID != FixtureTrustRootID ||
		!validSHA256(spec.Verifier.SHA256) || spec.Verifier.DeadlineMillis < 1 || spec.Verifier.DeadlineMillis > 60_000 ||
		spec.Policy.MaxArtifactBytes < 1 || spec.Policy.MaxArtifactBytes > 512<<20 || spec.Policy.MaxGeneration < 1 || spec.Policy.MaxGeneration > 1_000_000 {
		return errRefused
	}
	if spec.FirstRelease != (spec.PredecessorArtifactID == "") || (spec.FirstRelease && spec.ArtifactGeneration != 1) || (!spec.FirstRelease && !validSHA256(spec.PredecessorArtifactID)) {
		return errRefused
	}
	return nil
}

func validateEnvelope(envelope Envelope, spec RegisterSpec, candidate release.Candidate, inventory []FileDigest) error {
	if envelope.Schema != EnvelopeSchema || envelope.Proof.Kind != FixtureProofKind || spec.ProofKind != envelope.Proof.Kind {
		return errRefused
	}
	proof, err := hex.DecodeString(envelope.Proof.Hex)
	if err != nil || len(proof) < 1 || len(proof) > MaxProofBytes || hex.EncodeToString(proof) != envelope.Proof.Hex {
		return errRefused
	}
	p := envelope.Payload
	if p.ProtocolVersion != VerifierProtocol || p.CandidateSHA256 != spec.CandidateSHA256 || p.KurnManifestSHA256 != spec.CandidateManifestSHA256 ||
		p.ArtifactGeneration != spec.ArtifactGeneration || p.PredecessorArtifactID != spec.PredecessorArtifactID ||
		p.AdapterBehaviorSHA256 != candidate.Adapter.BehaviorSHA256 || p.OperationalProfileSHA256 != candidate.ProfileSHA256 ||
		p.KurnSHA256 != candidate.Kurn.SHA256 || p.PublisherID != spec.PublisherID || p.VerifierProfileID != spec.Verifier.ProfileID ||
		p.TrustRootID != spec.Verifier.TrustRootID || p.VerifierSHA256 != spec.Verifier.SHA256 || p.ProofKind != spec.ProofKind ||
		p.RouteNamespace != spec.RouteNamespace || validateEnvelopePayloadShape(p) != nil ||
		p.BaseSHA256 != inventoryDigest(inventory, "bundle/base.jsonl") || p.ConfigSHA256 != candidate.Bundle.ConfigSHA256 ||
		p.IndexSHA256 != inventoryDigest(inventory, "bundle/base.idx") {
		return errRefused
	}
	payloadSHA, err := PayloadSHA256(p)
	if err != nil || envelope.Proof.Hex != FixtureProof(payloadSHA) {
		return errRefused
	}
	return nil
}

func PayloadSHA256(payload EnvelopePayload) (string, error) {
	if validateEnvelopePayloadShape(payload) != nil {
		return "", errRefused
	}
	raw, err := marshalCanonical(payload)
	if err != nil {
		return "", errRefused
	}
	return digestDomain("kurn-search envelope payload v1", raw), nil
}

// ReplayNonce deterministically binds replay protection to the candidate,
// predecessor, generation, and opaque route namespace. It is not randomness
// and carries no authenticity property.
func ReplayNonce(candidateSHA, predecessor string, generation uint64, routeNamespace string) string {
	if !validSHA256(candidateSHA) || (predecessor != "" && !validSHA256(predecessor)) || generation < 1 || !validOpaque(routeNamespace) {
		return ""
	}
	value := struct {
		CandidateSHA256       string `json:"candidate_sha256"`
		ArtifactGeneration    uint64 `json:"artifact_generation"`
		PredecessorArtifactID string `json:"predecessor_artifact_id"`
		RouteNamespace        string `json:"route_namespace"`
	}{candidateSHA, generation, predecessor, routeNamespace}
	raw, err := marshalCanonical(value)
	if err != nil {
		return ""
	}
	return digestDomain("kurn-search replay nonce v1", raw)
}

func validateEnvelopePayloadShape(p EnvelopePayload) error {
	if p.ProtocolVersion != VerifierProtocol || !validSHA256(p.CandidateSHA256) || !validSHA256(p.KurnManifestSHA256) ||
		!validSHA256(p.ConfigSHA256) || !validSHA256(p.BaseSHA256) || !validSHA256(p.IndexSHA256) || !validSHA256(p.KurnSHA256) ||
		p.ArtifactGeneration < 1 || (p.PredecessorArtifactID != "" && !validSHA256(p.PredecessorArtifactID)) ||
		(p.ArtifactGeneration == 1) != (p.PredecessorArtifactID == "") || !validSHA256(p.AdapterBehaviorSHA256) ||
		!validSHA256(p.OperationalProfileSHA256) || p.PublisherID != FixturePublisherID || p.VerifierProfileID != FixtureProfileID ||
		p.TrustRootID != FixtureTrustRootID || !validSHA256(p.VerifierSHA256) || p.ProofKind != FixtureProofKind ||
		p.RouteNamespace != FixtureRouteScope || p.ReplayNonce != ReplayNonce(p.CandidateSHA256, p.PredecessorArtifactID, p.ArtifactGeneration, p.RouteNamespace) {
		return errRefused
	}
	return nil
}

func FixtureProof(payloadSHA string) string {
	if !validSHA256(payloadSHA) {
		return ""
	}
	return digestDomain("kurn-search non-authenticating fixture checksum v1", []byte(payloadSHA))
}

func ArtifactID(payloadSHA string) string {
	if !validSHA256(payloadSHA) {
		return ""
	}
	return digestDomain("kurn-search external-verifier artifact v1", []byte(payloadSHA))
}

func digestDomain(domain string, raw []byte) string {
	framed := make([]byte, 0, len(domain)+1+8+len(raw))
	framed = append(framed, domain...)
	framed = append(framed, 0)
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(raw)))
	framed = append(framed, length[:]...)
	framed = append(framed, raw...)
	return digestBytes(framed)
}

func digestProof(value string) string {
	raw, err := hex.DecodeString(value)
	if err != nil {
		return ""
	}
	return digestBytes(raw)
}

func candidateBundleInventory(candidate release.Candidate) []FileDigest {
	values := make([]FileDigest, 0, 5)
	for _, file := range candidate.Files {
		if file.Class == "bundle" {
			values = append(values, FileDigest{Path: file.Path, SHA256: file.SHA256})
		}
	}
	sort.Slice(values, func(i, j int) bool { return values[i].Path < values[j].Path })
	return values
}

func inventoryDigest(values []FileDigest, name string) string {
	for _, value := range values {
		if value.Path == name {
			return value.SHA256
		}
	}
	return ""
}

type boundedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *boundedBuffer) Write(raw []byte) (int, error) {
	if len(raw)+b.Len() > b.limit {
		return 0, errRefused
	}
	return b.Buffer.Write(raw)
}

func runVerifier(parent context.Context, spec RegisterSpec, envelopeRaw []byte) ([]byte, error) {
	ctx, cancel := context.WithTimeout(parent, time.Duration(spec.Verifier.DeadlineMillis)*time.Millisecond)
	defer cancel()
	var frame bytes.Buffer
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(envelopeRaw)))
	frame.Write(length[:])
	frame.Write(envelopeRaw)
	stdout := &boundedBuffer{limit: MaxVerifierOutput}
	stderr := &boundedBuffer{limit: MaxVerifierOutput}
	command := exec.CommandContext(ctx, spec.Verifier.Path)
	command.Env = []string{"LANG=C", "LC_ALL=C", "TZ=UTC"}
	command.Stdin = &frame
	command.Stdout = stdout
	command.Stderr = stderr
	command.WaitDelay = time.Second
	if err := command.Run(); err != nil || ctx.Err() != nil || stderr.Len() != 0 || stdout.Len() == 0 {
		return nil, errRefused
	}
	return bytes.Clone(stdout.Bytes()), nil
}

func validateReceipt(receipt Receipt, payload EnvelopePayload, payloadSHA, artifactID string) error {
	if receipt.Schema != ReceiptSchema || receipt.Decision != "accepted" || receipt.PayloadSHA256 != payloadSHA || receipt.ArtifactID != artifactID ||
		receipt.CandidateSHA256 != payload.CandidateSHA256 || receipt.PredecessorArtifactID != payload.PredecessorArtifactID ||
		receipt.ArtifactGeneration != payload.ArtifactGeneration || receipt.PublisherID != payload.PublisherID ||
		receipt.TrustRootID != payload.TrustRootID || receipt.VerifierSHA256 != payload.VerifierSHA256 ||
		receipt.ProofKind != payload.ProofKind || receipt.RouteNamespace != payload.RouteNamespace {
		return errRefused
	}
	return nil
}

// VerifyFixtureEnvelope validates the sole non-authenticating fixture proof and
// returns the exact protocol receipt. It must never be used as a production
// authenticity decision.
func VerifyFixtureEnvelope(raw []byte, executableSHA string) (Receipt, error) {
	var envelope Envelope
	if decodeCanonical(raw, MaxControlBytes, &envelope) != nil || envelope.Schema != EnvelopeSchema || executableSHA == "" || !validSHA256(executableSHA) {
		return Receipt{}, errRefused
	}
	p := envelope.Payload
	if validateEnvelopePayloadShape(p) != nil || p.VerifierSHA256 != executableSHA || envelope.Proof.Kind != FixtureProofKind {
		return Receipt{}, errRefused
	}
	payloadSHA, err := PayloadSHA256(p)
	if err != nil || envelope.Proof.Hex != FixtureProof(payloadSHA) {
		return Receipt{}, errRefused
	}
	artifactID := ArtifactID(payloadSHA)
	return Receipt{
		Schema: ReceiptSchema, Decision: "accepted", PayloadSHA256: payloadSHA, ArtifactID: artifactID,
		CandidateSHA256: p.CandidateSHA256, PredecessorArtifactID: p.PredecessorArtifactID,
		ArtifactGeneration: p.ArtifactGeneration, PublisherID: p.PublisherID,
		TrustRootID: p.TrustRootID, VerifierSHA256: p.VerifierSHA256,
		ProofKind: p.ProofKind, RouteNamespace: p.RouteNamespace,
	}, nil
}

func pathsOverlap(left, right string) bool {
	left, right = filepath.Clean(left), filepath.Clean(right)
	if left == right {
		return true
	}
	return strings.HasPrefix(left, right+string(os.PathSeparator)) || strings.HasPrefix(right, left+string(os.PathSeparator))
}

func artifactMatches(target, candidate string, inventory []FileDigest, envelope, receipt, record []byte) bool {
	for name, expected := range map[string][]byte{"envelope.json": envelope, "receipt.json": receipt, "record.json": record} {
		raw, err := readRegular(filepath.Join(target, name), MaxControlBytes)
		if err != nil || !bytes.Equal(raw, expected) {
			return false
		}
	}
	for _, file := range inventory {
		left, errLeft := readRegular(filepath.Join(target, filepath.FromSlash(file.Path)), 512<<20)
		right, errRight := readRegular(filepath.Join(candidate, filepath.FromSlash(file.Path)), 512<<20)
		if errLeft != nil || errRight != nil || !bytes.Equal(left, right) {
			return false
		}
	}
	return true
}

func sameRecord(left, right RegistryRecord) bool { return reflect.DeepEqual(left, right) }
