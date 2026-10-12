package partscatalogsuggest

import (
	"math"
	"slices"
	"sort"

	"github.com/kurn-dev/kurn-search/adapter"
)

// Semantics is the stateless pure parts-catalog-suggest adapter, under
// the default policy or an opt-in one (see PolicyOSA1).
type Semantics struct{ policy string }

// New returns the pure adapter value under the default policy.
func New() Semantics { return Semantics{} }

// NewWithPolicy returns the adapter under policy ("" is New()).
func NewWithPolicy(policy string) (Semantics, error) {
	if !validPolicy(policy) {
		return Semantics{}, invalidInput()
	}
	return Semantics{policy: policy}, nil
}

// Policy returns the adapter's policy ("" for the default).
func (semantics Semantics) Policy() string { return semantics.policy }

// Project emits the one raw identifier key used by the dedicated ngram list.
func (Semantics) Project(source []byte) ([]adapter.ProjectedEntry, error) {
	record, err := parseSource(source)
	if err != nil {
		return nil, err
	}
	keys, err := projectKeys(record.Identifier)
	if err != nil {
		return nil, err
	}
	payload, err := canonicalSource(record)
	if err != nil {
		return nil, invalidInput()
	}
	entry, err := adapter.NewProjectedEntry(adapter.ProjectedEntrySpec{
		EntryID: record.ID, Keys: keys, Payload: payload,
	})
	if err != nil {
		return nil, invalidInput()
	}
	return []adapter.ProjectedEntry{entry}, nil
}

func (Semantics) Compile(requestBytes, scopeBytes []byte) (adapter.CompiledPlan, error) {
	request, err := parseRequest(requestBytes)
	if err != nil {
		return adapter.CompiledPlan{}, err
	}
	scope, err := parseScope(scopeBytes)
	if err != nil {
		return adapter.CompiledPlan{}, invalidInput()
	}
	return compileParsed(request, scope)
}

// compileParsed validates the membership filter and builds the plan from an
// already parsed request and trusted scope.
func compileParsed(request searchRequest, scope trustedScope) (adapter.CompiledPlan, error) {
	if _, err := membershipPlan(request, scope); err != nil {
		return adapter.CompiledPlan{}, err
	}
	probe, err := adapter.NewProbe(adapter.ProbeSpec{
		Kind: kindSimilarity, LogicalTerm: request.Query,
		PhysicalKey: request.Query, ListRole: listRole,
	})
	if err != nil {
		return adapter.CompiledPlan{}, invalidInput()
	}
	plan, err := adapter.NewCompiledPlan([]adapter.Probe{probe})
	if err != nil {
		return adapter.CompiledPlan{}, invalidInput()
	}
	return plan, nil
}

// fallbackProbe is the transposition step for a parsed request: one any-of
// probe over the query's adjacent-transposition variants, or false when the
// query has none. Under the union policy a variant equal to the query under
// the analyzer (two adjacent letters differing only in case) is left out:
// that key is the similarity step's own exact match, which the step runs
// beside, and a transposition hit is never the query itself.
func fallbackProbe(request searchRequest, policy string) (adapter.Probe, bool, error) {
	variants := transpositions(request.Query)
	if unionPolicy(policy) {
		query := analyzedKey(request.Query)
		variants = slices.DeleteFunc(variants, func(variant string) bool { return analyzedKey(variant) == query })
	}
	if len(variants) == 0 {
		return adapter.Probe{}, false, nil
	}
	probe, err := adapter.NewProbe(adapter.ProbeSpec{
		Kind: kindTransposition, LogicalTerm: request.Query,
		PhysicalKeys: variants, ListRole: listRole,
	})
	if err != nil {
		return adapter.Probe{}, false, invalidInput()
	}
	return probe, true, nil
}

// CompileFallback is the recipe's second step: when the similarity step
// yields no candidate and the query has adjacent-transposition variants,
// every variant is looked up by exact key equality in one any-of probe on
// the same list (its exact companion index), under the same filter and
// pool. Under the default policy "no candidate" means an empty raw pool;
// under osa1 it means no candidate accepted by the shared predicate, so a
// non-empty pool whose every candidate is rejected still falls back, while
// malformed or foreign evidence is an error, never a fallback; under the
// union policy the step is taken for every query with variants, after the
// same validation. Deterministic in the request, so Resolve recomputes it.
func (semantics Semantics) CompileFallback(requestBytes, scopeBytes []byte, plan adapter.CompiledPlan, observed []adapter.ProbeEvidence) (adapter.CompiledPlan, bool, error) {
	request, err := parseRequest(requestBytes)
	if err != nil {
		return adapter.CompiledPlan{}, false, err
	}
	scope, err := parseScope(scopeBytes)
	if err != nil {
		return adapter.CompiledPlan{}, false, invalidInput()
	}
	expected, err := compileParsed(request, scope)
	if err != nil || !samePlan(expected, plan) || len(observed) != 1 || !observed[0].Valid() ||
		!sameProbe(plan.Probes()[0], observed[0].Probe()) || !validPool(observed[0].RequestedPool()) {
		return adapter.CompiledPlan{}, false, invalidPlan()
	}
	if verifierPolicy(semantics.policy) {
		accepted, err := semantics.validateCandidates(request, scope, plan.Probes()[0], observed[0], false)
		if err != nil {
			return adapter.CompiledPlan{}, false, err
		}
		if len(accepted) > 0 && !unionPolicy(semantics.policy) {
			return adapter.CompiledPlan{}, false, nil
		}
	} else if len(observed[0].Candidates()) > 0 {
		return adapter.CompiledPlan{}, false, nil
	}
	probe, ok, err := fallbackProbe(request, semantics.policy)
	if err != nil || !ok {
		return adapter.CompiledPlan{}, false, err
	}
	fallback, err := adapter.NewCompiledPlan([]adapter.Probe{probe})
	if err != nil {
		return adapter.CompiledPlan{}, false, invalidInput()
	}
	return fallback, true, nil
}

func (semantics Semantics) Resolve(requestBytes, scopeBytes []byte, plan adapter.CompiledPlan, observed []adapter.ProbeEvidence) (adapter.Resolution, error) {
	request, err := parseRequest(requestBytes)
	if err != nil {
		return adapter.Resolution{}, err
	}
	scope, err := parseScope(scopeBytes)
	if err != nil {
		return adapter.Resolution{}, err
	}
	expected, err := compileParsed(request, scope)
	if err != nil || !plan.Valid() {
		return adapter.Resolution{}, invalidPlan()
	}
	// One step (the similarity probe), or two (similarity, then the
	// transposition any-of probe this adapter compiles for the request):
	// the plan must be exactly what Compile and CompileFallback produce,
	// and the fallback must have been taken exactly when the shared
	// predicate says so.
	probes := plan.Probes()
	transposition := false
	// retained is the union policy's accepted similarity hits, combined
	// with the transposition hits below.
	var retained []*ranked
	switch len(probes) {
	case 1:
		if !samePlan(expected, plan) || len(observed) != 1 {
			return adapter.Resolution{}, invalidPlan()
		}
	case 2:
		fallback, ok, ferr := fallbackProbe(request, semantics.policy)
		if ferr != nil || !ok || !sameProbe(expected.Probes()[0], probes[0]) || !sameProbe(fallback, probes[1]) || len(observed) != 2 {
			return adapter.Resolution{}, invalidPlan()
		}
		first := observed[0]
		if !first.Valid() || !sameProbe(probes[0], first.Probe()) || !validPool(first.RequestedPool()) ||
			first.RequestedPool() != observed[1].RequestedPool() {
			return adapter.Resolution{}, invalidEvidence()
		}
		if verifierPolicy(semantics.policy) {
			// The original evidence is retained and validated; the
			// fallback is legitimate only when nothing in it is accepted.
			accepted, err := semantics.validateCandidates(request, scope, probes[0], first, false)
			if err != nil {
				return adapter.Resolution{}, err
			}
			if len(accepted) > 0 && !unionPolicy(semantics.policy) {
				return adapter.Resolution{}, invalidEvidence()
			}
			if len(first.Candidates()) == first.RequestedPool() {
				return adapter.Resolution{}, incomplete()
			}
			if unionPolicy(semantics.policy) {
				retained = accepted
			}
		} else if len(first.Candidates()) != 0 {
			return adapter.Resolution{}, invalidEvidence()
		}
		transposition = true
	default:
		return adapter.Resolution{}, invalidPlan()
	}

	probe := probes[len(probes)-1]
	evidence := observed[len(observed)-1]
	pool := evidence.RequestedPool()
	if !evidence.Valid() || !sameProbe(probe, evidence.Probe()) || !validPool(pool) {
		return adapter.Resolution{}, invalidEvidence()
	}
	candidates := evidence.Candidates()
	if len(candidates) > pool {
		return adapter.Resolution{}, invalidEvidence()
	}
	values, err := semantics.validateCandidates(request, scope, probe, evidence, transposition)
	if err != nil {
		return adapter.Resolution{}, err
	}
	if len(candidates) == pool {
		return adapter.Resolution{}, incomplete()
	}
	if verifierPolicy(semantics.policy) && !transposition && (len(values) == 0 || unionPolicy(semantics.policy)) {
		// The predicate accepted nothing in a one-step plan (or the policy
		// is the union): the executor must have taken the fallback when
		// the query has variants.
		if _, ok, _ := fallbackProbe(request, semantics.policy); ok {
			return adapter.Resolution{}, invalidPlan()
		}
	}
	if transposition && unionPolicy(semantics.policy) {
		// Combine: one entry once; the transposition evidence (score 100,
		// marked) wins over the same entry's similarity evidence.
		found := make(map[string]struct{}, len(values))
		for _, item := range values {
			found[item.id] = struct{}{}
		}
		for _, item := range retained {
			if _, dup := found[item.id]; !dup {
				values = append(values, item)
			}
		}
	}
	sort.SliceStable(values, func(i, j int) bool { return compareRanked(values[i], values[j]) < 0 })
	if len(values) > request.Limit {
		values = values[:request.Limit]
	}
	hits := make([]adapter.ResolvedHit, 0, len(values))
	for _, item := range values {
		resultBytes, err := canonicalResult(item.record, item.score)
		if err != nil {
			return adapter.Resolution{}, invalidEvidence()
		}
		evidenceBytes, err := canonicalEvidence(item.score, item.exact, item.transposition, item.key)
		if err != nil {
			return adapter.Resolution{}, invalidEvidence()
		}
		hit, err := adapter.NewResolvedHit(adapter.ResolvedHitSpec{
			EntryID: item.id, Result: resultBytes, Evidence: evidenceBytes,
		})
		if err != nil {
			return adapter.Resolution{}, invalidEvidence()
		}
		hits = append(hits, hit)
	}
	resolution, err := adapter.NewResolution(adapter.ResolutionSpec{Complete: true, Hits: hits})
	if err != nil {
		return adapter.Resolution{}, invalidEvidence()
	}
	return resolution, nil
}

// validateCandidates checks every candidate of one probe's evidence
// structurally (validity, score bounds, closed payload, entry and tenant
// agreement, partition and filters, echoed key, unique IDs, the exact and
// transposition rules) and returns, ranked-ready, the ones the policy's
// acceptance predicate accepts. A structural failure is invalid evidence;
// a predicate rejection is an ordinary miss. Under the default policy the
// predicate accepts everything, so the returned set is every candidate.
func (semantics Semantics) validateCandidates(request searchRequest, scope trustedScope, probe adapter.Probe, evidence adapter.ProbeEvidence, transposition bool) ([]*ranked, error) {
	candidates := evidence.Candidates()
	minScore := policyMinScore(semantics.policy)
	seen := make(map[string]struct{}, len(candidates))
	values := make([]*ranked, 0, len(candidates))
	for _, candidate := range candidates {
		score := candidate.Score()
		if !candidate.Valid() || score < minScore || score > 100 || math.Trunc(score) != score {
			return nil, invalidEvidence()
		}
		if transposition && score != 100 {
			return nil, invalidEvidence()
		}
		if _, exists := seen[candidate.EntryID()]; exists {
			return nil, invalidEvidence()
		}
		seen[candidate.EntryID()] = struct{}{}
		record, err := parseSource(candidate.Payload())
		if err != nil || record.ID != candidate.EntryID() || record.TenantID != scope.TenantID ||
			candidate.EchoedKey() != record.Identifier || !matchesRequest(record, request) {
			return nil, invalidEvidence()
		}
		keys, err := projectKeys(record.Identifier)
		if err != nil || len(keys) != 1 || keys[0] != candidate.EchoedKey() {
			return nil, invalidEvidence()
		}
		exact := normalizedIdentifier(request.Query) == normalizedIdentifier(record.Identifier)
		if exact && score != 100 {
			return nil, invalidEvidence()
		}
		// A transposition hit is an identifier equal (under the analyzer)
		// to one of the probed variants, never to the query itself: that
		// identifier would have answered the similarity step.
		if transposition && (exact || !matchesVariant(probe.PhysicalKeys(), record.Identifier)) {
			return nil, invalidEvidence()
		}
		if !transposition && !accepts(semantics.policy, request.Query, keys) {
			continue
		}
		values = append(values, &ranked{
			id: candidate.EntryID(), record: record, score: int(score), exact: exact,
			transposition: transposition, key: candidate.EchoedKey(),
		})
	}
	return values, nil
}

func validPool(pool int) bool {
	return pool == 128 || pool == 256 || pool == 512 || pool == 1000
}

func samePlan(left, right adapter.CompiledPlan) bool {
	if !left.Valid() || !right.Valid() {
		return false
	}
	a, b := left.Probes(), right.Probes()
	return len(a) == len(b) && slices.EqualFunc(a, b, sameProbe)
}

func sameProbe(left, right adapter.Probe) bool { return adapter.SameProbe(left, right) }
