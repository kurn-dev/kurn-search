package lifecycle

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
)

var routeStates = map[string]bool{
	"DRAFT": true, "AUTHORIZED": true, "CANARY": true, "ACTIVE": true,
	"DRAINING": true, "ROLLED-BACK": true, "DRAINED": true,
}

var transitionTargets = map[string]map[string]string{
	"AUTHORIZE":      {"DRAFT": "AUTHORIZED"},
	"CANARY":         {"AUTHORIZED": "CANARY"},
	"ACTIVATE":       {"CANARY": "ACTIVE"},
	"DRAIN":          {"ACTIVE": "DRAINING"},
	"DRAIN-COMPLETE": {"DRAINING": "DRAINED"},
	"ROLLBACK": {
		"AUTHORIZED": "ROLLED-BACK", "CANARY": "ROLLED-BACK",
		"ACTIVE": "ROLLED-BACK", "DRAINING": "ROLLED-BACK",
	},
}

func applyRoute(registryPath, intentPath string) (RouteResult, error) {
	root, err := resolvedDirectory(registryPath, 0o700)
	if err != nil || validateRegistryLayout(root) != nil {
		return RouteResult{}, errRefused
	}
	intentFile, err := resolvedRegular(intentPath, MaxControlBytes)
	if err != nil || pathsOverlap(intentFile, root) {
		return RouteResult{}, errRefused
	}
	raw, err := readModeFile(intentFile, MaxControlBytes)
	if err != nil {
		return RouteResult{}, errRefused
	}
	var intent RouteIntent
	if decodeCanonical(raw, MaxControlBytes, &intent) != nil || validateRouteIntentShape(intent) != nil {
		return RouteResult{}, errRefused
	}
	artifact, err := verifyArtifactRoot(root, intent.ArtifactID)
	if err != nil {
		return RouteResult{}, errRefused
	}
	if validateRouteArtifactBindings(intent, artifact) != nil {
		return RouteResult{}, errRefused
	}
	routesRoot := filepath.Join(root, "routes")
	routeRoot := filepath.Join(routesRoot, intent.RouteID)
	_, routeErr := os.Lstat(routeRoot)
	if errors.Is(routeErr, os.ErrNotExist) {
		if validateRouteGenesis(intent) != nil {
			return RouteResult{}, errRefused
		}
		decision := decisionFromIntent(intent, "DRAFT")
		return writeGenesis(routesRoot, intent.RouteID, raw, decision)
	}
	if routeErr != nil {
		return RouteResult{}, errRefused
	}
	history, hashes, err := readRouteHistory(root, intent.RouteID)
	if err != nil || len(history) == 0 {
		return RouteResult{}, errRefused
	}
	current, currentSHA := history[len(history)-1], hashes[len(hashes)-1]
	if current.State == "DRAINED" || current.State == "ROLLED-BACK" || intent.Generation != current.Generation+1 ||
		intent.PredecessorRouteSHA256 != currentSHA || intent.PriorStateSHA256 != currentSHA {
		return RouteResult{}, errRefused
	}
	target := transitionTargets[intent.Transition][current.State]
	if target == "" || validateTransitionGuards(root, intent, current, target) != nil {
		return RouteResult{}, errRefused
	}
	decision := decisionFromIntent(intent, target)
	return appendDecision(routeRoot, raw, decision)
}

func inspectRoute(registryPath, routeID string) (RouteResult, error) {
	root, err := resolvedDirectory(registryPath, 0o700)
	if err != nil || validateRegistryLayout(root) != nil || !validName(routeID) {
		return RouteResult{}, errRefused
	}
	history, hashes, err := readRouteHistory(root, routeID)
	if err != nil || len(history) == 0 {
		return RouteResult{}, errRefused
	}
	latest := history[len(history)-1]
	if _, err := verifyArtifactRoot(root, latest.ArtifactID); err != nil {
		return RouteResult{}, errRefused
	}
	return RouteResult{ArtifactID: latest.ArtifactID, DecisionSHA256: hashes[len(hashes)-1], Generation: latest.Generation, RouteID: routeID, State: latest.State}, nil
}

func validateRouteIntentShape(intent RouteIntent) error {
	if intent.Schema != RouteIntentSchema || !validName(intent.RouteID) || intent.Generation < 1 || !validSHA256(intent.ArtifactID) ||
		(intent.PredecessorArtifactID != "" && !validSHA256(intent.PredecessorArtifactID)) ||
		(intent.PredecessorRouteSHA256 != "" && !validSHA256(intent.PredecessorRouteSHA256)) ||
		!validSHA256(intent.AdapterBehaviorSHA256) || !validSHA256(intent.OperationalProfileSHA256) || !validSHA256(intent.KurnSHA256) ||
		!validSHA256(intent.PhysicalListIntentSHA256) || !validSHA256(intent.AuthorityReceiptSHA256) || !validSHA256(intent.RouteEnvelopeSHA256) ||
		(intent.PriorStateSHA256 != "" && !validSHA256(intent.PriorStateSHA256)) ||
		(intent.NodeSetSHA256 != "" && !validSHA256(intent.NodeSetSHA256)) ||
		(intent.GoldenSetSHA256 != "" && !validSHA256(intent.GoldenSetSHA256)) ||
		(intent.ServedStampSHA256 != "" && !validSHA256(intent.ServedStampSHA256)) ||
		(intent.SuccessorStateSHA256 != "" && !validSHA256(intent.SuccessorStateSHA256)) ||
		(intent.SuccessorRouteID != "" && !validName(intent.SuccessorRouteID)) {
		return errRefused
	}
	if intent.Transition != "CREATE" {
		if _, ok := transitionTargets[intent.Transition]; !ok {
			return errRefused
		}
	}
	return nil
}

func validateRouteArtifactBindings(intent RouteIntent, artifact checkedArtifact) error {
	payload := artifact.envelope.Payload
	if intent.AdapterBehaviorSHA256 != payload.AdapterBehaviorSHA256 || intent.OperationalProfileSHA256 != payload.OperationalProfileSHA256 ||
		intent.KurnSHA256 != payload.KurnSHA256 || intent.RouteEnvelopeSHA256 != artifact.record.EnvelopeSHA256 ||
		intent.AuthorityReceiptSHA256 != artifact.record.ReceiptSHA256 || intent.PredecessorArtifactID != artifact.record.PredecessorArtifactID {
		return errRefused
	}
	return nil
}

func validateRouteGenesis(intent RouteIntent) error {
	if intent.Generation != 1 || intent.Transition != "CREATE" || intent.PredecessorRouteSHA256 != "" || intent.PriorStateSHA256 != "" ||
		intent.DeploymentGeneration != "" || intent.NodeSetSHA256 != "" || intent.GoldenSetSHA256 != "" || intent.ServedStampSHA256 != "" ||
		intent.CanaryDecision != "" || intent.SuccessorRouteID != "" || intent.SuccessorStateSHA256 != "" || intent.Inflight != 0 {
		return errRefused
	}
	return nil
}

func validateTransitionGuards(root string, intent RouteIntent, current RouteDecision, target string) error {
	if current.RouteID != intent.RouteID || !routeStates[current.State] || intent.PhysicalListIntentSHA256 == "" ||
		intent.PhysicalListIntentSHA256 != current.PhysicalListIntentSHA256 {
		return errRefused
	}
	if intent.Transition != "ROLLBACK" {
		if intent.ArtifactID != current.ArtifactID || intent.PredecessorArtifactID != current.PredecessorArtifactID ||
			intent.AdapterBehaviorSHA256 == "" || intent.OperationalProfileSHA256 == "" || intent.KurnSHA256 == "" {
			return errRefused
		}
	} else {
		if current.PredecessorArtifactID == "" || intent.ArtifactID != current.PredecessorArtifactID {
			return errRefused
		}
		if intent.DeploymentGeneration != current.DeploymentGeneration || intent.NodeSetSHA256 != current.NodeSetSHA256 ||
			intent.GoldenSetSHA256 != current.GoldenSetSHA256 || intent.ServedStampSHA256 != current.ServedStampSHA256 ||
			intent.SuccessorRouteID != "" || intent.SuccessorStateSHA256 != "" || intent.CanaryDecision != "" || intent.Inflight != 0 {
			return errRefused
		}
		return nil
	}
	switch target {
	case "AUTHORIZED":
		if intent.DeploymentGeneration != "" || intent.NodeSetSHA256 != "" || intent.GoldenSetSHA256 != "" || intent.ServedStampSHA256 != "" || intent.CanaryDecision != "" || intent.SuccessorRouteID != "" || intent.SuccessorStateSHA256 != "" || intent.Inflight != 0 {
			return errRefused
		}
	case "CANARY":
		if !validName(intent.DeploymentGeneration) || !validSHA256(intent.NodeSetSHA256) || !validSHA256(intent.GoldenSetSHA256) || intent.ServedStampSHA256 != "" || intent.CanaryDecision != "" || intent.SuccessorRouteID != "" || intent.SuccessorStateSHA256 != "" || intent.Inflight != 0 {
			return errRefused
		}
	case "ACTIVE":
		if intent.DeploymentGeneration != current.DeploymentGeneration || intent.NodeSetSHA256 != current.NodeSetSHA256 || intent.GoldenSetSHA256 != current.GoldenSetSHA256 ||
			!validSHA256(intent.ServedStampSHA256) || intent.CanaryDecision != "success" || intent.SuccessorRouteID != "" || intent.SuccessorStateSHA256 != "" || intent.Inflight != 0 {
			return errRefused
		}
	case "DRAINING":
		if intent.DeploymentGeneration != current.DeploymentGeneration || intent.NodeSetSHA256 != current.NodeSetSHA256 || intent.GoldenSetSHA256 != current.GoldenSetSHA256 || intent.ServedStampSHA256 != current.ServedStampSHA256 ||
			intent.CanaryDecision != "" || intent.SuccessorRouteID == "" || intent.SuccessorRouteID == intent.RouteID || !validSHA256(intent.SuccessorStateSHA256) || intent.Inflight != 0 {
			return errRefused
		}
		successor, err := inspectRoute(root, intent.SuccessorRouteID)
		if err != nil || successor.State != "ACTIVE" || successor.DecisionSHA256 != intent.SuccessorStateSHA256 || successor.ArtifactID == intent.ArtifactID {
			return errRefused
		}
	case "DRAINED":
		if intent.DeploymentGeneration != current.DeploymentGeneration || intent.NodeSetSHA256 != current.NodeSetSHA256 || intent.GoldenSetSHA256 != current.GoldenSetSHA256 || intent.ServedStampSHA256 != current.ServedStampSHA256 ||
			intent.CanaryDecision != "" || intent.SuccessorRouteID == "" || intent.SuccessorRouteID != current.SuccessorRouteID || intent.SuccessorStateSHA256 != current.SuccessorStateSHA256 || intent.Inflight != 0 {
			return errRefused
		}
	default:
		return errRefused
	}
	return nil
}

func decisionFromIntent(intent RouteIntent, state string) RouteDecision {
	raw, _ := marshalCanonical(intent)
	return RouteDecision{
		Schema: RouteDecisionSchema, RouteID: intent.RouteID, Generation: intent.Generation, State: state,
		ArtifactID: intent.ArtifactID, PredecessorArtifactID: intent.PredecessorArtifactID,
		PredecessorRouteSHA256: intent.PredecessorRouteSHA256, IntentSHA256: digestBytes(raw), PriorStateSHA256: intent.PriorStateSHA256,
		AuthorityReceiptSHA256: intent.AuthorityReceiptSHA256, RouteEnvelopeSHA256: intent.RouteEnvelopeSHA256,
		PhysicalListIntentSHA256: intent.PhysicalListIntentSHA256,
		DeploymentGeneration:     intent.DeploymentGeneration, NodeSetSHA256: intent.NodeSetSHA256,
		GoldenSetSHA256: intent.GoldenSetSHA256, ServedStampSHA256: intent.ServedStampSHA256,
		SuccessorRouteID: intent.SuccessorRouteID, SuccessorStateSHA256: intent.SuccessorStateSHA256,
		Transition: intent.Transition, Decision: "accepted",
	}
}

func writeGenesis(routesRoot, routeID string, intentRaw []byte, decision RouteDecision) (RouteResult, error) {
	temporary, err := os.MkdirTemp(routesRoot, ".route.tmp-")
	if err != nil || os.Chmod(temporary, 0o700) != nil {
		if temporary != "" {
			_ = os.RemoveAll(temporary)
		}
		return RouteResult{}, errRefused
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.RemoveAll(temporary)
		}
	}()
	decisionRaw, err := marshalCanonical(decision)
	if err != nil || writeGeneration(temporary, decision.Generation, intentRaw, decisionRaw) != nil || syncDirectory(temporary) != nil ||
		os.Rename(temporary, filepath.Join(routesRoot, routeID)) != nil || syncDirectory(routesRoot) != nil {
		return RouteResult{}, errRefused
	}
	keep = true
	return routeResult(decision, decisionRaw), nil
}

func appendDecision(routeRoot string, intentRaw []byte, decision RouteDecision) (RouteResult, error) {
	decisionRaw, err := marshalCanonical(decision)
	if err != nil {
		return RouteResult{}, errRefused
	}
	target := filepath.Join(routeRoot, routeGenerationName(decision.Generation))
	if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		return RouteResult{}, errRefused
	}
	temporary, err := os.MkdirTemp(routeRoot, ".generation.tmp-")
	if err != nil || os.Chmod(temporary, 0o700) != nil {
		if temporary != "" {
			_ = os.RemoveAll(temporary)
		}
		return RouteResult{}, errRefused
	}
	defer func() {
		_ = os.RemoveAll(temporary)
	}()
	if writePrivate(filepath.Join(temporary, "intent.json"), intentRaw) != nil || writePrivate(filepath.Join(temporary, "decision.json"), decisionRaw) != nil ||
		syncDirectory(temporary) != nil || os.Rename(temporary, target) != nil || syncDirectory(routeRoot) != nil {
		return RouteResult{}, errRefused
	}
	return routeResult(decision, decisionRaw), nil
}

func writeGeneration(routeRoot string, generation uint64, intentRaw, decisionRaw []byte) error {
	generationRoot := filepath.Join(routeRoot, routeGenerationName(generation))
	if err := os.Mkdir(generationRoot, 0o700); err != nil {
		return errRefused
	}
	if writePrivate(filepath.Join(generationRoot, "intent.json"), intentRaw) != nil ||
		writePrivate(filepath.Join(generationRoot, "decision.json"), decisionRaw) != nil || syncDirectory(generationRoot) != nil {
		return errRefused
	}
	return nil
}

func routeResult(decision RouteDecision, raw []byte) RouteResult {
	return RouteResult{ArtifactID: decision.ArtifactID, DecisionSHA256: digestBytes(raw), Generation: decision.Generation, RouteID: decision.RouteID, State: decision.State}
}

func routeGenerationName(generation uint64) string { return fmt.Sprintf("%020d", generation) }

func readRouteHistory(root, routeID string) ([]RouteDecision, []string, error) {
	if !validName(routeID) {
		return nil, nil, errRefused
	}
	routeRoot := filepath.Join(root, "routes", routeID)
	if _, err := resolvedDirectory(routeRoot, 0o700); err != nil {
		return nil, nil, errRefused
	}
	entries, err := os.ReadDir(routeRoot)
	if err != nil || len(entries) == 0 {
		return nil, nil, errRefused
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	history := make([]RouteDecision, 0, len(entries))
	hashes := make([]string, 0, len(entries))
	for index, entry := range entries {
		generation := uint64(index + 1)
		if entry.Name() != routeGenerationName(generation) || !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return nil, nil, errRefused
		}
		generationRoot := filepath.Join(routeRoot, entry.Name())
		if _, err := resolvedDirectory(generationRoot, 0o700); err != nil {
			return nil, nil, errRefused
		}
		generationEntries, err := os.ReadDir(generationRoot)
		if err != nil || len(generationEntries) != 2 || generationEntries[0].Name() != "decision.json" || generationEntries[1].Name() != "intent.json" {
			return nil, nil, errRefused
		}
		intentRaw, err := readModeFile(filepath.Join(generationRoot, "intent.json"), MaxControlBytes)
		if err != nil {
			return nil, nil, errRefused
		}
		var intent RouteIntent
		if decodeCanonical(intentRaw, MaxControlBytes, &intent) != nil || validateRouteIntentShape(intent) != nil || intent.RouteID != routeID || intent.Generation != generation {
			return nil, nil, errRefused
		}
		raw, err := readModeFile(filepath.Join(generationRoot, "decision.json"), MaxControlBytes)
		if err != nil {
			return nil, nil, errRefused
		}
		var decision RouteDecision
		if decodeCanonical(raw, MaxControlBytes, &decision) != nil || validateDecision(decision, routeID, generation) != nil {
			return nil, nil, errRefused
		}
		sha := digestBytes(raw)
		artifact, err := verifyArtifactRoot(root, decision.ArtifactID)
		if err != nil || validateRouteArtifactBindings(intent, artifact) != nil {
			return nil, nil, errRefused
		}
		if index == 0 {
			if decision.State != "DRAFT" || decision.Transition != "CREATE" || decision.PredecessorRouteSHA256 != "" || decision.PriorStateSHA256 != "" {
				return nil, nil, errRefused
			}
		} else {
			prior := history[index-1]
			priorSHA := hashes[index-1]
			target := transitionTargets[decision.Transition][prior.State]
			if target != decision.State || decision.PredecessorRouteSHA256 != priorSHA || decision.PriorStateSHA256 != priorSHA {
				return nil, nil, errRefused
			}
		}
		wantRaw, err := marshalCanonical(decisionFromIntent(intent, decision.State))
		if err != nil || string(wantRaw) != string(raw) {
			return nil, nil, errRefused
		}
		history = append(history, decision)
		hashes = append(hashes, sha)
	}
	return history, hashes, nil
}

func validateDecision(value RouteDecision, routeID string, generation uint64) error {
	if value.Schema != RouteDecisionSchema || value.RouteID != routeID || value.Generation != generation || !routeStates[value.State] ||
		!validSHA256(value.ArtifactID) || (value.PredecessorArtifactID != "" && !validSHA256(value.PredecessorArtifactID)) ||
		!validSHA256(value.IntentSHA256) || !validSHA256(value.AuthorityReceiptSHA256) || !validSHA256(value.RouteEnvelopeSHA256) || !validSHA256(value.PhysicalListIntentSHA256) ||
		(value.PredecessorRouteSHA256 != "" && !validSHA256(value.PredecessorRouteSHA256)) ||
		(value.PriorStateSHA256 != "" && !validSHA256(value.PriorStateSHA256)) || value.Decision != "accepted" {
		return errRefused
	}
	if (value.SuccessorRouteID != "" && !validName(value.SuccessorRouteID)) || (value.SuccessorStateSHA256 != "" && !validSHA256(value.SuccessorStateSHA256)) {
		return errRefused
	}
	if parsed, err := strconv.ParseUint(routeGenerationName(generation), 10, 64); err != nil || parsed != generation {
		return errRefused
	}
	return nil
}
