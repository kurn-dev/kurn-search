package partscatalogselect

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"slices"
	"sort"

	"github.com/kurn-dev/kurn-search/adapter"
	"github.com/kurn-dev/kurn-search/adapters/partscatalog"
	"github.com/kurn-dev/kurn-search/internal/jsjson"
	"github.com/kurn-dev/kurn-search/internal/jsorder"
)

//go:embed testdata/neutral-fixture.json
var neutralFixture []byte

const fixtureSchema = "kurn-search/parts-catalog-select-fixture/v1"

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

type fixtureQuery struct {
	ID          string         `json:"id"`
	ExpectedIDs []string       `json:"expected_ids"`
	Request     fixtureRequest `json:"request"`
	Scope       fixtureScope   `json:"scope"`
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
// the pure adapter and emits the four exact semantic artifact components.
func GenerateArtifacts() (ArtifactBytes, error) {
	fixture, err := loadFixture()
	if err != nil {
		return ArtifactBytes{}, err
	}
	semantics := New()
	entries := make([]adapter.ProjectedEntry, 0, len(fixture.Records))
	entryObjects := make([]any, 0, len(fixture.Records))
	for _, record := range fixture.Records {
		raw, err := encodeFixtureRecord(record)
		if err != nil {
			return ArtifactBytes{}, err
		}
		projected, err := semantics.Project(raw)
		if err != nil || len(projected) != 1 {
			return ArtifactBytes{}, errors.New("partscatalogselect: neutral projection failed")
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
		requestBytes, scopeBytes, err := encodeFixtureQuery(query)
		if err != nil {
			return ArtifactBytes{}, err
		}
		plan, err := semantics.Compile(requestBytes, scopeBytes)
		if err != nil {
			return ArtifactBytes{}, errors.New("partscatalogselect: neutral compilation failed")
		}
		filter, err := semantics.CompileMembershipFilter(requestBytes, scopeBytes)
		if err != nil {
			return ArtifactBytes{}, err
		}
		probeObjects := objectsForProbes(plan.Probes(), true)
		codecQueries = append(codecQueries, jsjson.Object{
			"id": query.ID, "terms": objectsForProbes(plan.Probes(), false),
			"request": objectForRequest(query.Request),
		})
		compiledQueries = append(compiledQueries, jsjson.Object{
			"compiled": jsjson.Object{
				"contract": "kurn-search/parts-catalog-select/v1",
				"probes":   probeObjects,
				"query":    query.Request.Query,
				"filter":   objectsForFilter(filter),
			},
			"id": query.ID,
		})
		evidence, err := synthesizeEvidence(entries, plan, query, 1000)
		if err != nil {
			return ArtifactBytes{}, err
		}
		resolution, err := semantics.Resolve(requestBytes, scopeBytes, plan, evidence)
		if err != nil {
			return ArtifactBytes{}, errors.New("partscatalogselect: neutral resolution failed")
		}
		ids := make([]string, 0, len(resolution.Hits()))
		for _, hit := range resolution.Hits() {
			ids = append(ids, hit.EntryID())
		}
		if !slices.Equal(ids, query.ExpectedIDs) {
			return ArtifactBytes{}, errors.New("partscatalogselect: frozen answer mismatch")
		}
		resultObjects, err := objectsForHits(resolution.Hits())
		if err != nil {
			return ArtifactBytes{}, err
		}
		answers = append(answers, jsjson.Object{"id": query.ID, "results": resultObjects})
	}

	projection, err := jsjson.Encode(jsjson.Object{
		"entries": entryObjects, "schema": "kurn-search/parts-catalog-select-derived-entries/v1",
	})
	if err != nil {
		return ArtifactBytes{}, err
	}
	codec, err := jsjson.Encode(jsjson.Object{
		"selection": jsjson.Object{"within_field": "or", "across_fields": "and", "literal": "case-sensitive-untrimmed", "max_category": 16, "max_manufacturer": 16, "max_status": 2, "max_canonical_bytes": 16384},
		"queries":   codecQueries, "schema": "kurn-search/parts-catalog-select-query-codec/v1",
	})
	if err != nil {
		return ArtifactBytes{}, err
	}
	compiled, err := jsjson.Encode(jsjson.Object{
		"queries": compiledQueries, "schema": "kurn-search/parts-catalog-select-compiled-plans/v1",
	})
	if err != nil {
		return ArtifactBytes{}, err
	}
	resolved, err := jsjson.Encode(jsjson.Object{
		"answers": answers, "schema": "kurn-search/parts-catalog-select-resolved-results/v1",
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

func loadFixture() (fixtureFile, error) {
	if validateFixtureJSON(neutralFixture) != nil {
		return fixtureFile{}, invalidInput()
	}
	decoder := json.NewDecoder(bytes.NewReader(neutralFixture))
	decoder.DisallowUnknownFields()
	var fixture fixtureFile
	if decoder.Decode(&fixture) != nil || requireEOF(decoder) != nil || fixture.Schema != fixtureSchema ||
		fixture.SourceFixtureSHA256 != sourceFixtureSHA256 || len(fixture.Queries) != 20 {
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
		if includeRole {
			value["listRole"] = string(probe.ListRole())
		}
		values[index] = value
	}
	return values
}

func synthesizeEvidence(entries []adapter.ProjectedEntry, plan adapter.CompiledPlan, query fixtureQuery, pool int) ([]adapter.ProbeEvidence, error) {
	requestBytes, _, err := encodeFixtureQuery(query)
	if err != nil {
		return nil, err
	}
	request, err := parseRequest(requestBytes)
	if err != nil {
		return nil, err
	}
	result := make([]adapter.ProbeEvidence, 0, len(plan.Probes()))
	for _, probe := range plan.Probes() {
		candidates := make([]adapter.Candidate, 0)
		for _, entry := range entries {
			record, err := parseSource(entry.Payload())
			if err != nil {
				return nil, err
			}
			if record.TenantID != query.Scope.TenantID || !matchesRequest(record, request) ||
				!slices.Contains(entry.Keys(), probe.PhysicalKey()) {
				continue
			}
			candidate, err := adapter.NewCandidate(adapter.CandidateSpec{
				EntryID: entry.EntryID(), Score: 100, EchoedKey: probe.PhysicalKey(), Payload: entry.Payload(),
			})
			if err != nil {
				return nil, err
			}
			candidates = append(candidates, candidate)
		}
		sort.SliceStable(candidates, func(i, j int) bool {
			return jsorder.Less(candidates[i].EntryID(), candidates[j].EntryID())
		})
		if len(candidates) > pool {
			candidates = candidates[:pool]
		}
		item, err := adapter.NewProbeEvidence(adapter.ProbeEvidenceSpec{
			Probe: probe, RequestedPool: pool, Candidates: candidates,
		})
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, nil
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
	if json.Unmarshal(members["queries"], &queries) != nil || len(queries) != 20 {
		return invalidInput()
	}
	ids := map[string]bool{}
	for _, raw := range queries {
		q, err := parseMembers(raw, maxRequestBytes, []string{"id", "request", "scope", "expected_ids"}, []string{"id", "request", "scope", "expected_ids"})
		if err != nil {
			return invalidInput()
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
