// Package lifecycle provides the private, local conformance seam between an
// independently verified S2 candidate, an external verifier, an immutable
// artifact registry, and a guarded route-state contract. The included fixture
// proof kind is deliberately non-authenticating.
package lifecycle

import (
	"context"
	"errors"
)

const (
	RegisterSpecSchema  = "kurn-search-lifecycle-register/v1"
	EnvelopeSchema      = "kurn-search-artifact-envelope/v1"
	VerifierProtocol    = "kurn-search-external-verifier/v1"
	ReceiptSchema       = "kurn-search-verifier-receipt/v1"
	RegistrySchema      = "kurn-search-artifact-registry-record/v1"
	RouteIntentSchema   = "kurn-search-route-intent/v1"
	RouteDecisionSchema = "kurn-search-route-decision/v1"

	FixtureProofKind   = "fixture-checksum/v1"
	FixtureProfileID   = "fixture-profile/v1"
	FixtureTrustRootID = "fixture-trust-root/v1"
	FixturePublisherID = "fixture-publisher/v1"
	FixtureRouteScope  = "fixture-route/v1"

	ArtifactState     = "EXTERNAL-VERIFIER-ACCEPTED"
	MaxControlBytes   = 1 << 20
	MaxProofBytes     = 4096
	MaxVerifierOutput = 64 << 10
)

var errRefused = errors.New("lifecycle: refused")

type FileDigest struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type RegisterSpec struct {
	Schema                  string         `json:"schema"`
	CandidateSHA256         string         `json:"candidate_sha256"`
	CandidateManifestSHA256 string         `json:"candidate_manifest_sha256"`
	ArtifactGeneration      uint64         `json:"artifact_generation"`
	PredecessorArtifactID   string         `json:"predecessor_artifact_id"`
	FirstRelease            bool           `json:"first_release"`
	BundleInventory         []FileDigest   `json:"bundle_inventory"`
	RouteNamespace          string         `json:"route_namespace"`
	Verifier                VerifierSpec   `json:"verifier"`
	Policy                  RegistryPolicy `json:"policy"`
	PublisherID             string         `json:"publisher_id"`
	ProofKind               string         `json:"proof_kind"`
}

type VerifierSpec struct {
	Path           string `json:"path"`
	SHA256         string `json:"sha256"`
	Protocol       string `json:"protocol"`
	ProfileID      string `json:"profile_id"`
	TrustRootID    string `json:"trust_root_id"`
	DeadlineMillis int    `json:"deadline_millis"`
}

type RegistryPolicy struct {
	MaxArtifactBytes int64  `json:"max_artifact_bytes"`
	MaxGeneration    uint64 `json:"max_generation"`
}

type Envelope struct {
	Schema  string          `json:"schema"`
	Payload EnvelopePayload `json:"payload"`
	Proof   EnvelopeProof   `json:"proof"`
}

type EnvelopePayload struct {
	ProtocolVersion          string `json:"protocol_version"`
	CandidateSHA256          string `json:"candidate_sha256"`
	KurnManifestSHA256       string `json:"kurn_manifest_sha256"`
	ConfigSHA256             string `json:"config_sha256"`
	BaseSHA256               string `json:"base_sha256"`
	IndexSHA256              string `json:"index_sha256"`
	KurnSHA256               string `json:"kurn_sha256"`
	ArtifactGeneration       uint64 `json:"artifact_generation"`
	PredecessorArtifactID    string `json:"predecessor_artifact_id"`
	AdapterBehaviorSHA256    string `json:"adapter_behavior_sha256"`
	OperationalProfileSHA256 string `json:"operational_profile_sha256"`
	PublisherID              string `json:"publisher_id"`
	VerifierProfileID        string `json:"verifier_profile_id"`
	TrustRootID              string `json:"trust_root_id"`
	VerifierSHA256           string `json:"verifier_sha256"`
	ProofKind                string `json:"proof_kind"`
	RouteNamespace           string `json:"route_namespace"`
	ReplayNonce              string `json:"replay_nonce"`
}

type EnvelopeProof struct {
	Kind string `json:"kind"`
	Hex  string `json:"hex"`
}

type Receipt struct {
	Schema                string `json:"schema"`
	Decision              string `json:"decision"`
	PayloadSHA256         string `json:"payload_sha256"`
	ArtifactID            string `json:"artifact_id"`
	CandidateSHA256       string `json:"candidate_sha256"`
	PredecessorArtifactID string `json:"predecessor_artifact_id"`
	ArtifactGeneration    uint64 `json:"artifact_generation"`
	PublisherID           string `json:"publisher_id"`
	TrustRootID           string `json:"trust_root_id"`
	VerifierSHA256        string `json:"verifier_sha256"`
	ProofKind             string `json:"proof_kind"`
	RouteNamespace        string `json:"route_namespace"`
}

type RegistryRecord struct {
	Schema                  string       `json:"schema"`
	State                   string       `json:"state"`
	ArtifactID              string       `json:"artifact_id"`
	ArtifactGeneration      uint64       `json:"artifact_generation"`
	PredecessorArtifactID   string       `json:"predecessor_artifact_id"`
	CandidateSHA256         string       `json:"candidate_sha256"`
	CandidateManifestSHA256 string       `json:"candidate_manifest_sha256"`
	PayloadSHA256           string       `json:"payload_sha256"`
	EnvelopeSHA256          string       `json:"envelope_sha256"`
	ProofKind               string       `json:"proof_kind"`
	ProofSHA256             string       `json:"proof_sha256"`
	ReceiptSHA256           string       `json:"receipt_sha256"`
	RouteNamespace          string       `json:"route_namespace"`
	PublisherID             string       `json:"publisher_id"`
	VerifierProfileID       string       `json:"verifier_profile_id"`
	TrustRootID             string       `json:"trust_root_id"`
	VerifierSHA256          string       `json:"verifier_sha256"`
	BundleInventory         []FileDigest `json:"bundle_inventory"`
}

type ArtifactResult struct {
	ArtifactID         string `json:"artifact_id"`
	ArtifactGeneration uint64 `json:"artifact_generation"`
	RegistrySHA256     string `json:"registry_sha256"`
	State              string `json:"state"`
	Noop               bool   `json:"noop"`
}

type RouteIntent struct {
	Schema                   string `json:"schema"`
	RouteID                  string `json:"route_id"`
	Generation               uint64 `json:"generation"`
	ArtifactID               string `json:"artifact_id"`
	PredecessorArtifactID    string `json:"predecessor_artifact_id"`
	PredecessorRouteSHA256   string `json:"predecessor_route_sha256"`
	AdapterBehaviorSHA256    string `json:"adapter_behavior_sha256"`
	OperationalProfileSHA256 string `json:"operational_profile_sha256"`
	KurnSHA256               string `json:"kurn_sha256"`
	PhysicalListIntentSHA256 string `json:"physical_list_intent_sha256"`
	DeploymentGeneration     string `json:"deployment_generation"`
	NodeSetSHA256            string `json:"node_set_sha256"`
	GoldenSetSHA256          string `json:"golden_set_sha256"`
	ServedStampSHA256        string `json:"served_stamp_sha256"`
	Transition               string `json:"transition"`
	PriorStateSHA256         string `json:"prior_state_sha256"`
	AuthorityReceiptSHA256   string `json:"authority_receipt_sha256"`
	RouteEnvelopeSHA256      string `json:"route_envelope_sha256"`
	CanaryDecision           string `json:"canary_decision"`
	SuccessorRouteID         string `json:"successor_route_id"`
	SuccessorStateSHA256     string `json:"successor_state_sha256"`
	Inflight                 uint64 `json:"inflight"`
}

type RouteDecision struct {
	Schema                   string `json:"schema"`
	RouteID                  string `json:"route_id"`
	Generation               uint64 `json:"generation"`
	State                    string `json:"state"`
	ArtifactID               string `json:"artifact_id"`
	PredecessorArtifactID    string `json:"predecessor_artifact_id"`
	PredecessorRouteSHA256   string `json:"predecessor_route_sha256"`
	IntentSHA256             string `json:"intent_sha256"`
	PriorStateSHA256         string `json:"prior_state_sha256"`
	AuthorityReceiptSHA256   string `json:"authority_receipt_sha256"`
	RouteEnvelopeSHA256      string `json:"route_envelope_sha256"`
	PhysicalListIntentSHA256 string `json:"physical_list_intent_sha256"`
	DeploymentGeneration     string `json:"deployment_generation"`
	NodeSetSHA256            string `json:"node_set_sha256"`
	GoldenSetSHA256          string `json:"golden_set_sha256"`
	ServedStampSHA256        string `json:"served_stamp_sha256"`
	SuccessorRouteID         string `json:"successor_route_id"`
	SuccessorStateSHA256     string `json:"successor_state_sha256"`
	Transition               string `json:"transition"`
	Decision                 string `json:"decision"`
}

type RouteResult struct {
	ArtifactID     string `json:"artifact_id"`
	DecisionSHA256 string `json:"decision_sha256"`
	Generation     uint64 `json:"generation"`
	RouteID        string `json:"route_id"`
	State          string `json:"state"`
}

func Register(ctx context.Context, specPath, candidatePath, envelopePath, registryPath string) (ArtifactResult, error) {
	return register(ctx, specPath, candidatePath, envelopePath, registryPath)
}

func VerifyRegistry(registryPath, artifactID string) (ArtifactResult, error) {
	return verifyRegistry(registryPath, artifactID)
}

func ApplyRoute(registryPath, intentPath string) (RouteResult, error) {
	return applyRoute(registryPath, intentPath)
}

func InspectRoute(registryPath, routeID string) (RouteResult, error) {
	return inspectRoute(registryPath, routeID)
}

func MarshalRegisterSpec(value RegisterSpec) ([]byte, error) { return marshalCanonical(value) }
func MarshalEnvelope(value Envelope) ([]byte, error)         { return marshalCanonical(value) }
func MarshalReceipt(value Receipt) ([]byte, error)           { return marshalCanonical(value) }
func MarshalRouteIntent(value RouteIntent) ([]byte, error)   { return marshalCanonical(value) }
func MarshalResult(value any) ([]byte, error)                { return marshalCanonical(value) }
