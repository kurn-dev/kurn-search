// Package fleet provides the private, local W5-S4 conformance seam for
// planning, staging, inspecting, and recovering a fixed fixture fleet. It is
// deliberately not a production fleet controller.
package fleet

import "errors"

const (
	SpecSchema         = "kurn-search-fleet-spec/v1"
	PlanPrivateSchema  = "kurn-search-fleet-plan-private/v1"
	PlanSchema         = "kurn-search-fleet-plan/v1"
	StateSchema        = "kurn-search-fleet-state/v1"
	ObservationSchema  = "kurn-search-fleet-observation/v1"
	AuditSchema        = "kurn-search-fleet-audit/v1"
	RecoverySpecSchema = "kurn-search-fleet-recovery-spec/v1"
	RecoverySchema     = "kurn-search-fleet-recovery/v1"
	OwnershipSchema    = "kurn-search-fleet-ownership/v1"
	ResultSchema       = "kurn-search-fleet-result/v1"

	KurnSourceRevision = "f7423cd8cbb8165caf8aa750f56586a0ee621249"
	KurnCompatibility  = "v0.6.0-1-gf7423cd"
	KurnSHA256         = "a03086474de8c0c32dd501798b0ea0c0fdd1a93906722fa893eb8c5f4a8a6c55"
	KurndSHA256        = "240f20897a8d34e14123c343f1df11a6dadcae7deceff97c781323cdeee988e9"
	KurnSearchdSHA256  = "aed2bbb6f80ab1469bf5200c7ecc15080a9ac1a8e141bb06cd4c25591c78ee07"

	PhysicalListName    = "catalog-identifiers-v1"
	NeutralGoldenID     = "neutral-golden"
	NeutralGoldenSHA256 = "db655271800b88538ece763ed81c112ad41080e7649e926e8808b7312fa29d7a"
	ManagementTopology  = "shared-loopback-listener; separate-management-port-unproved"
	MaxControlBytes     = 1 << 20
	MaxBundleBytes      = 1 << 30
	MaxNodes            = 32
	MaxGoldens          = 64
	MaxMetrics          = 32
)

var errRefused = errors.New("fleet: refused")

type Executable struct {
	Name               string `json:"name"`
	Path               string `json:"path"`
	SHA256             string `json:"sha256"`
	SourceRevision     string `json:"source_revision"`
	CompatibilityLabel string `json:"compatibility_label"`
}

type ExecutableSet struct {
	Kurn        Executable `json:"kurn"`
	Kurnd       Executable `json:"kurnd"`
	KurnSearchd Executable `json:"kurn_searchd"`
}

type NodeSpec struct {
	BackendCredentialFile string `json:"backend_credential_file"`
	ID                    string `json:"id"`
	Root                  string `json:"root"`
}

type BundleExpectation struct {
	BaseSHA256   string `json:"base_sha256"`
	ConfigSHA256 string `json:"config_sha256"`
	Entries      uint64 `json:"entries"`
}

type GoldenSpec struct {
	ExpectedSHA256 string `json:"expected_sha256"`
	ID             string `json:"id"`
}

type MetricRule struct {
	Maximum int64  `json:"maximum"`
	Minimum int64  `json:"minimum"`
	Name    string `json:"name"`
}

type RoleAssignment struct {
	ActorID string `json:"actor_id"`
	Role    string `json:"role"`
	ScopeID string `json:"scope_id"`
}

type FleetSpec struct {
	ArtifactID                    string            `json:"artifact_id"`
	Bundle                        BundleExpectation `json:"bundle"`
	Executables                   ExecutableSet     `json:"executables"`
	FirstGeneration               bool              `json:"first_generation"`
	FleetID                       string            `json:"fleet_id"`
	Generation                    uint64            `json:"generation"`
	Goldens                       []GoldenSpec      `json:"goldens"`
	MaxObservationGeneration      uint64            `json:"max_observation_generation"`
	Metrics                       []MetricRule      `json:"metrics"`
	Nodes                         []NodeSpec        `json:"nodes"`
	PredecessorArtifactID         string            `json:"predecessor_artifact_id"`
	PredecessorPlanSHA256         string            `json:"predecessor_plan_sha256"`
	ProbeDeadlineMillis           int               `json:"probe_deadline_millis"`
	RequiredObservationGeneration uint64            `json:"required_observation_generation"`
	Roles                         []RoleAssignment  `json:"roles"`
	RouteDecisionSHA256           string            `json:"route_decision_sha256"`
	RouteID                       string            `json:"route_id"`
	Schema                        string            `json:"schema"`
}

type Evidence struct {
	DeploymentGeneration string `json:"deployment_generation"`
	GoldenSetSHA256      string `json:"golden_set_sha256"`
	NodeSetSHA256        string `json:"node_set_sha256"`
	ProcessSetSHA256     string `json:"process_set_sha256"`
	ServedStampSHA256    string `json:"served_stamp_sha256"`
}

type Plan struct {
	ArtifactRegistrySHA256        string            `json:"artifact_registry_sha256"`
	Bundle                        BundleExpectation `json:"bundle"`
	Executables                   ExecutableSet     `json:"executables"`
	Evidence                      Evidence          `json:"evidence"`
	FirstGeneration               bool              `json:"first_generation"`
	FleetID                       string            `json:"fleet_id"`
	Generation                    uint64            `json:"generation"`
	Goldens                       []GoldenSpec      `json:"goldens"`
	MaxObservationGeneration      uint64            `json:"max_observation_generation"`
	Metrics                       []MetricRule      `json:"metrics"`
	Nodes                         []NodeSpec        `json:"nodes"`
	PredecessorArtifactID         string            `json:"predecessor_artifact_id"`
	PredecessorPlanSHA256         string            `json:"predecessor_plan_sha256"`
	ProbeDeadlineMillis           int               `json:"probe_deadline_millis"`
	RegistryRoot                  string            `json:"registry_root"`
	RequiredObservationGeneration uint64            `json:"required_observation_generation"`
	Roles                         []RoleAssignment  `json:"roles"`
	RouteDecisionSHA256           string            `json:"route_decision_sha256"`
	RouteID                       string            `json:"route_id"`
	Schema                        string            `json:"schema"`
	ArtifactID                    string            `json:"artifact_id"`
}

type PlanPrivate struct {
	RegistryRoot string    `json:"registry_root"`
	Schema       string    `json:"schema"`
	Spec         FleetSpec `json:"spec"`
}

type NodeState struct {
	BundleSHA256 string `json:"bundle_sha256"`
	ID           string `json:"id"`
	Noop         bool   `json:"noop"`
	Root         string `json:"root"`
}

type MetricValue struct {
	Name  string `json:"name"`
	Value int64  `json:"value"`
}

// ManagementSignal is the retained, allowlisted projection of one private
// loopback stats response. It deliberately drops the physical list name.
type ManagementSignal struct {
	Entries int
	Version string
}

type Observation struct {
	AuditPresent        bool          `json:"audit_present"`
	Decision            string        `json:"decision"`
	FreshnessGeneration uint64        `json:"freshness_generation"`
	GoldenPresent       bool          `json:"golden_present"`
	GoldenNodes         int           `json:"golden_nodes"`
	LiveProcessPairs    int           `json:"live_process_pairs"`
	ManagementTopology  string        `json:"management_topology"`
	Metrics             []MetricValue `json:"metrics"`
	ObservationSHA256   string        `json:"observation_sha256"`
	PlanSHA256          string        `json:"plan_sha256"`
	ProcessPresent      bool          `json:"process_present"`
	ReadyNodes          int           `json:"ready_nodes"`
	RequiredNodes       int           `json:"required_nodes"`
	Schema              string        `json:"schema"`
	ServedStampSHA256   string        `json:"served_stamp_sha256"`
}

type AuditRecord struct {
	ActorID           string `json:"actor_id"`
	Decision          string `json:"decision"`
	Generation        uint64 `json:"generation"`
	ObservationSHA256 string `json:"observation_sha256"`
	PlanSHA256        string `json:"plan_sha256"`
	PredecessorSHA256 string `json:"predecessor_sha256"`
	RecordSHA256      string `json:"record_sha256"`
	Role              string `json:"role"`
}

type Audit struct {
	Records []AuditRecord `json:"records"`
	Schema  string        `json:"schema"`
}

type State struct {
	ArtifactID            string      `json:"artifact_id"`
	AuditSHA256           string      `json:"audit_sha256"`
	Evidence              Evidence    `json:"evidence"`
	FleetID               string      `json:"fleet_id"`
	Generation            uint64      `json:"generation"`
	Nodes                 []NodeState `json:"nodes"`
	ObservationSHA256     string      `json:"observation_sha256"`
	PlanSHA256            string      `json:"plan_sha256"`
	PredecessorArtifactID string      `json:"predecessor_artifact_id"`
	RouteDecisionSHA256   string      `json:"route_decision_sha256"`
	Schema                string      `json:"schema"`
	Status                string      `json:"status"`
}

type RecoverySpec struct {
	EvidenceReviewerActorID   string `json:"evidence_reviewer_actor_id"`
	ExpectedServedStampSHA256 string `json:"expected_served_stamp_sha256"`
	ExpectedStateSHA256       string `json:"expected_state_sha256"`
	Mode                      string `json:"mode"`
	RecoveryGeneration        uint64 `json:"recovery_generation"`
	RecoveryOperatorActorID   string `json:"recovery_operator_actor_id"`
	Schema                    string `json:"schema"`
	StepDeadlineMillis        int    `json:"step_deadline_millis"`
	TargetArtifactID          string `json:"target_artifact_id"`
}

type BackupFile struct {
	Mode   uint32 `json:"mode"`
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type Recovery struct {
	BackupSHA256       string       `json:"backup_sha256"`
	Files              []BackupFile `json:"files"`
	Generation         uint64       `json:"generation"`
	RecoveredNodes     int          `json:"recovered_nodes"`
	Schema             string       `json:"schema"`
	ServedStampSHA256  string       `json:"served_stamp_sha256"`
	StateSHA256        string       `json:"state_sha256"`
	Status             string       `json:"status"`
	StepCount          int          `json:"step_count"`
	StepDeadlineMillis int          `json:"step_deadline_millis"`
}

type DecisionApproval struct {
	ActorID                string `json:"actor_id"`
	Decision               string `json:"decision"`
	Generation             uint64 `json:"generation"`
	PredecessorPlanSHA256  string `json:"predecessor_plan_sha256"`
	Role                   string `json:"role"`
	ScopeID                string `json:"scope_id"`
	ValidThroughGeneration uint64 `json:"valid_through_generation"`
}

type ExceptionRecord struct {
	ExceptionID            string `json:"exception_id"`
	ExpiresAfterGeneration uint64 `json:"expires_after_generation"`
	OpenedGeneration       uint64 `json:"opened_generation"`
	OwnerActorID           string `json:"owner_actor_id"`
	ProofGapID             string `json:"proof_gap_id"`
	ScopeID                string `json:"scope_id"`
	State                  string `json:"state"`
}

type PostChangeRecord struct {
	ActionID     string `json:"action_id"`
	Generation   uint64 `json:"generation"`
	IncidentID   string `json:"incident_id"`
	OwnerActorID string `json:"owner_actor_id"`
	ProofGapID   string `json:"proof_gap_id"`
	ScopeID      string `json:"scope_id"`
	State        string `json:"state"`
}

type OwnershipLedger struct {
	Approvals   []DecisionApproval `json:"approvals"`
	Exceptions  []ExceptionRecord  `json:"exceptions"`
	PostChanges []PostChangeRecord `json:"post_changes"`
	Schema      string             `json:"schema"`
}

type Result struct {
	BackupSHA256      string `json:"backup_sha256"`
	Decision          string `json:"decision"`
	FleetID           string `json:"fleet_id"`
	Generation        uint64 `json:"generation"`
	Noop              bool   `json:"noop"`
	NoopNodes         int    `json:"noop_nodes"`
	PlanSHA256        string `json:"plan_sha256"`
	RecoveredNodes    int    `json:"recovered_nodes"`
	Schema            string `json:"schema"`
	ServedStampSHA256 string `json:"served_stamp_sha256"`
	Status            string `json:"status"`
}
