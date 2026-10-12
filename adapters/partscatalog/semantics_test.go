package partscatalog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/kurn-dev/kurn-search/adapter"
	"github.com/kurn-dev/kurn-search/adapter/analyzer"
	"github.com/kurn-dev/kurn-search/internal/jsorder"
)

func fixtureEntries(t *testing.T) (fixtureFile, []adapter.ProjectedEntry) {
	t.Helper()
	f, err := loadFixture()
	if err != nil {
		t.Fatal(err)
	}
	entries := make([]adapter.ProjectedEntry, 0, len(f.Records))
	for _, r := range f.Records {
		raw, err := encodeFixtureRecord(r)
		if err != nil {
			t.Fatal(err)
		}
		projected, err := New().Project(raw)
		if err != nil || len(projected) != 1 {
			t.Fatalf("projection: %v", err)
		}
		entries = append(entries, projected[0])
	}
	return f, entries
}

func TestSourceStrictBoundsAndProjection(t *testing.T) {
	baseline := map[string]any{"id": "part-1", "identifier": "AX-100", "manufacturer": "Northstar", "category": "bearing", "status": "active", "tenant_id": "tenant-a", "partition_id": "north"}
	encode := func(m map[string]any) []byte {
		b, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	copyRecord := func() map[string]any {
		m := map[string]any{}
		for k, v := range baseline {
			m[k] = v
		}
		return m
	}
	raw := encode(baseline)
	entries, err := New().Project(raw)
	if err != nil || len(entries) != 1 {
		t.Fatal(err)
	}
	if !slices.IsSortedFunc(entries[0].Keys(), jsorder.Compare) {
		t.Fatal("key ordering")
	}
	decoded := map[string]any{}
	if json.Unmarshal(entries[0].Payload(), &decoded) != nil || !bytes.Equal(encode(decoded), raw) {
		t.Fatal("payload changed")
	}
	keys := entries[0].Keys()
	keys[0] = "corrupt"
	if entries[0].Keys()[0] == "corrupt" {
		t.Fatal("key alias")
	}
	for _, name := range []string{"id", "identifier", "manufacturer", "category", "status", "tenant_id", "partition_id"} {
		if name != "partition_id" {
			m := copyRecord()
			delete(m, name)
			if _, err := New().Project(encode(m)); err == nil {
				t.Fatalf("missing %s", name)
			}
		}
		for _, value := range []any{nil, true, 1, []string{"x"}, map[string]string{"x": "y"}, "", " ", "\u00a0x", "x\u2003"} {
			m := copyRecord()
			m[name] = value
			if _, err := New().Project(encode(m)); err == nil {
				t.Fatalf("invalid source %s %v", name, value)
			}
		}
	}
	for _, name := range []string{"identifier", "manufacturer", "category", "tenant_id", "partition_id"} {
		m := copyRecord()
		m[name] = strings.Repeat("x", 512)
		if _, err := parseSource(encode(m)); err != nil {
			t.Fatalf("512 source %s", name)
		}
		m[name] = strings.Repeat("x", 513)
		if _, err := parseSource(encode(m)); err == nil {
			t.Fatalf("513 source %s", name)
		}
	}
	for _, n := range []int{128, 129} {
		m := copyRecord()
		m["id"] = strings.Repeat("😀", n)
		_, err := New().Project(encode(m))
		if (err == nil) != (n == 128) {
			t.Fatal("ID byte boundary")
		}
	}
	m := copyRecord()
	m["manufacturer"] = "North star"
	if _, err := New().Project(encode(m)); err != nil {
		t.Fatal("interior whitespace changed")
	}
	m = copyRecord()
	m["status"] = "archived"
	if _, err := New().Project(encode(m)); err == nil {
		t.Fatal("status")
	}
	invalid := [][]byte{
		[]byte(strings.Replace(string(raw), `"AX-100"`, `"\ud800"`, 1)),
		[]byte(strings.Replace(string(raw), `"AX-100"`, `"\udc00"`, 1)),
		[]byte(strings.Replace(string(raw), `"id":"part-1"`, `"id":"part-1","id":"part-2"`, 1)),
		append(append([]byte{}, raw...), []byte(" true")...),
		append([]byte(strings.Repeat(" ", maxSourceBytes)), raw...),
		[]byte(strings.Replace(string(raw), "AX-100", string([]byte{0xff}), 1)),
	}
	for i, b := range invalid {
		if _, err := New().Project(b); err == nil {
			t.Fatalf("raw source refusal %d", i)
		}
	}
}

func TestFrozenFixtureAndExpectedAnswers(t *testing.T) {
	f, entries := fixtureEntries(t)
	if len(f.Records) != 152 || len(f.Queries) != 24 {
		t.Fatalf("fixture census: %d records, %d queries", len(f.Records), len(f.Queries))
	}
	for i := 12; i < 152; i++ {
		r := f.Records[i]
		id := fmt.Sprintf("ZB-%d", 9000+i-12)
		status := "active"
		if i == 151 {
			status = "discontinued"
		}
		if r.ID != id || r.Identifier != id || r.Manufacturer != "Halden" || r.Category != "gasket" || r.Status != status || r.TenantID != "tenant-a" || r.PartitionID != "" {
			t.Fatal("block construction")
		}
	}
	want := map[string]string{
		"unfiltered": "001,002,003,004,010,011,006,007,008,012", "empty-filters": "001,002,003,004,010,011,006,007,008,012",
		"manufacturer": "001,003,004,010,006,012", "category": "001,002,003,010,011,006",
		"status": "001,002,004,010,011,006,008,012", "manufacturer-category": "001,003,010,006",
		"manufacturer-status": "001,004,010,006,012", "category-status": "001,002,010,011,006", "all-fields": "001,010,006",
		"manufacturer-case": "011", "category-case": "012", "literal-space": "", "unknown": "",
		"all-north": "001,006", "south": "003,007,008", "tenant-b": "005", "golden": "009", "no-hit": "",
		"starts": "001,002,003,004,006,007,010,011,012", "word": "001,002,003,004,006,007,010,011,012",
		"ends": "001,002,003,004,008,010,011", "limit-two": "001,002",
	}
	for _, q := range f.Queries {
		t.Run(q.ID, func(t *testing.T) {
			req, scope, err := encodeFixtureQuery(q)
			if err != nil {
				t.Fatal(err)
			}
			plan, err := New().Compile(req, scope)
			if err != nil {
				t.Fatal(err)
			}
			ev, err := synthesizeEvidence(entries, plan, q, 1000)
			if err != nil {
				t.Fatal(err)
			}
			result, err := New().Resolve(req, scope, plan, ev)
			if err != nil {
				t.Fatal(err)
			}
			var ids []string
			for _, h := range result.Hits() {
				ids = append(ids, h.EntryID())
				index := slices.IndexFunc(entries, func(e adapter.ProjectedEntry) bool { return e.EntryID() == h.EntryID() })
				if index < 0 || !bytes.Equal(entries[index].Payload(), h.Result()) {
					t.Fatal("full result payload")
				}
			}
			var expected []string
			switch q.ID {
			case "below-cut-baseline":
				for n := 9000; n <= 9139; n++ {
					expected = append(expected, fmt.Sprintf("ZB-%d", n))
				}
			case "below-cut-filtered":
				expected = []string{"ZB-9139"}
			default:
				row, ok := want[q.ID]
				if !ok {
					t.Fatal("unreviewed query")
				}
				if row != "" {
					for _, id := range strings.Split(row, ",") {
						expected = append(expected, "PC-"+id)
					}
				}
			}
			if !result.Complete() || !slices.Equal(ids, expected) {
				t.Fatalf("got %v want %v", ids, expected)
			}
		})
	}
}

func TestPureBelowCutAndCompleteness(t *testing.T) {
	f, entries := fixtureEntries(t)
	q := f.Queries[slices.IndexFunc(f.Queries, func(q fixtureQuery) bool { return q.ID == "below-cut-baseline" })]
	req, scope, _ := encodeFixtureQuery(q)
	plan, err := New().Compile(req, scope)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"exact:zb-9", "starts:zb-9", "ends:zb-9", "word:9", "word:zb"}
	var probes []string
	for _, p := range plan.Probes() {
		probes = append(probes, string(p.Kind())+":"+p.LogicalTerm())
	}
	if !slices.Equal(probes, want) {
		t.Fatalf("five probes %v", probes)
	}
	ev, err := synthesizeEvidence(entries, plan, q, 128)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ev {
		for _, c := range e.Candidates() {
			if c.EntryID() == "ZB-9139" {
				t.Fatal("target before cut")
			}
		}
	}
	if _, err := New().Resolve(req, scope, plan, ev); !New().IsIncomplete(err) {
		t.Fatal("saturation accepted")
	}
	ev, err = synthesizeEvidence(entries, plan, q, 256)
	if err != nil {
		t.Fatal(err)
	}
	result, err := New().Resolve(req, scope, plan, ev)
	if err != nil || len(result.Hits()) != 140 {
		t.Fatal("complete rung")
	}
	q.Request.Filters = map[string]string{"status": "discontinued"}
	req, scope, _ = encodeFixtureQuery(q)
	plan, err = New().Compile(req, scope)
	if err != nil {
		t.Fatal(err)
	}
	ev, err = synthesizeEvidence(entries, plan, q, 128)
	if err != nil {
		t.Fatal(err)
	}
	result, err = New().Resolve(req, scope, plan, ev)
	if err != nil || len(result.Hits()) != 1 || result.Hits()[0].EntryID() != "ZB-9139" {
		t.Fatal("filtered target")
	}
	// These are pure synthetic evidence cases, not live engine/stats claims.
}

func TestResolveRefusesMismatchedPayloads(t *testing.T) {
	req := []byte(`{"query":"AX-100","partition_id":"north","filters":{"manufacturer":"Northstar","category":"bearing","status":"active"}}`)
	scope := []byte(`{"tenant_id":"tenant-a"}`)
	plan, err := New().Compile(req, scope)
	if err != nil {
		t.Fatal(err)
	}
	base := map[string]any{"id": "PC-001", "identifier": "AX-100", "manufacturer": "Northstar", "category": "bearing", "status": "active", "tenant_id": "tenant-a", "partition_id": "north"}
	makeEvidence := func(payload []byte, score float64, key string) []adapter.ProbeEvidence {
		ev := make([]adapter.ProbeEvidence, len(plan.Probes()))
		for i, p := range plan.Probes() {
			var candidates []adapter.Candidate
			if i == 0 {
				c, err := adapter.NewCandidate(adapter.CandidateSpec{EntryID: "PC-001", Score: score, EchoedKey: key, Payload: payload})
				if err != nil {
					t.Fatal(err)
				}
				candidates = []adapter.Candidate{c}
			}
			e, err := adapter.NewProbeEvidence(adapter.ProbeEvidenceSpec{Probe: p, RequestedPool: 128, Candidates: candidates})
			if err != nil {
				t.Fatal(err)
			}
			ev[i] = e
		}
		return ev
	}
	for _, field := range []string{"id", "manufacturer", "category", "status", "tenant_id", "partition_id"} {
		m := map[string]any{}
		for k, v := range base {
			m[k] = v
		}
		m[field] = "other"
		if field == "status" {
			m[field] = "discontinued"
		}
		payload, _ := json.Marshal(m)
		if _, err := New().Resolve(req, scope, plan, makeEvidence(payload, 100, plan.Probes()[0].PhysicalKey())); err == nil {
			t.Fatalf("mismatch %s accepted", field)
		}
	}
	payload, _ := json.Marshal(base)
	for _, ev := range [][]adapter.ProbeEvidence{
		makeEvidence(payload, 99, plan.Probes()[0].PhysicalKey()),
		makeEvidence(payload, 100, "wrong-key"),
	} {
		if _, err := New().Resolve(req, scope, plan, ev); err == nil {
			t.Fatal("hostile evidence accepted")
		}
	}
	good := makeEvidence(payload, 100, plan.Probes()[0].PhysicalKey())
	if _, err := New().Resolve(req, scope, plan, good); err != nil {
		t.Fatal(err)
	}
	if _, err := New().Resolve(req, scope, plan, good[:len(good)-1]); err == nil {
		t.Fatal("missing probe")
	}
}

func TestQueryAndProjectionCeilings(t *testing.T) {
	scope := []byte(`{"tenant_id":"tenant-a"}`)
	for _, query := range []string{"", " ", "\uFEFF", strings.Repeat("x", 257), strings.Repeat("😀", 256)} {
		raw, _ := json.Marshal(map[string]any{"query": query})
		if _, err := New().Compile(raw, scope); err == nil {
			t.Fatal("query ceiling")
		}
	}
	var terms []string
	for i := 0; i < 22; i++ {
		terms = append(terms, fmt.Sprintf("token%c", 'a'+i))
	}
	raw, _ := json.Marshal(map[string]any{"query": strings.Join(terms, " ")})
	if _, err := New().Compile(raw, scope); err == nil {
		t.Fatal("probe ceiling")
	}
	for i := 22; i < 55; i++ {
		terms = append(terms, fmt.Sprintf("key%03d", i))
	}
	if _, err := projectKeys(strings.Join(terms, " ")); err == nil {
		t.Fatal("key ceiling")
	}
}

func TestProjectionKeyCountIndependentOfKeyLength(t *testing.T) {
	for _, lastLength := range []int{18, 19} {
		t.Run(fmt.Sprintf("last-token-%d", lastLength), func(t *testing.T) {
			var tokens []string
			for c := 'a'; c <= 'f'; c++ {
				tokens = append(tokens, string(c)+strings.Repeat("x", 18)+string(c))
			}
			tokens = append(tokens, "g"+strings.Repeat("x", lastLength-2)+"g")
			identifier := strings.Join(tokens, " ")
			// Enumerate the unchanged analyzer's terms independently of the
			// count-limited projector. All individual keys must fit, so the
			// overflow case cannot pass by tripping the key-length guard.
			uncapped := map[string]bool{}
			for _, view := range []struct {
				kind adapter.EvidenceKind
				view analyzer.View
			}{{kindExact, analyzer.ExactNormalizer}, {kindStarts, analyzer.StartsIndex}, {kindEnds, analyzer.EndsIndex}, {kindWord, analyzer.WordIndex}} {
				terms, err := analyzer.Analyze(view.view, identifier)
				if err != nil {
					t.Fatal(err)
				}
				for _, term := range terms {
					key, err := physicalKey(view.kind, term)
					if err != nil {
						t.Fatal("individual key exceeded its length ceiling", err)
					}
					uncapped[key] = true
				}
			}
			want := 256 + 2*(lastLength-18)
			if len(uncapped) != want {
				t.Fatalf("independent key census %d, want %d", len(uncapped), want)
			}
			keys, err := projectKeys(identifier)
			if (err == nil) != (want == 256) || (err == nil && len(keys) != want) {
				t.Fatal("projection key-count boundary", err)
			}
			raw, err := json.Marshal(map[string]string{"id": "part-key-bound", "identifier": identifier, "manufacturer": "Northstar", "category": "bearing", "status": "active", "tenant_id": "tenant-a"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := parseSource(raw); err != nil {
				t.Fatal("valid source refused before projection", err)
			}
			if _, err := New().Project(raw); (err == nil) != (want == 256) {
				t.Fatal("full projection did not preserve key-count boundary", err)
			}
		})
	}
}

// These are pure contract tests, not actual Kurn or allocation measurements.
func TestSourceClosedWireAndEnvelopeBoundaries(t *testing.T) {
	valid := []byte(`{"id":"part-1","identifier":"AX-100","manufacturer":"Northstar","category":"bearing","status":"active","tenant_id":"tenant-a"}`)
	for _, c := range []struct {
		name string
		raw  []byte
	}{
		{"null", []byte(`null`)},
		{"array", append(append([]byte("["), valid...), ']')},
		{"boolean", []byte(`true`)},
		{"string", []byte(`"source"`)},
		{"unknown-member", bytes.Replace(valid, []byte(`"id":`), []byte(`"extra":"x","id":`), 1)},
		{"escaped-duplicate-member", bytes.Replace(valid, []byte(`"id":`), []byte(`"\u0069d":"other","id":`), 1)},
		{"nested-duplicate-member", bytes.Replace(valid, []byte(`"bearing"`), []byte(`{"x":1,"x":2}`), 1)},
		{"trailing-object", append(bytes.Clone(valid), []byte(` {}`)...)},
		{"invalid-utf8", bytes.Replace(valid, []byte("AX-100"), []byte{0xff}, 1)},
		{"unpaired-high-surrogate", bytes.Replace(valid, []byte(`"Northstar"`), []byte(`"\ud800"`), 1)},
		{"unpaired-low-surrogate", bytes.Replace(valid, []byte(`"Northstar"`), []byte(`"\udc00"`), 1)},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, err := New().Project(c.raw); err == nil {
				t.Fatal("malformed source accepted")
			}
		})
	}
	t.Run("paired-surrogate-preserved", func(t *testing.T) {
		raw := bytes.Replace(valid, []byte(`"Northstar"`), []byte(`"\ud83d\ude00"`), 1)
		entries, err := New().Project(raw)
		if err != nil || len(entries) != 1 {
			t.Fatal("valid surrogate pair refused", err)
		}
		var payload map[string]any
		if json.Unmarshal(entries[0].Payload(), &payload) != nil || payload["manufacturer"] != "😀" {
			t.Fatal("surrogate pair altered")
		}
		if _, exists := payload["partition_id"]; exists {
			t.Fatal("absent partition materialized")
		}
	})
	for _, c := range []struct {
		name  string
		raw   []byte
		bound int
		check func([]byte) error
	}{
		{"source", valid, maxSourceBytes, func(raw []byte) error { _, err := New().Project(raw); return err }},
		{"request", []byte(`{"query":"AX-100"}`), maxRequestBytes, func(raw []byte) error { _, err := New().Compile(raw, []byte(`{"tenant_id":"tenant-a"}`)); return err }},
		{"scope", []byte(`{"tenant_id":"tenant-a"}`), maxTrustedScopeBytes, func(raw []byte) error { _, err := New().Compile([]byte(`{"query":"AX-100"}`), raw); return err }},
	} {
		for _, over := range []int{0, 1} {
			t.Run(fmt.Sprintf("%s-bytes-plus-%d", c.name, over), func(t *testing.T) {
				raw := append(bytes.Repeat([]byte(" "), c.bound+over-len(c.raw)), c.raw...)
				if err := c.check(raw); (err == nil) != (over == 0) {
					t.Fatalf("exact envelope admission mismatch: %v", err)
				}
			})
		}
	}
}

func TestSourceUnicodeScalarsAndInteriorPreservation(t *testing.T) {
	baseline := map[string]string{"id": "part-1", "identifier": "AX-100", "manufacturer": "Northstar", "category": "bearing", "status": "active", "tenant_id": "tenant-a", "partition_id": "north"}
	encoded := func(field, value string) []byte {
		t.Helper()
		m := map[string]string{}
		for name, original := range baseline {
			m[name] = original
		}
		m[field] = value
		raw, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	for _, field := range []string{"identifier", "manufacturer", "category", "tenant_id", "partition_id"} {
		for _, n := range []int{512, 513} {
			t.Run(fmt.Sprintf("%s-%d-scalars", field, n), func(t *testing.T) {
				// Parsing admits the source scalar limit independently of the
				// stricter projected-key limit on a long identifier.
				if _, err := parseSource(encoded(field, strings.Repeat("😀", n))); (err == nil) != (n == 512) {
					t.Fatal("source scalar boundary", err)
				}
			})
		}
	}
	for _, field := range []string{"id", "identifier", "manufacturer", "category", "tenant_id", "partition_id"} {
		t.Run(field+"-interior-space", func(t *testing.T) {
			value := "A\u00a0B"
			entries, err := New().Project(encoded(field, value))
			if err != nil || len(entries) != 1 {
				t.Fatal("interior-space source refused", err)
			}
			var payload map[string]string
			if json.Unmarshal(entries[0].Payload(), &payload) != nil || payload[field] != value {
				t.Fatal("interior source whitespace normalized")
			}
		})
	}
	for _, n := range []int{382, 383} {
		t.Run(fmt.Sprintf("physical-key-%d-ascii-bytes", n), func(t *testing.T) {
			// Two namespace bytes + unpadded base64: 382 bytes fit 512;
			// 383 bytes need 513. This is not a source-scalar overflow.
			key, err := physicalKey(kindExact, strings.Repeat("x", n))
			if (err == nil) != (n == 382) || (err == nil && len(key) != 512) {
				t.Fatal("physical-key encoded boundary", err)
			}
		})
	}
}

func TestResolveCanonicalAgreementAndDuplicateEvidence(t *testing.T) {
	request, scope := []byte(`{"query":"AX-100"}`), []byte(`{"tenant_id":"tenant-a"}`)
	plan, err := New().Compile(request, scope)
	if err != nil || len(plan.Probes()) < 2 {
		t.Fatal("plan", err)
	}
	base := []byte(`{"id":"part-1","identifier":"AX-100","manufacturer":"Northstar","category":"bearing","status":"active","tenant_id":"tenant-a"}`)
	makeEvidence := func(payloads ...[]byte) []adapter.ProbeEvidence {
		t.Helper()
		evidence := make([]adapter.ProbeEvidence, len(plan.Probes()))
		for i, probe := range plan.Probes() {
			var candidates []adapter.Candidate
			if i < len(payloads) {
				candidate, err := adapter.NewCandidate(adapter.CandidateSpec{EntryID: "part-1", Score: 100, EchoedKey: probe.PhysicalKey(), Payload: payloads[i]})
				if err != nil {
					t.Fatal(err)
				}
				candidates = []adapter.Candidate{candidate}
			}
			var err error
			evidence[i], err = adapter.NewProbeEvidence(adapter.ProbeEvidenceSpec{Probe: probe, RequestedPool: 128, Candidates: candidates})
			if err != nil {
				t.Fatal(err)
			}
		}
		return evidence
	}
	t.Run("equivalent-json-single-result", func(t *testing.T) {
		alternate := []byte(` {"tenant_id":"tenant-a", "status":"active", "category":"bearing", "manufacturer":"\u004eorthstar", "identifier":"AX-100", "id":"part-1"} `)
		result, err := New().Resolve(request, scope, plan, makeEvidence(base, alternate))
		if err != nil || !result.Complete() || len(result.Hits()) != 1 {
			t.Fatal("canonical agreement refused", err)
		}
		record, err := parseSource(base)
		if err != nil {
			t.Fatal(err)
		}
		canonical, err := canonicalSource(record)
		if err != nil || !bytes.Equal(result.Hits()[0].Result(), canonical) {
			t.Fatal("noncanonical resolved payload")
		}
	})
	for _, c := range []struct {
		name string
		raw  []byte
	}{
		{"different-metadata-same-id", bytes.Replace(base, []byte("Northstar"), []byte("Halden"), 1)},
		{"different-identifier-same-id", bytes.Replace(base, []byte("AX-100"), []byte("AX-101"), 1)},
		{"partition-presence-conflict", bytes.Replace(base, []byte(`"id":`), []byte(`"partition_id":"north","id":`), 1)},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, err := New().Resolve(request, scope, plan, makeEvidence(base, c.raw)); err == nil {
				t.Fatal("cross-probe payload conflict accepted")
			}
		})
	}
	t.Run("duplicate-id-within-probe", func(t *testing.T) {
		evidence := makeEvidence(base)
		candidates := evidence[0].Candidates()
		evidence[0], err = adapter.NewProbeEvidence(adapter.ProbeEvidenceSpec{Probe: plan.Probes()[0], RequestedPool: 128, Candidates: append(candidates, candidates[0])})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := New().Resolve(request, scope, plan, evidence); err == nil {
			t.Fatal("same-probe duplicate accepted")
		}
	})
	t.Run("probe-order-conflict", func(t *testing.T) {
		evidence := makeEvidence(base)
		evidence[0], evidence[1] = evidence[1], evidence[0]
		if _, err := New().Resolve(request, scope, plan, evidence); err == nil {
			t.Fatal("reordered evidence accepted")
		}
	})
}
