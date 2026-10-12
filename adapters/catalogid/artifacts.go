package catalogid

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"slices"
	"sort"

	"github.com/kurn-dev/kurn-search/adapter"
	"github.com/kurn-dev/kurn-search/internal/jsjson"
	"github.com/kurn-dev/kurn-search/internal/jsorder"
)

//go:embed testdata/neutral-fixture.json
var neutralFixture []byte

const fixtureSchema = "kurn-search/catalog-identifier-fixture/v1"

type fixtureRecord struct {
	ID          string `json:"id"`
	Identifier  string `json:"identifier"`
	TenantID    string `json:"tenant_id"`
	PartitionID string `json:"partition_id,omitempty"`
}

type fixtureRequest struct {
	Query       string `json:"query"`
	PartitionID string `json:"partition_id,omitempty"`
	Limit       int    `json:"limit,omitempty"`
}

type fixtureScope struct {
	TenantID string `json:"tenant_id"`
}

type fixtureQuery struct {
	ID      string         `json:"id"`
	Request fixtureRequest `json:"request"`
	Scope   fixtureScope   `json:"scope"`
}

type fixtureFile struct {
	Schema  string          `json:"schema"`
	Records []fixtureRecord `json:"records"`
	Queries []fixtureQuery  `json:"queries"`
}

// ArtifactBytes are the four concrete semantic components bound by Slice 2.
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
			return ArtifactBytes{}, errors.New("catalogid: neutral projection failed")
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
			return ArtifactBytes{}, errors.New("catalogid: neutral compilation failed")
		}
		probeObjects := objectsForProbes(plan.Probes(), true)
		codecQueries = append(codecQueries, jsjson.Object{
			"id": query.ID, "terms": objectsForProbes(plan.Probes(), false),
		})
		compiledQueries = append(compiledQueries, jsjson.Object{
			"compiled": jsjson.Object{
				"contract": "kurn-search/catalog-identifier/v1",
				"probes":   probeObjects,
				"query":    query.Request.Query,
			},
			"id": query.ID,
		})
		evidence, err := synthesizeEvidence(entries, plan, query, 1000)
		if err != nil {
			return ArtifactBytes{}, err
		}
		resolution, err := semantics.Resolve(requestBytes, scopeBytes, plan, evidence)
		if err != nil {
			return ArtifactBytes{}, errors.New("catalogid: neutral resolution failed")
		}
		resultObjects, err := objectsForHits(resolution.Hits())
		if err != nil {
			return ArtifactBytes{}, err
		}
		answers = append(answers, jsjson.Object{"id": query.ID, "results": resultObjects})
	}

	projection, err := jsjson.Encode(jsjson.Object{
		"entries": entryObjects, "schema": "kurn-search/catalog-identifier-derived-entries/v1",
	})
	if err != nil {
		return ArtifactBytes{}, err
	}
	codec, err := jsjson.Encode(jsjson.Object{
		"queries": codecQueries, "schema": "kurn-search/catalog-identifier-query-codec/v1",
	})
	if err != nil {
		return ArtifactBytes{}, err
	}
	compiled, err := jsjson.Encode(jsjson.Object{
		"queries": compiledQueries, "schema": "kurn-search/catalog-identifier-compiled-plans/v1",
	})
	if err != nil {
		return ArtifactBytes{}, err
	}
	resolved, err := jsjson.Encode(jsjson.Object{
		"answers": answers, "schema": "kurn-search/catalog-identifier-resolved-results/v1",
	})
	if err != nil {
		return ArtifactBytes{}, err
	}
	return ArtifactBytes{
		SourceProjection: projection, QueryCodec: codec,
		CompiledPlan: compiled, ResolvedResult: resolved,
	}, nil
}

func loadFixture() (fixtureFile, error) {
	if rejectDuplicateNames(neutralFixture) != nil {
		return fixtureFile{}, errors.New("catalogid: neutral fixture invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(neutralFixture))
	decoder.DisallowUnknownFields()
	var fixture fixtureFile
	if err := decoder.Decode(&fixture); err != nil || requireEOF(decoder) != nil || fixture.Schema != fixtureSchema ||
		len(fixture.Records) == 0 || len(fixture.Queries) == 0 {
		return fixtureFile{}, errors.New("catalogid: neutral fixture invalid")
	}
	return fixture, nil
}

func encodeFixtureRecord(record fixtureRecord) ([]byte, error) {
	return jsjson.Encode(objectForRecord(record))
}

func encodeFixtureQuery(query fixtureQuery) ([]byte, []byte, error) {
	request := jsjson.Object{"query": query.Request.Query}
	if query.Request.PartitionID != "" {
		request["partition_id"] = query.Request.PartitionID
	}
	if query.Request.Limit != 0 {
		request["limit"] = query.Request.Limit
	}
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
	result := make([]adapter.ProbeEvidence, 0, len(plan.Probes()))
	for _, probe := range plan.Probes() {
		candidates := make([]adapter.Candidate, 0)
		for _, entry := range entries {
			record, err := parseSource(entry.Payload())
			if err != nil || record.TenantID != query.Scope.TenantID ||
				(query.Request.PartitionID != "" && (!record.partition || record.PartitionID != query.Request.PartitionID)) ||
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
