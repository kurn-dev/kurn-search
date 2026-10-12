package fleet

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/kurn-dev/kurn-search/lifecycle"
)

var requiredRoles = []string{
	"change-owner",
	"data-security-owner",
	"evidence-reviewer",
	"incident-commander",
	"node-operator",
	"recovery-operator",
	"release-operator",
	"service-operator",
}

func MarshalSpec(value FleetSpec) ([]byte, error)            { return canonical(value) }
func MarshalRecoverySpec(value RecoverySpec) ([]byte, error) { return canonical(value) }
func MarshalResult(value Result) ([]byte, error)             { return canonical(value) }
func DigestBytes(raw []byte) string                          { return digest(raw) }
func DeriveEvidence(value FleetSpec) (Evidence, error) {
	if validateSpec(value) != nil {
		return Evidence{}, errRefused
	}
	return deriveEvidence(value)
}

func PlanFleet(specPath, registryPath, out string) (Result, error) {
	spec, err := loadSpec(specPath)
	if err != nil {
		return Result{}, errRefused
	}
	registry, err := resolvedPrivateDirectory(registryPath)
	if err != nil || validateRoots(spec, registry, out) != nil {
		return Result{}, errRefused
	}
	artifact, err := lifecycle.VerifyRegistry(registry, spec.ArtifactID)
	if err != nil || artifact.ArtifactID != spec.ArtifactID || artifact.State != lifecycle.ArtifactState {
		return Result{}, errRefused
	}
	bundle, err := registryBundleExpectation(registry, spec.ArtifactID)
	if err != nil || bundle != spec.Bundle {
		return Result{}, errRefused
	}
	route, err := lifecycle.InspectRoute(registry, spec.RouteID)
	if err != nil || route.ArtifactID != spec.ArtifactID || route.DecisionSHA256 != spec.RouteDecisionSHA256 || route.State != "ACTIVE" {
		return Result{}, errRefused
	}
	evidence, err := deriveEvidence(spec)
	if err != nil {
		return Result{}, errRefused
	}
	plan := Plan{
		ArtifactID: spec.ArtifactID, ArtifactRegistrySHA256: artifact.RegistrySHA256,
		Bundle: spec.Bundle, Executables: spec.Executables, Evidence: evidence, FirstGeneration: spec.FirstGeneration,
		FleetID: spec.FleetID, Generation: spec.Generation, Goldens: cloneGoldens(spec.Goldens),
		MaxObservationGeneration: spec.MaxObservationGeneration, Metrics: cloneMetrics(spec.Metrics), Nodes: cloneNodes(spec.Nodes),
		PredecessorArtifactID: spec.PredecessorArtifactID, PredecessorPlanSHA256: spec.PredecessorPlanSHA256,
		ProbeDeadlineMillis: spec.ProbeDeadlineMillis, RegistryRoot: registry, RequiredObservationGeneration: spec.RequiredObservationGeneration,
		Roles: cloneRoles(spec.Roles), RouteDecisionSHA256: spec.RouteDecisionSHA256, RouteID: spec.RouteID, Schema: PlanSchema,
	}
	neutral := neutralPlan(plan)
	raw, err := canonical(neutral)
	privateRaw, privateErr := canonical(PlanPrivate{RegistryRoot: registry, Schema: PlanPrivateSchema, Spec: spec})
	if err != nil || privateErr != nil || privateTransaction(out, map[string][]byte{"plan.json": raw, "private.json": privateRaw}) != nil {
		return Result{}, errRefused
	}
	return Result{FleetID: spec.FleetID, Generation: spec.Generation, PlanSHA256: digest(raw), Schema: ResultSchema, Status: "PLANNED"}, nil
}

func loadSpec(path string) (FleetSpec, error) {
	raw, err := readRegular(path, MaxControlBytes, nil)
	if err != nil {
		return FleetSpec{}, errRefused
	}
	var spec FleetSpec
	if decode(raw, MaxControlBytes, &spec) != nil || validateSpec(spec) != nil {
		return FleetSpec{}, errRefused
	}
	return spec, nil
}

func validateSpec(spec FleetSpec) error {
	if spec.Schema != SpecSchema || !validName(spec.FleetID) || spec.Generation == 0 ||
		spec.RequiredObservationGeneration == 0 || spec.MaxObservationGeneration < spec.RequiredObservationGeneration ||
		spec.MaxObservationGeneration > 1_000_000 || spec.ProbeDeadlineMillis < 1 || spec.ProbeDeadlineMillis > 60_000 ||
		!validSHA(spec.ArtifactID) || !validSHA(spec.RouteDecisionSHA256) || !validName(spec.RouteID) || validateBundleExpectation(spec.Bundle) != nil {
		return errRefused
	}
	if spec.FirstGeneration {
		if spec.Generation != 1 || spec.PredecessorPlanSHA256 != "" {
			return errRefused
		}
	} else if spec.Generation < 2 || !validSHA(spec.PredecessorPlanSHA256) {
		return errRefused
	}
	if spec.PredecessorArtifactID != "" && !validSHA(spec.PredecessorArtifactID) {
		return errRefused
	}
	if validateExecutables(spec.Executables) != nil || validateNodes(spec.Nodes) != nil ||
		validateGoldens(spec.Goldens) != nil || validateMetrics(spec.Metrics) != nil || validateRoles(spec.FleetID, spec.Roles) != nil {
		return errRefused
	}
	return nil
}

func validateExecutables(set ExecutableSet) error {
	values := []struct {
		value Executable
		name  string
		sha   string
	}{
		{set.Kurn, "kurn", KurnSHA256},
		{set.Kurnd, "kurnd", KurndSHA256},
		{set.KurnSearchd, "kurn-searchd", KurnSearchdSHA256},
	}
	paths := make([]string, 0, len(values))
	for _, item := range values {
		value := item.value
		if value.Name != item.name || value.SHA256 != item.sha || value.SourceRevision != KurnSourceRevision || value.CompatibilityLabel != KurnCompatibility {
			return errRefused
		}
		path, actual, err := resolvedExecutable(value.Path)
		if err != nil || actual != value.SHA256 {
			return errRefused
		}
		paths = append(paths, path)
	}
	for i := range paths {
		for j := i + 1; j < len(paths); j++ {
			if paths[i] == paths[j] {
				return errRefused
			}
		}
	}
	return nil
}

func validateBundleExpectation(value BundleExpectation) error {
	if !validSHA(value.BaseSHA256) || !validSHA(value.ConfigSHA256) || value.Entries == 0 || value.Entries > 1_000_000_000 {
		return errRefused
	}
	return nil
}

func registryBundleExpectation(registry, artifactID string) (BundleExpectation, error) {
	root := filepath.Join(registry, "artifacts", artifactID)
	baseRaw, baseErr := readRegular(filepath.Join(root, "bundle", "base.jsonl"), MaxBundleBytes, modePointer(0o600))
	configRaw, configErr := readRegular(filepath.Join(root, "bundle", "config.json"), MaxBundleBytes, modePointer(0o600))
	manifestRaw, manifestErr := readRegular(filepath.Join(root, "bundle", "manifest.json"), MaxControlBytes, modePointer(0o600))
	envelopeRaw, envelopeErr := readRegular(filepath.Join(root, "envelope.json"), MaxControlBytes, modePointer(0o600))
	var manifest struct {
		V            int             `json:"v"`
		SHA256       string          `json:"sha256"`
		Collapsed    int             `json:"collapsed,omitempty"`
		VersionID    string          `json:"version_id"`
		Analyzer     string          `json:"analyzer"`
		Mode         string          `json:"mode"`
		ConfigSHA256 string          `json:"config_sha256,omitempty"`
		Entries      int             `json:"entries"`
		Keys         int             `json:"keys"`
		PrevSHA256   string          `json:"prev_sha256,omitempty"`
		Delta        json.RawMessage `json:"delta,omitempty"`
	}
	var envelope lifecycle.Envelope
	if baseErr != nil || configErr != nil || manifestErr != nil || envelopeErr != nil || rejectDuplicates(manifestRaw) != nil ||
		json.Unmarshal(manifestRaw, &manifest) != nil || rejectDuplicates(envelopeRaw) != nil || json.Unmarshal(envelopeRaw, &envelope) != nil ||
		manifest.V != 2 || manifest.Mode != "exact" || manifest.Entries < 1 || manifest.Entries > 1_000_000_000 ||
		manifest.SHA256 != digest(baseRaw) || manifest.ConfigSHA256 != kurnConfigIdentity(configRaw) ||
		envelope.Payload.BaseSHA256 != manifest.SHA256 || envelope.Payload.ConfigSHA256 != manifest.ConfigSHA256 {
		return BundleExpectation{}, errRefused
	}
	return BundleExpectation{BaseSHA256: manifest.SHA256, ConfigSHA256: manifest.ConfigSHA256, Entries: uint64(manifest.Entries)}, nil
}

func kurnConfigIdentity(raw []byte) string {
	if len(raw) < 2 || raw[len(raw)-1] != '\n' || raw[len(raw)-2] == '\n' || rejectDuplicates(raw[:len(raw)-1]) != nil || !json.Valid(raw[:len(raw)-1]) {
		return ""
	}
	compact := raw[:len(raw)-1]
	hash := sha256.New()
	hash.Write([]byte("kurn config v1\x00"))
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(compact)))
	hash.Write(length[:])
	hash.Write(compact)
	return hex.EncodeToString(hash.Sum(nil))
}

func validateCredential(path string) error {
	raw, err := readRegular(path, 64<<10, modePointer(0o600))
	if err != nil || len(raw) == 0 || !utf8.Valid(raw) || !bytes.Equal(raw, bytes.TrimSpace(raw)) || strings.ContainsAny(string(raw), "\r\n") {
		return errRefused
	}
	return nil
}

func validateNodes(nodes []NodeSpec) error {
	if len(nodes) < 3 || len(nodes) > MaxNodes {
		return errRefused
	}
	previous := ""
	for index, node := range nodes {
		if !validName(node.ID) || node.ID <= previous || !cleanAbsolute(node.Root) || !cleanAbsolute(node.BackendCredentialFile) ||
			node.BackendCredentialFile != filepath.Join(filepath.Dir(filepath.Dir(node.Root)), "backend.credential") || validateCredential(node.BackendCredentialFile) != nil {
			return errRefused
		}
		parent := filepath.Dir(node.Root)
		resolved, err := filepath.EvalSymlinks(parent)
		if err != nil || resolved != parent {
			return errRefused
		}
		for prior := 0; prior < index; prior++ {
			if overlap(node.Root, nodes[prior].Root) {
				return errRefused
			}
		}
		previous = node.ID
	}
	return nil
}

func validateGoldens(values []GoldenSpec) error {
	if len(values) != 1 || values[0].ID != NeutralGoldenID || values[0].ExpectedSHA256 != NeutralGoldenSHA256 {
		return errRefused
	}
	previous := ""
	for _, value := range values {
		if !validName(value.ID) || value.ID <= previous || !validSHA(value.ExpectedSHA256) {
			return errRefused
		}
		previous = value.ID
	}
	return nil
}

func validateMetrics(values []MetricRule) error {
	if len(values) != 2 || values[0].Name != "fleet_nodes_expected" || values[1].Name != "fleet_nodes_ready" {
		return errRefused
	}
	previous := ""
	for _, value := range values {
		if !metricRE.MatchString(value.Name) || value.Name <= previous || value.Minimum < 0 || value.Maximum < value.Minimum || value.Maximum > 1_000_000_000 {
			return errRefused
		}
		previous = value.Name
	}
	return nil
}

func validateRoles(fleetID string, values []RoleAssignment) error {
	if len(values) != len(requiredRoles) {
		return errRefused
	}
	actors := map[string]bool{}
	for index, value := range values {
		if value.Role != requiredRoles[index] || !validName(value.ActorID) || value.ScopeID != fleetID || actors[value.ActorID] {
			return errRefused
		}
		actors[value.ActorID] = true
	}
	return nil
}

func validateRoots(spec FleetSpec, registry, out string) error {
	if resolvedAbsent(out) != nil || overlap(out, registry) {
		return errRefused
	}
	for _, node := range spec.Nodes {
		if overlap(node.Root, registry) || overlap(node.Root, out) {
			return errRefused
		}
		for _, executable := range []Executable{spec.Executables.Kurn, spec.Executables.Kurnd, spec.Executables.KurnSearchd} {
			if overlap(node.Root, executable.Path) {
				return errRefused
			}
		}
	}
	return nil
}

func deriveEvidence(spec FleetSpec) (Evidence, error) {
	nodeIDs := make([]string, len(spec.Nodes))
	for index, node := range spec.Nodes {
		nodeIDs[index] = node.ID
	}
	nodeSet, err := domainDigest("kurn-search-fleet-node-set/v1", nodeIDs)
	if err != nil {
		return Evidence{}, errRefused
	}
	goldenSet, err := domainDigest("kurn-search-fleet-golden-set/v1", spec.Goldens)
	if err != nil {
		return Evidence{}, errRefused
	}
	identities := []struct {
		CompatibilityLabel string `json:"compatibility_label"`
		Name               string `json:"name"`
		SHA256             string `json:"sha256"`
		SourceRevision     string `json:"source_revision"`
	}{
		{spec.Executables.Kurn.CompatibilityLabel, spec.Executables.Kurn.Name, spec.Executables.Kurn.SHA256, spec.Executables.Kurn.SourceRevision},
		{spec.Executables.Kurnd.CompatibilityLabel, spec.Executables.Kurnd.Name, spec.Executables.Kurnd.SHA256, spec.Executables.Kurnd.SourceRevision},
		{spec.Executables.KurnSearchd.CompatibilityLabel, spec.Executables.KurnSearchd.Name, spec.Executables.KurnSearchd.SHA256, spec.Executables.KurnSearchd.SourceRevision},
	}
	processSet, err := domainDigest("kurn-search-fleet-process-set/v1", struct {
		Executables any      `json:"executables"`
		Nodes       []string `json:"nodes"`
	}{identities, nodeIDs})
	if err != nil {
		return Evidence{}, errRefused
	}
	deployment, err := domainDigest("kurn-search-fleet-deployment-generation/v1", struct {
		ArtifactID string `json:"artifact_id"`
		FleetID    string `json:"fleet_id"`
		Generation uint64 `json:"generation"`
		NodeSet    string `json:"node_set_sha256"`
	}{spec.ArtifactID, spec.FleetID, spec.Generation, nodeSet})
	if err != nil {
		return Evidence{}, errRefused
	}
	served, err := expectedServedStampDigest(spec.Nodes, spec.Bundle)
	if err != nil {
		return Evidence{}, errRefused
	}
	return Evidence{DeploymentGeneration: deployment, GoldenSetSHA256: goldenSet, NodeSetSHA256: nodeSet, ProcessSetSHA256: processSet, ServedStampSHA256: served}, nil
}

type servedStampIdentity struct {
	NodeID string `json:"node_id"`
	Stamp  string `json:"stamp"`
}

func expectedStoreStamp(bundle BundleExpectation) string {
	return fmt.Sprintf("%s@%d+j0+c%s", bundle.BaseSHA256, bundle.Entries, bundle.ConfigSHA256)
}

func expectedServedStampDigest(nodes []NodeSpec, bundle BundleExpectation) (string, error) {
	values := make([]servedStampIdentity, len(nodes))
	stamp := expectedStoreStamp(bundle)
	for index, node := range nodes {
		values[index] = servedStampIdentity{NodeID: node.ID, Stamp: stamp}
	}
	return observedServedStampDigest(values)
}

func observedServedStampDigest(values []servedStampIdentity) (string, error) {
	if len(values) < 3 || len(values) > MaxNodes {
		return "", errRefused
	}
	previous := ""
	for _, value := range values {
		if !validName(value.NodeID) || value.NodeID <= previous || len(value.Stamp) == 0 || len(value.Stamp) > 512 {
			return "", errRefused
		}
		previous = value.NodeID
	}
	return domainDigest("kurn-search-fleet-observed-served-stamps/v1", values)
}

func loadPlanDir(path string) (Plan, []byte, []byte, error) {
	if exactDirectory(path, []string{"plan.json", "private.json"}) != nil {
		return Plan{}, nil, nil, errRefused
	}
	raw, err := readRegular(filepath.Join(path, "plan.json"), MaxControlBytes, modePointer(0o600))
	if err != nil {
		return Plan{}, nil, nil, errRefused
	}
	privateRaw, err := readRegular(filepath.Join(path, "private.json"), MaxControlBytes, modePointer(0o600))
	if err != nil {
		return Plan{}, nil, nil, errRefused
	}
	plan, err := decodePlanPair(raw, privateRaw)
	if err != nil {
		return Plan{}, nil, nil, errRefused
	}
	return plan, raw, privateRaw, nil
}

func decodePlanPair(raw, privateRaw []byte) (Plan, error) {
	var neutral Plan
	var private PlanPrivate
	if decode(raw, MaxControlBytes, &neutral) != nil || decode(privateRaw, MaxControlBytes, &private) != nil ||
		private.Schema != PlanPrivateSchema || validateSpec(private.Spec) != nil || !cleanAbsolute(private.RegistryRoot) {
		return Plan{}, errRefused
	}
	plan := neutral
	plan.RegistryRoot = private.RegistryRoot
	plan.Executables.Kurn.Path = private.Spec.Executables.Kurn.Path
	plan.Executables.Kurnd.Path = private.Spec.Executables.Kurnd.Path
	plan.Executables.KurnSearchd.Path = private.Spec.Executables.KurnSearchd.Path
	if len(plan.Nodes) != len(private.Spec.Nodes) {
		return Plan{}, errRefused
	}
	for index := range plan.Nodes {
		plan.Nodes[index].BackendCredentialFile = private.Spec.Nodes[index].BackendCredentialFile
		plan.Nodes[index].Root = private.Spec.Nodes[index].Root
	}
	if validatePlan(plan) != nil {
		return Plan{}, errRefused
	}
	wantNeutral := neutralPlan(plan)
	wantRaw, err := canonical(wantNeutral)
	if err != nil || !bytes.Equal(raw, wantRaw) || !reflect.DeepEqual(private.Spec, specFromPlan(plan)) {
		return Plan{}, errRefused
	}
	return plan, nil
}

func neutralPlan(plan Plan) Plan {
	value := plan
	value.RegistryRoot = ""
	value.Nodes = cloneNodes(plan.Nodes)
	for index := range value.Nodes {
		value.Nodes[index].Root = ""
		value.Nodes[index].BackendCredentialFile = ""
	}
	value.Executables.Kurn.Path = ""
	value.Executables.Kurnd.Path = ""
	value.Executables.KurnSearchd.Path = ""
	return value
}

func specFromPlan(plan Plan) FleetSpec {
	return FleetSpec{ArtifactID: plan.ArtifactID, Bundle: plan.Bundle, Executables: plan.Executables, FirstGeneration: plan.FirstGeneration, FleetID: plan.FleetID,
		Generation: plan.Generation, Goldens: cloneGoldens(plan.Goldens), MaxObservationGeneration: plan.MaxObservationGeneration,
		Metrics: cloneMetrics(plan.Metrics), Nodes: cloneNodes(plan.Nodes), PredecessorArtifactID: plan.PredecessorArtifactID,
		PredecessorPlanSHA256: plan.PredecessorPlanSHA256, ProbeDeadlineMillis: plan.ProbeDeadlineMillis, RequiredObservationGeneration: plan.RequiredObservationGeneration,
		Roles: cloneRoles(plan.Roles), RouteDecisionSHA256: plan.RouteDecisionSHA256, RouteID: plan.RouteID, Schema: SpecSchema}
}

func validatePlan(plan Plan) error {
	if plan.Schema != PlanSchema || !validSHA(plan.ArtifactRegistrySHA256) || !validSHA(plan.RouteDecisionSHA256) || !validSHA(plan.Evidence.DeploymentGeneration) ||
		!validSHA(plan.Evidence.NodeSetSHA256) || !validSHA(plan.Evidence.GoldenSetSHA256) || !validSHA(plan.Evidence.ProcessSetSHA256) || !validSHA(plan.Evidence.ServedStampSHA256) ||
		!cleanAbsolute(plan.RegistryRoot) {
		return errRefused
	}
	spec := specFromPlan(plan)
	if validateSpec(spec) != nil {
		return errRefused
	}
	want, err := deriveEvidence(spec)
	if err != nil || want != plan.Evidence {
		return errRefused
	}
	return recheckPlanAuthority(plan)
}

func recheckPlanAuthority(plan Plan) error {
	registry, err := resolvedPrivateDirectory(plan.RegistryRoot)
	if err != nil {
		return errRefused
	}
	artifact, err := lifecycle.VerifyRegistry(registry, plan.ArtifactID)
	if err != nil || artifact.RegistrySHA256 != plan.ArtifactRegistrySHA256 {
		return errRefused
	}
	route, err := lifecycle.InspectRoute(registry, plan.RouteID)
	if err != nil || route.ArtifactID != plan.ArtifactID || route.DecisionSHA256 != plan.RouteDecisionSHA256 || route.State != "ACTIVE" {
		return errRefused
	}
	return validateExecutables(plan.Executables)
}

func roleActor(roles []RoleAssignment, role string) (string, error) {
	index := sort.Search(len(roles), func(index int) bool { return roles[index].Role >= role })
	if index >= len(roles) || roles[index].Role != role {
		return "", errRefused
	}
	return roles[index].ActorID, nil
}

func modePointer(value os.FileMode) *os.FileMode { return &value }

func cloneNodes(values []NodeSpec) []NodeSpec       { return append([]NodeSpec(nil), values...) }
func cloneGoldens(values []GoldenSpec) []GoldenSpec { return append([]GoldenSpec(nil), values...) }
func cloneMetrics(values []MetricRule) []MetricRule { return append([]MetricRule(nil), values...) }
func cloneRoles(values []RoleAssignment) []RoleAssignment {
	return append([]RoleAssignment(nil), values...)
}

func equalRaw(path string, raw []byte) bool {
	actual, err := readRegular(path, int64(len(raw)), modePointer(0o600))
	return err == nil && bytes.Equal(actual, raw)
}
