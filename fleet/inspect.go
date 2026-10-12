package fleet

import (
	"bytes"
	"path/filepath"
)

type checkedState struct {
	audit          Audit
	auditRaw       []byte
	observation    Observation
	observationRaw []byte
	plan           Plan
	planRaw        []byte
	state          State
	stateRaw       []byte
}

func Inspect(stateDir string) (Result, error) {
	checked, err := loadStateDir(stateDir)
	if err != nil {
		return Result{}, errRefused
	}
	noop := 0
	for _, node := range checked.state.Nodes {
		if node.Noop {
			noop++
		}
	}
	return Result{Decision: checked.observation.Decision, FleetID: checked.state.FleetID, Generation: checked.state.Generation,
		NoopNodes: noop, PlanSHA256: checked.state.PlanSHA256, Schema: ResultSchema,
		ServedStampSHA256: checked.state.Evidence.ServedStampSHA256, Status: "INSPECTED"}, nil
}

func loadStateDir(path string) (checkedState, error) {
	if exactDirectory(path, []string{"audit.json", "observation.json", "plan.json", "private.json", "state.json"}) != nil {
		return checkedState{}, errRefused
	}
	read := func(name string) ([]byte, error) {
		return readRegular(filepath.Join(path, name), MaxControlBytes, modePointer(0o600))
	}
	planRaw, err := read("plan.json")
	if err != nil {
		return checkedState{}, errRefused
	}
	privateRaw, err := read("private.json")
	if err != nil {
		return checkedState{}, errRefused
	}
	plan, err := decodePlanPair(planRaw, privateRaw)
	if err != nil {
		return checkedState{}, errRefused
	}
	observationRaw, err := read("observation.json")
	if err != nil {
		return checkedState{}, errRefused
	}
	var observation Observation
	if decode(observationRaw, MaxControlBytes, &observation) != nil || validateObservation(plan, observation, digest(planRaw)) != nil {
		return checkedState{}, errRefused
	}
	auditRaw, err := read("audit.json")
	if err != nil {
		return checkedState{}, errRefused
	}
	var audit Audit
	if decode(auditRaw, MaxControlBytes, &audit) != nil || validateAudit(plan, observation, audit, digest(planRaw)) != nil {
		return checkedState{}, errRefused
	}
	stateRaw, err := read("state.json")
	if err != nil {
		return checkedState{}, errRefused
	}
	var state State
	if decode(stateRaw, MaxControlBytes, &state) != nil || validateState(plan, observation, auditRaw, state, digest(planRaw)) != nil {
		return checkedState{}, errRefused
	}
	return checkedState{audit: audit, auditRaw: auditRaw, observation: observation, observationRaw: observationRaw,
		plan: plan, planRaw: planRaw, state: state, stateRaw: stateRaw}, nil
}

func validateObservation(plan Plan, observation Observation, planSHA string) error {
	if observation.PlanSHA256 != planSHA || observation.Decision == "" {
		return errRefused
	}
	identity := observation
	identity.ObservationSHA256 = ""
	wantSHA, err := domainDigest("kurn-search-fleet-observation/v1", identity)
	if err != nil || wantSHA != observation.ObservationSHA256 {
		return errRefused
	}
	wantDecision, err := EvaluateObservation(plan, observation)
	if err != nil || wantDecision != observation.Decision {
		return errRefused
	}
	return nil
}

func validateAudit(plan Plan, observation Observation, audit Audit, planSHA string) error {
	if audit.Schema != AuditSchema || len(audit.Records) != 1 {
		return errRefused
	}
	record := audit.Records[0]
	actor, err := roleActor(plan.Roles, "service-operator")
	if err != nil || record.ActorID != actor || record.Role != "service-operator" || record.Decision != observation.Decision ||
		record.Generation != plan.Generation || record.ObservationSHA256 != observation.ObservationSHA256 ||
		record.PlanSHA256 != planSHA || record.PredecessorSHA256 != plan.PredecessorPlanSHA256 || !validSHA(record.RecordSHA256) {
		return errRefused
	}
	identity := record
	identity.RecordSHA256 = ""
	want, err := domainDigest("kurn-search-fleet-audit-record/v1", identity)
	if err != nil || want != record.RecordSHA256 {
		return errRefused
	}
	return nil
}

func validateState(plan Plan, observation Observation, auditRaw []byte, state State, planSHA string) error {
	if state.Schema != StateSchema || state.Status != observation.Decision || state.Status != "HEALTHY" || state.PlanSHA256 != planSHA ||
		state.ArtifactID != plan.ArtifactID || state.AuditSHA256 != digest(auditRaw) || state.Evidence != plan.Evidence ||
		state.FleetID != plan.FleetID || state.Generation != plan.Generation || state.ObservationSHA256 != observation.ObservationSHA256 ||
		state.PredecessorArtifactID != plan.PredecessorArtifactID || state.RouteDecisionSHA256 != plan.RouteDecisionSHA256 || len(state.Nodes) != len(plan.Nodes) {
		return errRefused
	}
	for index, node := range state.Nodes {
		if node.ID != plan.Nodes[index].ID || node.Root != "" || !validSHA(node.BundleSHA256) {
			return errRefused
		}
		actual, err := verifyNodeRoot(plan.Nodes[index].Root)
		if err != nil || actual != node.BundleSHA256 {
			return errRefused
		}
	}
	return nil
}

func stateFilesEqual(left, right checkedState) bool {
	return bytes.Equal(left.planRaw, right.planRaw) && bytes.Equal(left.stateRaw, right.stateRaw) &&
		bytes.Equal(left.observationRaw, right.observationRaw) && bytes.Equal(left.auditRaw, right.auditRaw)
}
