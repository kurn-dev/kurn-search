package partscatalogalternatives

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
var frozenOracle []byte

const (
	alternativeFixtureSchema = "kurn-search/parts-catalog-discover-facet-alternatives-fixture/v1"
	alternativeSourceSHA256  = "d6b7e113dd01d43ec108ab8eee54ef4ea59af5d0cca22b3b0f3a058efc1aa57c"
	alternativeOracleSHA256  = "f79924e008f38c74285014be8d44d89fc0de5efc8513c1c3fbb684f227657537"
)

// OracleFixture returns a defensive copy of the accepted independent oracle.
func OracleFixture() []byte { return bytes.Clone(frozenOracle) }

type oracleFacet struct {
	BasisTotal int                  `json:"basis_total"`
	Buckets    [][2]json.RawMessage `json:"buckets"`
}

type oracleRow struct {
	ID           string          `json:"id"`
	Request      json.RawMessage `json:"request"`
	Scope        json.RawMessage `json:"scope"`
	ExpectedIDs  []string        `json:"expected_ids"`
	TotalMatches int             `json:"total_matches"`
	Facets       struct {
		Category     oracleFacet `json:"category"`
		Manufacturer oracleFacet `json:"manufacturer"`
		Status       oracleFacet `json:"status"`
	} `json:"facets"`
}

type oracleFile struct {
	Schema              string      `json:"schema"`
	SourceFixtureSHA256 string      `json:"source_fixture_sha256"`
	Queries             []oracleRow `json:"queries"`
}

func loadOracle() (oracleFile, error) {
	var file oracleFile
	if adapter.PlainSHA256(frozenOracle).Hex() != alternativeOracleSHA256 {
		return file, invalidInput()
	}
	decoder := json.NewDecoder(bytes.NewReader(frozenOracle))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&file); err != nil {
		return file, invalidInput()
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return file, invalidInput()
	}
	if file.Schema != alternativeFixtureSchema || file.SourceFixtureSHA256 != alternativeSourceSHA256 || len(file.Queries) != 12 {
		return file, invalidInput()
	}
	for index, row := range file.Queries {
		if row.ID != fmt.Sprintf("A%02d", index+1) || len(row.ExpectedIDs) > 1000 {
			return file, invalidInput()
		}
	}
	return file, nil
}

func artifactEntries() ([]adapter.ProjectedEntry, error) {
	source, err := partscatalogdiscover.ExampleSource()
	if err != nil {
		return nil, err
	}
	lines := bytes.Split(bytes.TrimSuffix(source, []byte("\n")), []byte("\n"))
	if len(lines) != 152 || len(source) != 70_605 {
		return nil, invalidInput()
	}
	entries := make([]adapter.ProjectedEntry, 0, len(lines))
	for _, line := range lines {
		var record map[string]json.RawMessage
		if err := json.Unmarshal(line, &record); err != nil {
			return nil, err
		}
		delete(record, "keys")
		payload, err := json.Marshal(record)
		if err != nil {
			return nil, err
		}
		projected, err := New().Project(payload)
		if err != nil || len(projected) != 1 {
			return nil, invalidInput()
		}
		entries = append(entries, projected[0])
	}
	return entries, nil
}

func artifactEvidence(entries []adapter.ProjectedEntry, plan adapter.CompiledPlan, tenant, partition string) ([]adapter.ProbeEvidence, error) {
	items := make([]adapter.ProbeEvidence, 0, len(plan.Probes()))
	for _, probe := range plan.Probes() {
		candidates := make([]adapter.Candidate, 0)
		for _, entry := range entries {
			var record map[string]string
			if err := json.Unmarshal(entry.Payload(), &record); err != nil {
				return nil, err
			}
			if record["tenant_id"] != tenant || partition != "" && record["partition_id"] != partition ||
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
		sort.Slice(candidates, func(i, j int) bool { return jsorder.Less(candidates[i].EntryID(), candidates[j].EntryID()) })
		item, err := adapter.NewProbeEvidence(adapter.ProbeEvidenceSpec{Probe: probe, RequestedPool: 1000, Candidates: candidates})
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func checkFacet(actual adapter.AlternativeSummaryGroup, expected oracleFacet) bool {
	if actual.BasisTotal() != expected.BasisTotal || len(actual.Buckets()) != len(expected.Buckets) {
		return false
	}
	for index, bucket := range actual.Buckets() {
		var count int
		var value string
		if json.Unmarshal(expected.Buckets[index][0], &count) != nil || json.Unmarshal(expected.Buckets[index][1], &value) != nil ||
			bucket.Count() != count || bucket.Value() != value {
			return false
		}
	}
	return true
}

// ArtifactBytes identify unchanged projection/query components and the new
// self-excluding result semantics. GenerateArtifacts refuses oracle drift.
type ArtifactBytes struct {
	SourceProjection []byte
	QueryCodec       []byte
	CompiledPlan     []byte
	ResolvedResult   []byte
}

func GenerateArtifacts() (ArtifactBytes, error) {
	file, err := loadOracle()
	if err != nil {
		return ArtifactBytes{}, err
	}
	shared, err := partscatalogdiscover.GenerateArtifacts()
	if err != nil {
		return ArtifactBytes{}, err
	}
	entries, err := artifactEntries()
	if err != nil {
		return ArtifactBytes{}, err
	}
	semantics := New()
	answers := make([]any, 0, len(file.Queries))
	for _, row := range file.Queries {
		var scope struct {
			TenantID string `json:"tenant_id"`
		}
		var request struct {
			PartitionID string `json:"partition_id"`
		}
		if json.Unmarshal(row.Scope, &scope) != nil || json.Unmarshal(row.Request, &request) != nil {
			return ArtifactBytes{}, invalidInput()
		}
		plan, err := semantics.Compile(row.Request, row.Scope)
		if err != nil {
			return ArtifactBytes{}, err
		}
		filter, err := semantics.CompileMembershipFilter(row.Request, row.Scope)
		if err != nil || len(filter.Caller()) > 1 {
			return ArtifactBytes{}, invalidInput()
		}
		evidence, err := artifactEvidence(entries, plan, scope.TenantID, request.PartitionID)
		if err != nil {
			return ArtifactBytes{}, err
		}
		resolution, summary, err := semantics.ResolveWithAlternativeSummary(row.Request, row.Scope, plan, evidence)
		if err != nil {
			return ArtifactBytes{}, err
		}
		ids := make([]string, 0, len(resolution.Hits()))
		hits := make([]any, 0, len(resolution.Hits()))
		for _, hit := range resolution.Hits() {
			ids = append(ids, hit.EntryID())
			var payload, observed map[string]any
			if json.Unmarshal(hit.Result(), &payload) != nil || json.Unmarshal(hit.Evidence(), &observed) != nil {
				return ArtifactBytes{}, invalidInput()
			}
			hits = append(hits, jsjson.Object{"entryId": hit.EntryID(), "payload": payload, "evidence": observed})
		}
		if !slices.Equal(ids, row.ExpectedIDs) || summary.TotalMatches() != row.TotalMatches {
			return ArtifactBytes{}, invalidInput()
		}
		groups := summary.Groups()
		if len(groups) != 3 || !checkFacet(groups[0], row.Facets.Category) ||
			!checkFacet(groups[1], row.Facets.Manufacturer) || !checkFacet(groups[2], row.Facets.Status) {
			return ArtifactBytes{}, invalidInput()
		}
		facetObject := jsjson.Object{}
		for _, group := range groups {
			buckets := make([]any, 0, len(group.Buckets()))
			for _, bucket := range group.Buckets() {
				buckets = append(buckets, jsjson.Object{"count": bucket.Count(), "value": bucket.Value()})
			}
			facetObject[group.Name()] = jsjson.Object{"basis_total": group.BasisTotal(), "buckets": buckets}
		}
		answers = append(answers, jsjson.Object{"id": row.ID, "results": hits, "total_matches": summary.TotalMatches(), "facets": facetObject})
	}
	resolved, err := jsjson.Encode(jsjson.Object{
		"schema": "kurn-search/parts-catalog-discover-facet-alternatives-resolved-results/v1", "answers": answers,
	})
	if err != nil {
		return ArtifactBytes{}, err
	}
	return ArtifactBytes{SourceProjection: shared.SourceProjection, QueryCodec: shared.QueryCodec,
		CompiledPlan: shared.CompiledPlan, ResolvedResult: resolved}, nil
}
