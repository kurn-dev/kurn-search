package partscatalogsuggest

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"slices"

	"github.com/kurn-dev/kurn-search/adapter"
	"github.com/kurn-dev/kurn-search/adapters/partscatalog"
	"github.com/kurn-dev/kurn-search/internal/jsjson"
)

//go:embed testdata/neutral-fixture.json
var neutralFixture []byte

const fixtureSchema = "kurn-search/parts-catalog-suggest-fixture/v1"

type fixtureRecord struct {
	ID           string `json:"id"`
	Identifier   string `json:"identifier"`
	TenantID     string `json:"tenant_id"`
	Manufacturer string `json:"manufacturer"`
	Category     string `json:"category"`
	Status       string `json:"status"`
	PartitionID  string `json:"partition_id,omitempty"`
}

type fixtureRequest struct {
	Query       string         `json:"query"`
	Filters     map[string]any `json:"filters,omitempty"`
	PartitionID string         `json:"partition_id,omitempty"`
	Limit       int            `json:"limit,omitempty"`
}

type fixtureScope struct {
	TenantID string `json:"tenant_id"`
}

type fixtureCandidate struct {
	EntryID string `json:"entry_id"`
	Score   int    `json:"score"`
}

type fixtureQuery struct {
	ID         string             `json:"id"`
	Candidates []fixtureCandidate `json:"candidates"`
	// Policies lists the policies the query is evaluated under; absent
	// means every policy. Queries that discriminate an opt-in policy are
	// listed under it only, so the default artifacts stay what they were.
	Policies []string `json:"policies,omitempty"`
	// FallbackCandidates is present exactly for the queries whose
	// similarity step is empty and whose query has transposition variants:
	// the candidates the transposition any-of probe observes (possibly
	// none).
	FallbackCandidates *[]fixtureCandidate `json:"fallback_candidates,omitempty"`
	ExpectedIDs        []string            `json:"expected_ids"`
	Request            fixtureRequest      `json:"request"`
	Scope              fixtureScope        `json:"scope"`
}

type fixtureFile struct {
	Schema              string          `json:"schema"`
	Records             []fixtureRecord `json:"-"`
	SourceFixtureSHA256 string          `json:"source_fixture_sha256"`
	Queries             []fixtureQuery  `json:"queries"`
}

// ArtifactBytes are the four independently identified parts semantic components.
type ArtifactBytes struct {
	SourceProjection []byte
	QueryCodec       []byte
	CompiledPlan     []byte
	ResolvedResult   []byte
}

// NeutralFixture returns a defensive copy of the approved synthetic input.
func NeutralFixture() []byte { return slices.Clone(neutralFixture) }

// GenerateArtifacts deterministically evaluates the neutral fixture through
// the pure adapter under the default policy and emits the four exact
// semantic artifact components.
func GenerateArtifacts() (ArtifactBytes, error) { return GenerateArtifactsFor("") }

// GenerateArtifactsFor is GenerateArtifacts under policy: the fixture
// queries listed for that policy (or for every policy) are evaluated with
// the policy's semantics, so each policy has its own behaviour identity.
func GenerateArtifactsFor(policy string) (ArtifactBytes, error) {
	semantics, err := NewWithPolicy(policy)
	if err != nil {
		return ArtifactBytes{}, err
	}
	fixture, err := loadFixture()
	if err != nil {
		return ArtifactBytes{}, err
	}
	entries := make([]adapter.ProjectedEntry, 0, len(fixture.Records))
	entryObjects := make([]any, 0, len(fixture.Records))
	for _, record := range fixture.Records {
		raw, err := encodeFixtureRecord(record)
		if err != nil {
			return ArtifactBytes{}, err
		}
		projected, err := semantics.Project(raw)
		if err != nil || len(projected) != 1 {
			return ArtifactBytes{}, errors.New("partscatalogsuggest: neutral projection failed")
		}
		entry := projected[0]
		entries = append(entries, entry)
		entryObjects = append(entryObjects, jsjson.Object{
			"id": entry.EntryID(), "keys": entry.Keys(), "payload": objectForRecord(record),
		})
	}

	codecQueries := make([]any, 0, len(fixture.Queries))
	compiledQueries := make([]any, 0, len(fixture.Queries))
	answers := make([]any, 0, len(fixture.Queries))
	for _, query := range fixture.Queries {
		if len(query.Policies) > 0 && !slices.Contains(query.Policies, policy) {
			continue
		}
		requestBytes, scopeBytes, err := encodeFixtureQuery(query)
		if err != nil {
			return ArtifactBytes{}, err
		}
		plan, err := semantics.Compile(requestBytes, scopeBytes)
		if err != nil {
			return ArtifactBytes{}, errors.New("partscatalogsuggest: neutral compilation failed")
		}
		filter, err := semantics.CompileMembershipFilter(requestBytes, scopeBytes)
		if err != nil {
			return ArtifactBytes{}, err
		}
		evidence, err := synthesizeEvidence(entries, plan.Probes()[0], query.Candidates, 1000)
		if err != nil {
			return ArtifactBytes{}, err
		}
		// The fallback step is taken exactly where the fixture declares it;
		// under the union policy it is taken for every query with variants,
		// and a query that declares no fallback candidates observes none.
		runPlan := plan
		fallback, taken, err := semantics.CompileFallback(requestBytes, scopeBytes, plan, evidence)
		if err != nil {
			return ArtifactBytes{}, errors.New("partscatalogsuggest: neutral fallback compilation failed")
		}
		declared := query.FallbackCandidates
		if taken && declared == nil && unionPolicy(policy) {
			declared = &[]fixtureCandidate{}
		}
		if taken != (declared != nil) {
			return ArtifactBytes{}, errors.New("partscatalogsuggest: fixture fallback inventory mismatch")
		}
		if taken {
			fallbackEvidence, err := synthesizeEvidence(entries, fallback.Probes()[0], *declared, 1000)
			if err != nil {
				return ArtifactBytes{}, err
			}
			runPlan, err = adapter.NewCompiledPlan(append(plan.Probes(), fallback.Probes()...))
			if err != nil {
				return ArtifactBytes{}, err
			}
			evidence = append(evidence, fallbackEvidence...)
		}
		probeObjects := objectsForProbes(runPlan.Probes(), true)
		codecQueries = append(codecQueries, jsjson.Object{
			"id": query.ID, "terms": objectsForProbes(runPlan.Probes(), false),
			"request": objectForRequest(query.Request),
		})
		compiledQueries = append(compiledQueries, jsjson.Object{
			"compiled": jsjson.Object{
				"contract": "kurn-search/parts-catalog-suggest/v1",
				"probes":   probeObjects,
				"query":    query.Request.Query,
				"filter":   objectsForFilter(filter),
			},
			"id": query.ID,
		})
		resolution, err := semantics.Resolve(requestBytes, scopeBytes, runPlan, evidence)
		if err != nil {
			return ArtifactBytes{}, errors.New("partscatalogsuggest: neutral resolution failed")
		}
		ids := make([]string, 0, len(resolution.Hits()))
		for _, hit := range resolution.Hits() {
			ids = append(ids, hit.EntryID())
		}
		if !slices.Equal(ids, query.ExpectedIDs) {
			return ArtifactBytes{}, errors.New("partscatalogsuggest: frozen answer mismatch")
		}
		resultObjects, err := objectsForHits(resolution.Hits())
		if err != nil {
			return ArtifactBytes{}, err
		}
		answers = append(answers, jsjson.Object{"id": query.ID, "results": resultObjects})
	}

	projection, err := jsjson.Encode(jsjson.Object{
		"entries": entryObjects, "schema": "kurn-search/parts-catalog-suggest-derived-entries/v1",
	})
	if err != nil {
		return ArtifactBytes{}, err
	}
	codec, err := jsjson.Encode(jsjson.Object{
		"selection": jsjson.Object{"within_field": "or", "across_fields": "and", "literal": "case-sensitive-untrimmed", "max_category": 16, "max_manufacturer": 16, "max_status": 2, "max_canonical_bytes": 16384},
		"queries":   codecQueries, "schema": "kurn-search/parts-catalog-suggest-query-codec/v1",
	})
	if err != nil {
		return ArtifactBytes{}, err
	}
	compiled, err := jsjson.Encode(jsjson.Object{
		"queries": compiledQueries, "schema": "kurn-search/parts-catalog-suggest-compiled-plans/v1",
	})
	if err != nil {
		return ArtifactBytes{}, err
	}
	resolved, err := jsjson.Encode(jsjson.Object{
		"answers": answers, "schema": "kurn-search/parts-catalog-suggest-resolved-results/v1",
	})
	if err != nil {
		return ArtifactBytes{}, err
	}
	return ArtifactBytes{
		SourceProjection: projection, QueryCodec: codec,
		CompiledPlan: compiled, ResolvedResult: resolved,
	}, nil
}

const sourceFixtureSHA256 = "d6b7e113dd01d43ec108ab8eee54ef4ea59af5d0cca22b3b0f3a058efc1aa57c"

// fixtureQueryCount is the neutral fixture's query inventory: the seven
// similarity queries, the two transposition-fallback queries, the two
// queries evaluated under the verifier policies only, and the two evaluated
// under the union policy only (a duplicate across branches; a case-adjacent
// query whose analyzer-equal variant is left out of the step).
const fixtureQueryCount = 13

func loadFixture() (fixtureFile, error) {
	if validateFixtureJSON(neutralFixture) != nil {
		return fixtureFile{}, invalidInput()
	}
	decoder := json.NewDecoder(bytes.NewReader(neutralFixture))
	decoder.DisallowUnknownFields()
	var fixture fixtureFile
	if decoder.Decode(&fixture) != nil || requireEOF(decoder) != nil || fixture.Schema != fixtureSchema ||
		fixture.SourceFixtureSHA256 != sourceFixtureSHA256 || len(fixture.Queries) != fixtureQueryCount {
		return fixtureFile{}, invalidInput()
	}
	source := partscatalog.NeutralFixture()
	if adapter.PlainSHA256(source).Hex() != sourceFixtureSHA256 {
		return fixtureFile{}, invalidInput()
	}
	var inherited struct {
		Records []json.RawMessage `json:"records"`
	}
	if json.Unmarshal(source, &inherited) != nil || len(inherited.Records) != 152 {
		return fixtureFile{}, invalidInput()
	}
	ids := map[string]bool{}
	for _, raw := range inherited.Records {
		record, err := parseSource(raw)
		if err != nil || ids[record.ID] {
			return fixtureFile{}, invalidInput()
		}
		ids[record.ID] = true
		var value fixtureRecord
		if json.Unmarshal(raw, &value) != nil {
			return fixtureFile{}, invalidInput()
		}
		fixture.Records = append(fixture.Records, value)
	}
	return fixture, nil
}

// validateFixtureCandidates checks one observed candidate list: unique
// bounded entry IDs and integer scores in [minimum, 100].
func validateFixtureCandidates(candidates []json.RawMessage, minimum int) error {
	candidateIDs := map[string]bool{}
	for _, raw := range candidates {
		candidate, err := parseMembers(raw, maxRequestBytes, []string{"entry_id", "score"}, []string{"entry_id", "score"})
		if err != nil {
			return invalidInput()
		}
		var entryID string
		var score int
		if !boundedString(candidate["entry_id"], &entryID) || candidateIDs[entryID] ||
			decodeInteger(candidate["score"], &score) != nil || score < minimum || score > 100 {
			return invalidInput()
		}
		candidateIDs[entryID] = true
	}
	return nil
}

func encodeFixtureRecord(record fixtureRecord) ([]byte, error) {
	return jsjson.Encode(objectForRecord(record))
}

func encodeFixtureQuery(query fixtureQuery) ([]byte, []byte, error) {
	request := objectForRequest(query.Request)
	requestBytes, err := jsjson.Encode(request)
	if err != nil {
		return nil, nil, err
	}
	scopeBytes, err := jsjson.Encode(jsjson.Object{"tenant_id": query.Scope.TenantID})
	return requestBytes, scopeBytes, err
}

func objectForRecord(record fixtureRecord) jsjson.Object {
	value := jsjson.Object{
		"id": record.ID, "identifier": record.Identifier, "tenant_id": record.TenantID,
		"manufacturer": record.Manufacturer, "category": record.Category, "status": record.Status,
	}
	if record.PartitionID != "" {
		value["partition_id"] = record.PartitionID
	}
	return value
}

func objectsForProbes(probes []adapter.Probe, includeRole bool) []any {
	values := make([]any, len(probes))
	for index, probe := range probes {
		value := jsjson.Object{
			"kind": string(probe.Kind()), "logicalTerm": probe.LogicalTerm(),
			"physicalKey": probe.PhysicalKey(),
		}
		if keys := probe.PhysicalKeys(); len(keys) > 0 {
			value["physicalKeys"] = keys
		}
		if includeRole {
			value["listRole"] = string(probe.ListRole())
		}
		values[index] = value
	}
	return values
}

func synthesizeEvidence(entries []adapter.ProjectedEntry, probe adapter.Probe, observedCandidates []fixtureCandidate, pool int) ([]adapter.ProbeEvidence, error) {
	if len(observedCandidates) > pool {
		return nil, invalidInput()
	}
	byID := make(map[string]adapter.ProjectedEntry, len(entries))
	for _, entry := range entries {
		byID[entry.EntryID()] = entry
	}
	candidates := make([]adapter.Candidate, 0, len(observedCandidates))
	for _, observed := range observedCandidates {
		entry, ok := byID[observed.EntryID]
		if !ok || len(entry.Keys()) != 1 {
			return nil, invalidInput()
		}
		candidate, err := adapter.NewCandidate(adapter.CandidateSpec{
			EntryID: entry.EntryID(), Score: float64(observed.Score),
			EchoedKey: entry.Keys()[0], Payload: entry.Payload(),
		})
		if err != nil {
			return nil, err
		}
		candidates = append(candidates, candidate)
	}
	item, err := adapter.NewProbeEvidence(adapter.ProbeEvidenceSpec{
		Probe: probe, RequestedPool: pool, Candidates: candidates,
	})
	if err != nil {
		return nil, err
	}
	return []adapter.ProbeEvidence{item}, nil
}

func objectsForHits(hits []adapter.ResolvedHit) ([]any, error) {
	values := make([]any, 0, len(hits))
	for _, hit := range hits {
		var payload, evidenceValue map[string]any
		if err := json.Unmarshal(hit.Result(), &payload); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(hit.Evidence(), &evidenceValue); err != nil {
			return nil, err
		}
		for _, object := range []map[string]any{payload, evidenceValue} {
			if value, ok := object["score"].(float64); ok {
				object["score"] = int(value)
			}
		}
		values = append(values, jsjson.Object{
			"entryId": hit.EntryID(), "evidence": evidenceValue, "payload": payload,
		})
	}
	return values, nil
}

func objectForRequest(q fixtureRequest) jsjson.Object {
	value := jsjson.Object{"query": q.Query}
	if q.Limit != 0 {
		value["limit"] = q.Limit
	}
	if q.PartitionID != "" {
		value["partition_id"] = q.PartitionID
	}
	if q.Filters != nil {
		fields := jsjson.Object{}
		for name, text := range q.Filters {
			fields[name] = text
		}
		value["filters"] = fields
	}
	return value
}

func objectsForFilter(p adapter.MembershipFilterPlan) jsjson.Object {
	trusted := make([]any, 0, len(p.Trusted()))
	for _, term := range p.Trusted() {
		trusted = append(trusted, jsjson.Object{"name": term.Name(), "value": term.Value()})
	}
	caller := make([]any, 0, len(p.Caller()))
	for _, term := range p.Caller() {
		caller = append(caller, jsjson.Object{"name": term.Name(), "values": term.Values()})
	}
	return jsjson.Object{"caller": caller, "trusted": trusted, "canonical": string(p.CanonicalJSON())}
}

func validateFixtureJSON(raw []byte) error {
	members, err := parseMembers(raw, 4<<20, []string{"schema", "source_fixture_sha256", "queries"}, []string{"schema", "source_fixture_sha256", "queries"})
	if err != nil {
		return invalidInput()
	}
	var queries []json.RawMessage
	if json.Unmarshal(members["queries"], &queries) != nil || len(queries) != fixtureQueryCount {
		return invalidInput()
	}
	ids := map[string]bool{}
	for _, raw := range queries {
		q, err := parseMembers(raw, maxRequestBytes, []string{"candidates", "expected_ids", "fallback_candidates", "id", "policies", "request", "scope"}, []string{"candidates", "expected_ids", "id", "request", "scope"})
		if err != nil {
			return invalidInput()
		}
		if raw, ok := q["policies"]; ok {
			var policies []string
			if json.Unmarshal(raw, &policies) != nil || len(policies) == 0 {
				return invalidInput()
			}
			for _, policy := range policies {
				if policy == "" || !validPolicy(policy) {
					return invalidInput()
				}
			}
		}
		var id string
		if !boundedString(q["id"], &id) || ids[id] {
			return invalidInput()
		}
		ids[id] = true
		if _, err := parseRequest(q["request"]); err != nil {
			return invalidInput()
		}
		if _, err := parseScope(q["scope"]); err != nil {
			return invalidInput()
		}
		var candidates []json.RawMessage
		if json.Unmarshal(q["candidates"], &candidates) != nil || len(candidates) > 1000 {
			return invalidInput()
		}
		if err := validateFixtureCandidates(candidates, minSimilarityScore); err != nil {
			return err
		}
		if raw, ok := q["fallback_candidates"]; ok {
			// A declared fallback step: every fallback hit scores 100, and
			// the similarity step is empty unless the query is evaluated
			// under a policy whose predicate may reject a non-empty pool.
			var fallback []json.RawMessage
			if (len(candidates) != 0 && q["policies"] == nil) || json.Unmarshal(raw, &fallback) != nil || len(fallback) > 1000 {
				return invalidInput()
			}
			if err := validateFixtureCandidates(fallback, 100); err != nil {
				return err
			}
		}
		var expected []json.RawMessage
		if json.Unmarshal(q["expected_ids"], &expected) != nil {
			return invalidInput()
		}
		seen := map[string]bool{}
		for _, raw := range expected {
			var id string
			if !boundedString(raw, &id) || seen[id] {
				return invalidInput()
			}
			seen[id] = true
		}
	}
	return nil
}
