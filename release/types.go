// Package release turns one admitted adapter's private source snapshot into a
// deterministic, independently verified Kurn bundle candidate. It performs no
// source capture, artifact attestation, registration, shipping, routing, or
// deployment work.
package release

import (
	"context"
	"errors"
)

const (
	SpecSchema                   = "kurn-search-release-spec/v1"
	EvidenceSchema               = "kurn-search-release-private-evidence/v1"
	CandidateSchema              = "kurn-search-release-candidate/v1"
	CandidateState               = "BUILT-UNATTESTED"
	KurnSourceRevision           = "f7423cd8cbb8165caf8aa750f56586a0ee621249"
	KurnCompatibilityLabel       = "v0.6.0-1-gf7423cd"
	KurnBinarySHA256             = "a03086474de8c0c32dd501798b0ea0c0fdd1a93906722fa893eb8c5f4a8a6c55"
	EmptyAnalyzerSHA256          = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	S1InitialBaseSHA256          = "a7e0bb5f2525515b7726fa2b83f1c14cb96931630651936e581c98c928cc9770"
	MaxSnapshotBytes       int64 = 64 << 20
	MaxSnapshotRecords           = 1_000_000
	MaxProcessOutput             = 1 << 20
)

var errRefused = errors.New("release: refused")

// Spec is the one closed semantic input for Build. Path fields are private
// invocation details and are never copied into a candidate.
type Spec struct {
	Schema   string       `json:"schema"`
	Snapshot SnapshotSpec `json:"snapshot"`
	Adapter  AdapterSpec  `json:"adapter"`
	Kurn     KurnSpec     `json:"kurn"`
	Bundle   BundleSpec   `json:"bundle"`
	Policy   PolicySpec   `json:"policy"`
	Golden   GoldenSpec   `json:"golden"`
}

type SnapshotSpec struct {
	ID         string `json:"id"`
	SHA256     string `json:"sha256"`
	Records    int    `json:"records"`
	MaxBytes   int64  `json:"max_bytes"`
	MaxRecords int    `json:"max_records"`
}

type AdapterSpec struct {
	Name           string `json:"name"`
	Version        string `json:"version"`
	BehaviorSHA256 string `json:"behavior_sha256"`
	CodecSHA256    string `json:"codec_sha256"`
	SearchSHA256   string `json:"search_sha256"`
}

type KurnSpec struct {
	Path               string `json:"path"`
	SHA256             string `json:"sha256"`
	SourceRevision     string `json:"source_revision"`
	CompatibilityLabel string `json:"compatibility_label"`
	DeadlineMillis     int    `json:"deadline_millis"`
}

type BundleSpec struct {
	PhysicalList   string           `json:"physical_list"`
	Mode           string           `json:"mode"`
	AnalyzerSHA256 string           `json:"analyzer_sha256"`
	Predecessor    *PredecessorSpec `json:"predecessor,omitempty"`
}

type PredecessorSpec struct {
	Path            string `json:"path"`
	CandidateSHA256 string `json:"candidate_sha256"`
}

type PolicySpec struct {
	TimeoutMillis        uint64 `json:"timeout_millis"`
	MaxInflight          int    `json:"max_inflight"`
	WorstCaseChargeBytes uint64 `json:"worst_case_charge_bytes"`
}

type GoldenSpec struct {
	EntryID     string `json:"entry_id"`
	PhysicalKey string `json:"physical_key"`
}

type Identity struct {
	Role     string `json:"role"`
	Name     string `json:"name"`
	SHA256   string `json:"sha256,omitempty"`
	Status   string `json:"status,omitempty"`
	Producer string `json:"producer"`
	Verifier string `json:"verifier"`
	Exposure string `json:"exposure"`
}

type BundleIdentity struct {
	AnalyzerSHA256    string `json:"analyzer_sha256"`
	BaseSHA256        string `json:"base_sha256"`
	ConfigSHA256      string `json:"config_sha256"`
	Entries           int    `json:"entries"`
	Keys              int    `json:"keys"`
	Mode              string `json:"mode"`
	PredecessorSHA256 string `json:"predecessor_sha256,omitempty"`
	DeltaAdds         int    `json:"delta_adds,omitempty"`
	DeltaUpdates      int    `json:"delta_updates,omitempty"`
	DeltaDeletes      int    `json:"delta_deletes,omitempty"`
}

type CandidateFile struct {
	Path   string `json:"path"`
	Class  string `json:"class"`
	SHA256 string `json:"sha256,omitempty"`
}

// Candidate contains no invocation path or raw private identity. The sealed
// private evidence file binds the source and feed identities.
type Candidate struct {
	Schema        string          `json:"schema"`
	State         string          `json:"state"`
	Adapter       AdapterSpec     `json:"adapter"`
	Kurn          CandidateKurn   `json:"kurn"`
	Bundle        BundleIdentity  `json:"bundle"`
	ProfileSHA256 string          `json:"profile_sha256"`
	S1Relation    string          `json:"s1_initial_relation"`
	Identities    []Identity      `json:"identities"`
	Files         []CandidateFile `json:"files"`
}

type CandidateKurn struct {
	SHA256             string `json:"sha256"`
	SourceRevision     string `json:"source_revision"`
	CompatibilityLabel string `json:"compatibility_label"`
	GoVersion          string `json:"go_version"`
	ModulePath         string `json:"module_path"`
	CommandPath        string `json:"command_path"`
}

type PrivateEvidence struct {
	Schema        string               `json:"schema"`
	Snapshot      SnapshotEvidence     `json:"snapshot"`
	FeedSHA256    string               `json:"feed_sha256"`
	MappingSHA256 string               `json:"mapping_sha256"`
	PhysicalList  string               `json:"physical_list"`
	Golden        GoldenSpec           `json:"golden"`
	Policy        PolicySpec           `json:"policy"`
	Predecessor   *PredecessorEvidence `json:"predecessor,omitempty"`
}

type SnapshotEvidence struct {
	ID      string `json:"id"`
	SHA256  string `json:"sha256"`
	Records int    `json:"records"`
	Bytes   int64  `json:"bytes"`
}

type PredecessorEvidence struct {
	CandidateSHA256 string `json:"candidate_sha256"`
	BaseSHA256      string `json:"base_sha256"`
}

type Result struct {
	CandidateSHA256 string `json:"candidate_sha256"`
	Entries         int    `json:"entries"`
	Keys            int    `json:"keys"`
	State           string `json:"state"`
}

// Build creates one absent-only candidate transaction.
func Build(ctx context.Context, specPath, sourcePath, outputPath string) (Result, error) {
	return build(ctx, specPath, sourcePath, outputPath)
}

// Verify reconstructs one existing candidate without network or mutation.
func Verify(candidatePath string) (Result, error) { return verify(candidatePath) }

// MarshalSpec emits the sole accepted canonical spec form.
func MarshalSpec(spec Spec) ([]byte, error) { return marshalCanonical(spec) }
