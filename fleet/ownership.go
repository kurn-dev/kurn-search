package fleet

var requiredDecisions = []struct {
	decision string
	role     string
}{
	{"CONVERGENCE", "service-operator"},
	{"EVIDENCE", "evidence-reviewer"},
	{"INCIDENT", "incident-commander"},
	{"PLAN", "change-owner"},
	{"RECOVERY", "recovery-operator"},
}

func MarshalOwnershipLedger(value OwnershipLedger) ([]byte, error) { return canonical(value) }

// ValidateOwnershipLedger checks only deterministic fixture ownership. Actor
// labels are deliberately not credentials or authentication claims.
func ValidateOwnershipLedger(spec FleetSpec, ledger OwnershipLedger) error {
	if spec.Schema != SpecSchema || !validName(spec.FleetID) || spec.Generation == 0 ||
		validateRoles(spec.FleetID, spec.Roles) != nil || ledger.Schema != OwnershipSchema || len(ledger.Approvals) != len(requiredDecisions) {
		return errRefused
	}
	actors := make(map[string]string, len(spec.Roles))
	for _, assignment := range spec.Roles {
		actors[assignment.Role] = assignment.ActorID
	}
	for index, approval := range ledger.Approvals {
		required := requiredDecisions[index]
		if approval.Decision != required.decision || approval.Role != required.role || approval.ActorID != actors[required.role] ||
			approval.ScopeID != spec.FleetID || approval.Generation != spec.Generation || approval.ValidThroughGeneration < spec.Generation ||
			approval.ValidThroughGeneration > spec.Generation+1_000 || approval.PredecessorPlanSHA256 != spec.PredecessorPlanSHA256 {
			return errRefused
		}
	}
	previous := ""
	for _, exception := range ledger.Exceptions {
		if !validName(exception.ExceptionID) || exception.ExceptionID <= previous || exception.OwnerActorID != actors["data-security-owner"] ||
			exception.ScopeID != spec.FleetID || !validOpaque(exception.ProofGapID) || exception.OpenedGeneration == 0 ||
			exception.OpenedGeneration > spec.Generation || exception.ExpiresAfterGeneration < spec.Generation ||
			exception.ExpiresAfterGeneration > spec.Generation+1_000 || exception.State != "RESOLVED" {
			return errRefused
		}
		previous = exception.ExceptionID
	}
	previous = ""
	for _, record := range ledger.PostChanges {
		if !validName(record.ActionID) || record.ActionID <= previous || !validName(record.IncidentID) ||
			record.OwnerActorID != actors["incident-commander"] || record.ScopeID != spec.FleetID || !validOpaque(record.ProofGapID) ||
			record.Generation != spec.Generation || record.State != "CLOSED" {
			return errRefused
		}
		previous = record.ActionID
	}
	return nil
}
