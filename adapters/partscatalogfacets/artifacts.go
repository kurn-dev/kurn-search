package partscatalogfacets

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"sort"

	"github.com/kurn-dev/kurn-search/adapter"
	"github.com/kurn-dev/kurn-search/adapters/partscatalogdiscover"
	"github.com/kurn-dev/kurn-search/internal/jsjson"
	"github.com/kurn-dev/kurn-search/internal/jsorder"
)

//go:embed testdata/oracle.json
var oracleFixture []byte

const (
	fixtureSchema        = "kurn-search/parts-catalog-discover-facets-fixture/v1"
	baseFixtureSHA256    = "4e6649e12be908c424b10617fde06d29de9bf6a41a9a0dbd67cca10be5211c23"
	sourceFixtureSHA256  = "d6b7e113dd01d43ec108ab8eee54ef4ea59af5d0cca22b3b0f3a058efc1aa57c"
	fixtureQueryCount    = 26
	artifactEvidencePool = 1000
)

// OracleFixture returns a defensive copy of the frozen facet oracle.
func OracleFixture() []byte { return bytes.Clone(oracleFixture) }

type oracleRequest struct {
	Query       string         `json:"query"`
	Filters     map[string]any `json:"filters,omitempty"`
	PartitionID string         `json:"partition_id,omitempty"`
	Limit       int            `json:"limit,omitempty"`
}

type oracleScope struct {
	TenantID string `json:"tenant_id"`
}

type bucketPair [2]json.RawMessage

type oracleSummary struct {
	TotalMatches int `json:"total_matches"`
	Facets       struct {
		Category     []bucketPair `json:"category"`
		Manufacturer []bucketPair `json:"manufacturer"`
		Status       []bucketPair `json:"status"`
	} `json:"facets"`
}

type oracleQuery struct {
	ID              string        `json:"id"`
	Request         oracleRequest `json:"request"`
	Scope           oracleScope   `json:"scope"`
	ExpectedIDs     []string      `json:"expected_ids"`
	ExpectedSummary oracleSummary `json:"expected_summary"`
}

type oracleFile struct {
	Schema              string        `json:"schema"`
	BaseFixtureSHA256   string        `json:"base_fixture_sha256"`
	SourceFixtureSHA256 string        `json:"source_fixture_sha256"`
	Queries             []oracleQuery `json:"queries"`
}

func loadOracle() (oracleFile, error) {
	var fixture oracleFile
	if !json.Valid(oracleFixture) {
		return fixture, invalidInput()
	}
	decoder := json.NewDecoder(bytes.NewReader(oracleFixture))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&fixture); err != nil {
		return fixture, invalidInput()
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return fixture, invalidInput()
	}
	if fixture.Schema != fixtureSchema || fixture.BaseFixtureSHA256 != baseFixtureSHA256 ||
		fixture.SourceFixtureSHA256 != sourceFixtureSHA256 || len(fixture.Queries) != fixtureQueryCount {
		return fixture, invalidInput()
	}
	return fixture, nil
}

func (pair bucketPair) decode() (int, string, error) {
	var count int
	var value string
	if err := json.Unmarshal(pair[0], &count); err != nil {
		return 0, "", invalidInput()
	}
	if err := json.Unmarshal(pair[1], &value); err != nil {
		return 0, "", invalidInput()
	}
	return count, value, nil
}

// expectedSummary converts the frozen fixture shape into the contract input,
// enforcing the same invariants the constructor enforces.
func (summary oracleSummary) expected() (adapter.SummarySpec, error) {
	spec := adapter.SummarySpec{TotalMatches: summary.TotalMatches}
	fields := []struct {
		name  string
		pairs []bucketPair
	}{
		{"category", summary.Facets.Category},
		{"manufacturer", summary.Facets.Manufacturer},
		{"status", summary.Facets.Status},
	}
	for _, field := range fields {
		group := adapter.SummaryGroupSpec{Name: field.name}
		for _, pair := range field.pairs {
			count, value, err := pair.decode()
			if err != nil {
				return adapter.SummarySpec{}, err
			}
			group.Buckets = append(group.Buckets, adapter.SummaryBucketSpec{Count: count, Value: value})
		}
		spec.Groups = append(spec.Groups, group)
	}
	return spec, nil
}

// exampleEntries projects the checked discover example source through the
// delegate projection, exactly as the served list is built.
func exampleEntries() ([]adapter.ProjectedEntry, error) {
	source, err := partscatalogdiscover.ExampleSource()
	if err != nil {
		return nil, err
	}
	semantics := New()
	entries := make([]adapter.ProjectedEntry, 0, 152)
	lines := bytes.Split(bytes.TrimRight(source, "\n"), []byte("\n"))
	if len(lines) != 152 {
		return nil, errors.New("partscatalogfacets: example source census mismatch")
	}
	for _, line := range lines {
		var record map[string]json.RawMessage
		decoder := json.NewDecoder(bytes.NewReader(line))
		if err := decoder.Decode(&record); err != nil {
			return nil, err
		}
		canonical, err := canonicalRecord(record)
		if err != nil {
			return nil, err
		}
		projected, err := semantics.Project(canonical)
		if err != nil || len(projected) != 1 {
			return nil, errors.New("partscatalogfacets: example projection failed")
		}
		entries = append(entries, projected[0])
	}
	return entries, nil
}

// canonicalRecord re-encodes one example source row through the canonical
// encoder so projection sees the same bytes as the served list builder. The
// feed's pre-projected `keys` member is stripped: projection recomputes it.
func canonicalRecord(record map[string]json.RawMessage) ([]byte, error) {
	value := jsjson.Object{}
	for name, raw := range record {
		if name == "keys" {
			continue
		}
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return nil, errors.New("partscatalogfacets: example source shape mismatch")
		}
		value[name] = text
	}
	return jsjson.Encode(value)
}

func encodeOracleRequest(query oracleQuery) ([]byte, []byte, error) {
	request := jsjson.Object{"query": query.Request.Query}
	if query.Request.Limit != 0 {
		request["limit"] = query.Request.Limit
	}
	if query.Request.PartitionID != "" {
		request["partition_id"] = query.Request.PartitionID
	}
	if query.Request.Filters != nil {
		fields := jsjson.Object{}
		for name, raw := range query.Request.Filters {
			fields[name] = raw
		}
		request["filters"] = fields
	}
	requestBytes, err := jsjson.Encode(request)
	if err != nil {
		return nil, nil, err
	}
	scopeBytes, err := jsjson.Encode(jsjson.Object{"tenant_id": query.Scope.TenantID})
	if err != nil {
		return nil, nil, err
	}
	return requestBytes, scopeBytes, nil
}

// recordMatches mirrors the delegate's scope/filter membership exactly: trusted
// tenant equality, optional caller partition equality, and caller literal
// scalar/IN filters on the three admitted fields.
func recordMatches(payload []byte, query oracleQuery) bool {
	var record map[string]string
	if err := json.Unmarshal(payload, &record); err != nil {
		return false
	}
	if record["tenant_id"] != query.Scope.TenantID {
		return false
	}
	if query.Request.PartitionID != "" && record["partition_id"] != query.Request.PartitionID {
		return false
	}
	for name, raw := range query.Request.Filters {
		value := record[name]
		switch selection := raw.(type) {
		case string:
			if value != selection {
				return false
			}
		case []any:
			found := false
			for _, option := range selection {
				if text, ok := option.(string); ok && value == text {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// synthesizeEvidence builds the complete probe evidence for one fixture query
// at the artifact pool, mirroring the delegate's artifact synthesis.
func synthesizeEvidence(entries []adapter.ProjectedEntry, plan adapter.CompiledPlan, query oracleQuery) ([]adapter.ProbeEvidence, error) {
	result := make([]adapter.ProbeEvidence, 0, len(plan.Probes()))
	for _, probe := range plan.Probes() {
		candidates := make([]adapter.Candidate, 0)
		for _, entry := range entries {
			if !recordMatches(entry.Payload(), query) || !slices.Contains(entry.Keys(), probe.PhysicalKey()) {
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
		if len(candidates) > artifactEvidencePool {
			candidates = candidates[:artifactEvidencePool]
		}
		item, err := adapter.NewProbeEvidence(adapter.ProbeEvidenceSpec{
			Probe: probe, RequestedPool: artifactEvidencePool, Candidates: candidates,
		})
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, nil
}

func summaryObject(summary adapter.Summary) jsjson.Object {
	facets := jsjson.Object{}
	for _, group := range summary.Groups() {
		buckets := make([]any, 0, len(group.Buckets()))
		for _, bucket := range group.Buckets() {
			buckets = append(buckets, jsjson.Object{"count": bucket.Count(), "value": bucket.Value()})
		}
		facets[group.Name()] = buckets
	}
	return jsjson.Object{"facets": facets, "total_matches": summary.TotalMatches()}
}

func objectsForSummaryHits(hits []adapter.ResolvedHit) ([]any, error) {
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

func sameSummary(summary adapter.Summary, expected adapter.SummarySpec) bool {
	if summary.TotalMatches() != expected.TotalMatches || len(summary.Groups()) != len(expected.Groups) {
		return false
	}
	for index, group := range summary.Groups() {
		expectedGroup := expected.Groups[index]
		if group.Name() != expectedGroup.Name || len(group.Buckets()) != len(expectedGroup.Buckets) {
			return false
		}
		for position, bucket := range group.Buckets() {
			if bucket.Count() != expectedGroup.Buckets[position].Count ||
				bucket.Value() != expectedGroup.Buckets[position].Value {
				return false
			}
		}
	}
	return true
}

// resolveOracle compiles, synthesizes and resolves one frozen oracle query,
// returning the ordered hit identifiers, the summary and the resolution.
func resolveOracle(semantics Semantics, entries []adapter.ProjectedEntry, query oracleQuery) ([]string, adapter.Summary, adapter.Resolution, error) {
	requestBytes, scopeBytes, err := encodeOracleRequest(query)
	if err != nil {
		return nil, adapter.Summary{}, adapter.Resolution{}, err
	}
	plan, err := semantics.Compile(requestBytes, scopeBytes)
	if err != nil {
		return nil, adapter.Summary{}, adapter.Resolution{}, errors.New("partscatalogfacets: neutral compilation failed")
	}
	if _, err := semantics.CompileMembershipFilter(requestBytes, scopeBytes); err != nil {
		return nil, adapter.Summary{}, adapter.Resolution{}, err
	}
	evidence, err := synthesizeEvidence(entries, plan, query)
	if err != nil {
		return nil, adapter.Summary{}, adapter.Resolution{}, err
	}
	resolution, summary, err := semantics.ResolveWithSummary(requestBytes, scopeBytes, plan, evidence)
	if err != nil {
		return nil, adapter.Summary{}, adapter.Resolution{}, errors.New("partscatalogfacets: neutral resolution failed")
	}
	ids := make([]string, 0, len(resolution.Hits()))
	for _, hit := range resolution.Hits() {
		ids = append(ids, hit.EntryID())
	}
	return ids, summary, resolution, nil
}

// ArtifactBytes are the facet adapter's own resolved-result component; the
// projection, codec and plan components are reused byte-identically from the
// delivered discover adapter.
type ArtifactBytes struct {
	SourceProjection []byte
	QueryCodec       []byte
	CompiledPlan     []byte
	ResolvedResult   []byte
}

// GenerateArtifacts deterministically evaluates the frozen facet oracle through
// the delegating adapter and emits the exact semantic artifact components.
func GenerateArtifacts() (ArtifactBytes, error) {
	fixture, err := loadOracle()
	if err != nil {
		return ArtifactBytes{}, err
	}
	shared, err := partscatalogdiscover.GenerateArtifacts()
	if err != nil {
		return ArtifactBytes{}, err
	}
	entries, err := exampleEntries()
	if err != nil {
		return ArtifactBytes{}, err
	}
	semantics := New()
	answers := make([]any, 0, len(fixture.Queries))
	for _, query := range fixture.Queries {
		ids, summary, resolution, err := resolveOracle(semantics, entries, query)
		if err != nil {
			return ArtifactBytes{}, err
		}
		expectedSpec, err := query.ExpectedSummary.expected()
		if err != nil {
			return ArtifactBytes{}, err
		}
		if !slices.Equal(ids, query.ExpectedIDs) || !sameSummary(summary, expectedSpec) {
			return ArtifactBytes{}, fmt.Errorf("partscatalogfacets: frozen answer mismatch at %s", query.ID)
		}
		resultObjects, err := objectsForSummaryHits(resolution.Hits())
		if err != nil {
			return ArtifactBytes{}, err
		}
		answers = append(answers, jsjson.Object{
			"id": query.ID, "results": resultObjects, "summary": summaryObject(summary),
		})
	}
	resolved, err := jsjson.Encode(jsjson.Object{
		"answers": answers, "schema": "kurn-search/parts-catalog-discover-facets-resolved-results/v1",
	})
	if err != nil {
		return ArtifactBytes{}, err
	}
	return ArtifactBytes{
		SourceProjection: shared.SourceProjection, QueryCodec: shared.QueryCodec,
		CompiledPlan: shared.CompiledPlan, ResolvedResult: resolved,
	}, nil
}
