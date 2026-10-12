package catalogid

import (
	"bytes"
	"slices"
	"sort"
	"unicode/utf8"

	"github.com/kurn-dev/kurn-search/adapter"
	"github.com/kurn-dev/kurn-search/adapter/analyzer"
	"github.com/kurn-dev/kurn-search/internal/jsorder"
)

// Semantics is the stateless pure catalog-identifier adapter.
type Semantics struct{}

// New returns the pure adapter value.
func New() Semantics { return Semantics{} }

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
	if _, err := parseScope(scopeBytes); err != nil || whitespaceOnly(request.Query) || utf8.RuneCountInString(request.Query) > 256 {
		return adapter.CompiledPlan{}, invalidInput()
	}

	byIdentity := map[string]adapter.Probe{}
	add := func(kind adapter.EvidenceKind, term string) error {
		key, err := physicalKey(kind, term)
		if err != nil {
			return err
		}
		probe, err := adapter.NewProbe(adapter.ProbeSpec{
			Kind: kind, LogicalTerm: term, PhysicalKey: key, ListRole: listRole,
		})
		if err != nil {
			return invalidInput()
		}
		byIdentity[string(kind)+"\x00"+key] = probe
		return nil
	}

	exact, err := analyzed(analyzer.ExactNormalizer, request.Query)
	if err != nil {
		return adapter.CompiledPlan{}, err
	}
	if len(exact) == 1 && exact[0] != "" {
		if err := add(kindExact, exact[0]); err != nil {
			return adapter.CompiledPlan{}, err
		}
	}
	for _, token := range whitespaceTokens(request.Query) {
		edges, err := analyzed(analyzer.EdgeSearch, token)
		if err != nil {
			return adapter.CompiledPlan{}, err
		}
		for _, edge := range edges {
			length := utf8.RuneCountInString(edge)
			if length >= 3 && length <= 20 {
				if err := add(kindStarts, edge); err != nil {
					return adapter.CompiledPlan{}, err
				}
				if err := add(kindEnds, edge); err != nil {
					return adapter.CompiledPlan{}, err
				}
			}
		}
	}
	words, err := analyzed(analyzer.WordIndex, request.Query)
	if err != nil {
		return adapter.CompiledPlan{}, err
	}
	for _, word := range words {
		if err := add(kindWord, word); err != nil {
			return adapter.CompiledPlan{}, err
		}
	}

	probes := make([]adapter.Probe, 0, len(byIdentity))
	for _, probe := range byIdentity {
		probes = append(probes, probe)
	}
	sort.SliceStable(probes, func(i, j int) bool {
		if kindOrder[probes[i].Kind()] != kindOrder[probes[j].Kind()] {
			return kindOrder[probes[i].Kind()] < kindOrder[probes[j].Kind()]
		}
		return jsorder.Less(probes[i].LogicalTerm(), probes[j].LogicalTerm())
	})
	if len(probes) == 0 || len(probes) > 64 {
		return adapter.CompiledPlan{}, invalidInput()
	}
	plan, err := adapter.NewCompiledPlan(probes)
	if err != nil {
		return adapter.CompiledPlan{}, invalidInput()
	}
	return plan, nil
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
	expected, err := semantics.Compile(requestBytes, scopeBytes)
	if err != nil || !samePlan(expected, plan) || len(observed) != len(plan.Probes()) {
		return adapter.Resolution{}, invalidPlan()
	}

	probes := plan.Probes()
	pool := 0
	accumulated := make(map[string]*ranked)
	for index, item := range observed {
		if !item.Valid() || !sameProbe(probes[index], item.Probe()) {
			return adapter.Resolution{}, invalidEvidence()
		}
		if pool == 0 {
			pool = item.RequestedPool()
		} else if pool != item.RequestedPool() {
			return adapter.Resolution{}, invalidEvidence()
		}
		candidates := item.Candidates()
		if len(candidates) > pool {
			return adapter.Resolution{}, invalidEvidence()
		}
		if len(candidates) == pool {
			return adapter.Resolution{}, incomplete()
		}
		seen := make(map[string]struct{}, len(candidates))
		for _, candidate := range candidates {
			if !candidate.Valid() || candidate.Score() != 100 || candidate.EchoedKey() != probes[index].PhysicalKey() {
				return adapter.Resolution{}, invalidEvidence()
			}
			if _, exists := seen[candidate.EntryID()]; exists {
				return adapter.Resolution{}, invalidEvidence()
			}
			seen[candidate.EntryID()] = struct{}{}
			record, err := parseSource(candidate.Payload())
			if err != nil || record.ID != candidate.EntryID() || record.TenantID != scope.TenantID ||
				(request.partition && (!record.partition || record.PartitionID != request.PartitionID)) {
				return adapter.Resolution{}, invalidEvidence()
			}
			canonical, err := canonicalSource(record)
			if err != nil {
				return adapter.Resolution{}, invalidEvidence()
			}
			item := accumulated[candidate.EntryID()]
			if item == nil {
				item = &ranked{id: candidate.EntryID(), record: record, payload: canonical, evidence: newEvidence()}
				accumulated[candidate.EntryID()] = item
			} else if !bytes.Equal(item.payload, canonical) {
				return adapter.Resolution{}, invalidEvidence()
			}
			item.evidence.add(probes[index].Kind(), probes[index].LogicalTerm())
		}
	}

	values := make([]*ranked, 0, len(accumulated))
	for _, item := range accumulated {
		values = append(values, item)
	}
	sort.SliceStable(values, func(i, j int) bool { return compareRanked(values[i], values[j]) < 0 })
	if len(values) > request.Limit {
		values = values[:request.Limit]
	}
	hits := make([]adapter.ResolvedHit, 0, len(values))
	for _, item := range values {
		evidenceBytes, err := canonicalEvidence(item.evidence)
		if err != nil {
			return adapter.Resolution{}, invalidEvidence()
		}
		hit, err := adapter.NewResolvedHit(adapter.ResolvedHitSpec{
			EntryID: item.id, Result: item.payload, Evidence: evidenceBytes,
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

func samePlan(left, right adapter.CompiledPlan) bool {
	if !left.Valid() || !right.Valid() {
		return false
	}
	a, b := left.Probes(), right.Probes()
	return len(a) == len(b) && slices.EqualFunc(a, b, sameProbe)
}

func sameProbe(left, right adapter.Probe) bool {
	return left.Valid() && right.Valid() && left.Kind() == right.Kind() &&
		left.LogicalTerm() == right.LogicalTerm() && left.PhysicalKey() == right.PhysicalKey() &&
		left.ListRole() == right.ListRole()
}
