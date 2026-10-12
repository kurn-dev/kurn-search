package kurnhttp

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func unfilteredResult(list, stamp, candidates string) string {
	return fmt.Sprintf(`{"candidates":%s,"versions":{%q:%q},"took_us":2}`, candidates, list, stamp)
}

func batchEnvelope(results string) string {
	return `{"results":` + results + `,"took_us":3}`
}

func TestDecodeUnfilteredSuccessAndImmutableValues(t *testing.T) {
	probe := mustProbe(t, validProbeSpec())
	candidate := `{"list":"catalog-v1","entry_id":"e1","score":98.5,"key":"Alpha","payload":{"private":"value"}}`
	raw := batchEnvelope(`[` + unfilteredResult("catalog-v1", testStamp, `[`+candidate+`]`) + `]`)
	result, err := decodeBatch([]byte(raw), []Probe{probe})
	if err != nil {
		t.Fatal(err)
	}
	if result.TookUS() != 3 || len(result.Results()) != 1 {
		t.Fatal("batch shape mismatch")
	}
	probeResult := result.Results()[0]
	if probeResult.TookUS() != 2 || probeResult.Stamp().Raw != testStamp ||
		probeResult.AppliedFilter() != nil {
		t.Fatal("probe evidence mismatch")
	}
	if _, ok := probeResult.FilterStats(); ok {
		t.Fatal("unfiltered result exposed filter stats")
	}
	candidates := probeResult.Candidates()
	if len(candidates) != 1 || candidates[0].List() != "catalog-v1" ||
		candidates[0].EntryID() != "e1" || candidates[0].Score() != 98.5 ||
		candidates[0].Key() != "Alpha" || string(candidates[0].Payload()) != `{"private":"value"}` {
		t.Fatalf("candidate mismatch")
	}

	results := result.Results()
	results[0] = ProbeResult{}
	candidates[0] = Candidate{}
	payload := probeResult.Candidates()[0].Payload()
	payload[0] = '['
	if len(result.Results()[0].Candidates()) != 1 ||
		string(result.Results()[0].Candidates()[0].Payload()) != `{"private":"value"}` {
		t.Fatal("success values were mutated through an accessor")
	}
}

func TestDecodeAdditiveUnknownMembers(t *testing.T) {
	filter := mustFilter(t, FilterTerm{Name: "factory", Value: "north"})
	spec := validProbeSpec()
	spec.Filter = filter
	probe := mustProbe(t, spec)
	raw := fmt.Sprintf(`{
		"unknown_batch":{"v":1},
		"results":[{
			"unknown_result":true,
			"candidates":[{"unknown_candidate":[],"list":"catalog-v1","entry_id":"e1","score":1,"key":"k"}],
			"versions":{"unknown_version_note":null,"catalog-v1":%q},
			"filter":{"factory":"north"},
			"filter_stats":{"catalog-v1":{"evaluated":1,"rejected":0,"unknown_stat":"ok"}},
			"took_us":0
		}],
		"took_us":0
	}`, testStamp)
	// versions is itself a closed list-to-stamp identity map; an extra key is
	// not additive metadata and must be refused. Prove that boundary first.
	_, err := decodeBatch([]byte(raw), []Probe{probe})
	requireCategory(t, err, CategoryListIdentityMismatch)

	raw = strings.Replace(raw, `"versions":{"unknown_version_note":null,"catalog-v1"`, `"versions":{"catalog-v1"`, 1)
	result, err := decodeBatch([]byte(raw), []Probe{probe})
	if err != nil {
		t.Fatalf("additive envelope member rejected: %v", err)
	}
	if len(result.Results()[0].Candidates()) != 1 {
		t.Fatal("additive response lost candidate")
	}
}

func TestDecodeBatchAndResultFailures(t *testing.T) {
	probe := mustProbe(t, validProbeSpec())
	good := unfilteredResult("catalog-v1", testStamp, `[]`)
	tests := []struct {
		id       string
		raw      string
		category Category
	}{
		{id: "empty", raw: ``, category: CategoryBackendProtocol},
		{id: "array", raw: `[]`, category: CategoryBackendProtocol},
		{id: "trailing", raw: batchEnvelope(`[`+good+`]`) + `{}`, category: CategoryBackendProtocol},
		{id: "duplicate-results", raw: `{"results":[],"results":[],"took_us":0}`, category: CategoryBackendProtocol},
		{id: "duplicate-unknown", raw: `{"results":[],"took_us":0,"x":1,"x":2}`, category: CategoryBackendProtocol},
		{id: "missing-results", raw: `{"took_us":0}`, category: CategoryBackendProtocol},
		{id: "wrong-results", raw: `{"results":{},"took_us":0}`, category: CategoryBackendProtocol},
		{id: "count", raw: `{"results":[],"took_us":0}`, category: CategoryBackendProtocol},
		{id: "missing-took", raw: `{"results":[]}`, category: CategoryBackendProtocol},
		{id: "negative-took", raw: `{"results":[],"took_us":-1}`, category: CategoryBackendProtocol},
		{id: "fractional-took", raw: `{"results":[],"took_us":1.5}`, category: CategoryBackendProtocol},
		{id: "null-took", raw: `{"results":[],"took_us":null}`, category: CategoryBackendProtocol},
		{id: "result-scalar", raw: `{"results":[1],"took_us":0}`, category: CategoryBackendProtocol},
		{id: "result-missing-candidates", raw: fmt.Sprintf(`{"results":[{"versions":{"catalog-v1":%q},"took_us":0}],"took_us":0}`, testStamp), category: CategoryBackendProtocol},
		{id: "result-wrong-candidates", raw: fmt.Sprintf(`{"results":[{"candidates":{},"versions":{"catalog-v1":%q},"took_us":0}],"took_us":0}`, testStamp), category: CategoryBackendProtocol},
		{id: "result-duplicate", raw: fmt.Sprintf(`{"results":[{"candidates":[],"candidates":[],"versions":{"catalog-v1":%q},"took_us":0}],"took_us":0}`, testStamp), category: CategoryBackendProtocol},
		{id: "result-negative-took", raw: fmt.Sprintf(`{"results":[{"candidates":[],"versions":{"catalog-v1":%q},"took_us":-1}],"took_us":0}`, testStamp), category: CategoryBackendProtocol},
		{id: "result-null-took", raw: fmt.Sprintf(`{"results":[{"candidates":[],"versions":{"catalog-v1":%q},"took_us":null}],"took_us":0}`, testStamp), category: CategoryBackendProtocol},
	}
	for _, test := range tests {
		t.Run(test.id, func(t *testing.T) {
			_, err := decodeBatch([]byte(test.raw), []Probe{probe})
			requireCategory(t, err, test.category)
		})
	}
}

func TestDecodeRejectsInvalidUTF8(t *testing.T) {
	probe := mustProbe(t, validProbeSpec())
	raw := []byte(batchEnvelope(`[` + unfilteredResult("catalog-v1", testStamp, `[]`) + `]`))
	raw = append(raw[:len(raw)-1], 0xff, '}')
	_, err := decodeBatch(raw, []Probe{probe})
	requireCategory(t, err, CategoryBackendProtocol)
}

func TestInlineErrorPoisonsWholeBatchAndStaysOpaque(t *testing.T) {
	probe := mustProbe(t, validProbeSpec())
	good := unfilteredResult("catalog-v1", testStamp, `[]`)
	tests := []struct {
		id       string
		inline   string
		category Category
	}{
		{id: "opaque", inline: `{"error":"rate limit Retry-After: 1 secret"}`, category: CategoryBackendInline},
		{id: "additive", inline: `{"error":"opaque","future":true}`, category: CategoryBackendInline},
		{id: "empty", inline: `{"error":""}`, category: CategoryBackendProtocol},
		{id: "wrong-type", inline: `{"error":429}`, category: CategoryBackendProtocol},
		{id: "duplicate", inline: `{"error":"x","error":"y"}`, category: CategoryBackendProtocol},
		{id: "mixed-candidates", inline: `{"error":"x","candidates":[]}`, category: CategoryBackendProtocol},
		{id: "mixed-versions", inline: `{"error":"x","versions":{}}`, category: CategoryBackendProtocol},
	}
	for _, test := range tests {
		t.Run(test.id, func(t *testing.T) {
			raw := batchEnvelope(`[` + good + `,` + test.inline + `]`)
			_, err := decodeBatch([]byte(raw), []Probe{probe, probe})
			got := requireCategory(t, err, test.category)
			if got.Retryable || got.RetryAfter != 0 || strings.Contains(got.Error(), "secret") {
				t.Fatalf("inline classification leaked or inferred prose: %#v", got)
			}
		})
	}
}

func TestDecodeListStampAndCandidateInvariants(t *testing.T) {
	probe := mustProbe(t, validProbeSpec())
	otherStamp := strings.Repeat("d", 64) + "@2+j0+c" + testConfigHash
	wrongConfig := testBaseHash + "@2+j0+c" + strings.Repeat("d", 64)
	tests := []struct {
		id       string
		result   string
		category Category
	}{
		{id: "versions-missing", result: `{"candidates":[],"took_us":0}`, category: CategoryBackendProtocol},
		{id: "versions-wrong-type", result: `{"candidates":[],"versions":[],"took_us":0}`, category: CategoryListIdentityMismatch},
		{id: "versions-empty", result: `{"candidates":[],"versions":{},"took_us":0}`, category: CategoryListIdentityMismatch},
		{id: "versions-extra", result: fmt.Sprintf(`{"candidates":[],"versions":{"catalog-v1":%q,"other":%q},"took_us":0}`, testStamp, testStamp), category: CategoryListIdentityMismatch},
		{id: "versions-wrong-list", result: fmt.Sprintf(`{"candidates":[],"versions":{"other":%q},"took_us":0}`, testStamp), category: CategoryListIdentityMismatch},
		{id: "versions-duplicate", result: fmt.Sprintf(`{"candidates":[],"versions":{"catalog-v1":%q,"catalog-v1":%q},"took_us":0}`, testStamp, testStamp), category: CategoryListIdentityMismatch},
		{id: "stamp-empty", result: `{"candidates":[],"versions":{"catalog-v1":"empty@0+j0"},"took_us":0}`, category: CategoryListIdentityMismatch},
		{id: "config", result: fmt.Sprintf(`{"candidates":[],"versions":{"catalog-v1":%q},"took_us":0}`, wrongConfig), category: CategoryListIdentityMismatch},
		{id: "candidate-list", result: fmt.Sprintf(`{"candidates":[{"list":"other","entry_id":"e1","score":1,"key":"k"}],"versions":{"catalog-v1":%q},"took_us":0}`, testStamp), category: CategoryListIdentityMismatch},
		{id: "candidate-empty-id", result: fmt.Sprintf(`{"candidates":[{"list":"catalog-v1","entry_id":"","score":1,"key":"k"}],"versions":{"catalog-v1":%q},"took_us":0}`, testStamp), category: CategoryBackendProtocol},
		{id: "candidate-duplicate-id", result: fmt.Sprintf(`{"candidates":[{"list":"catalog-v1","entry_id":"e1","score":1,"key":"k"},{"list":"catalog-v1","entry_id":"e1","score":2,"key":"k2"}],"versions":{"catalog-v1":%q},"took_us":0}`, testStamp), category: CategoryBackendProtocol},
		{id: "candidate-missing-key", result: fmt.Sprintf(`{"candidates":[{"list":"catalog-v1","entry_id":"e1","score":1}],"versions":{"catalog-v1":%q},"took_us":0}`, testStamp), category: CategoryBackendProtocol},
		{id: "candidate-wrong-score", result: fmt.Sprintf(`{"candidates":[{"list":"catalog-v1","entry_id":"e1","score":"1","key":"k"}],"versions":{"catalog-v1":%q},"took_us":0}`, testStamp), category: CategoryBackendProtocol},
		{id: "candidate-null-score", result: fmt.Sprintf(`{"candidates":[{"list":"catalog-v1","entry_id":"e1","score":null,"key":"k"}],"versions":{"catalog-v1":%q},"took_us":0}`, testStamp), category: CategoryBackendProtocol},
		{id: "candidate-null-key", result: fmt.Sprintf(`{"candidates":[{"list":"catalog-v1","entry_id":"e1","score":1,"key":null}],"versions":{"catalog-v1":%q},"took_us":0}`, testStamp), category: CategoryBackendProtocol},
		{id: "candidate-duplicate-member", result: fmt.Sprintf(`{"candidates":[{"list":"catalog-v1","entry_id":"e1","entry_id":"e2","score":1,"key":"k"}],"versions":{"catalog-v1":%q},"took_us":0}`, testStamp), category: CategoryBackendProtocol},
		{id: "candidate-invalid-payload", result: fmt.Sprintf(`{"candidates":[{"list":"catalog-v1","entry_id":"e1","score":1,"key":"k","payload":{"x":}],"versions":{"catalog-v1":%q},"took_us":0}`, testStamp), category: CategoryBackendProtocol},
	}
	for _, test := range tests {
		t.Run(test.id, func(t *testing.T) {
			_, err := decodeBatch([]byte(batchEnvelope(`[`+test.result+`]`)), []Probe{probe})
			requireCategory(t, err, test.category)
		})
	}

	first := unfilteredResult("catalog-v1", testStamp, `[]`)
	second := unfilteredResult("catalog-v1", otherStamp, `[]`)
	_, err := decodeBatch([]byte(batchEnvelope(`[`+first+`,`+second+`]`)), []Probe{probe, probe})
	requireCategory(t, err, CategoryListIdentityMismatch)
	if _, err := decodeBatch([]byte(batchEnvelope(`[`+first+`,`+first+`]`)), []Probe{probe, probe}); err != nil {
		t.Fatalf("same-list identical stamps rejected: %v", err)
	}
}

func TestDecodeFilterEchoAndStatsInvariants(t *testing.T) {
	filter := mustFilter(t, FilterTerm{Name: "factory", Value: "north"})
	spec := validProbeSpec()
	spec.Filter = filter
	filtered := mustProbe(t, spec)
	unfiltered := mustProbe(t, validProbeSpec())
	base := fmt.Sprintf(`"candidates":[],"versions":{"catalog-v1":%q},"took_us":0`, testStamp)
	good := `{` + base + `,"filter":{"factory":"north"},"filter_stats":{"catalog-v1":{"evaluated":2,"rejected":2}}}`
	result, err := decodeBatch([]byte(batchEnvelope(`[`+good+`]`)), []Probe{filtered})
	if err != nil {
		t.Fatal(err)
	}
	stats, ok := result.Results()[0].FilterStats()
	if !ok || stats != (FilterStats{Evaluated: 2, Rejected: 2}) || len(result.Results()[0].Candidates()) != 0 {
		t.Fatal("filtered empty success lost evidence")
	}

	tests := []struct {
		id       string
		probe    Probe
		result   string
		category Category
	}{
		{id: "missing-echo", probe: filtered, result: `{` + base + `,"filter_stats":{"catalog-v1":{"evaluated":1,"rejected":0}}}`, category: CategoryFilterMismatch},
		{id: "changed-echo", probe: filtered, result: `{` + base + `,"filter":{"factory":"south"},"filter_stats":{"catalog-v1":{"evaluated":1,"rejected":0}}}`, category: CategoryFilterMismatch},
		{id: "extra-echo", probe: filtered, result: `{` + base + `,"filter":{"factory":"north","variant":"v1"},"filter_stats":{"catalog-v1":{"evaluated":1,"rejected":0}}}`, category: CategoryFilterMismatch},
		{id: "whitespace-echo", probe: filtered, result: `{` + base + `,"filter": { "factory" : "north" },"filter_stats":{"catalog-v1":{"evaluated":1,"rejected":0}}}`, category: CategoryFilterMismatch},
		{id: "duplicate-echo", probe: filtered, result: `{` + base + `,"filter":{"factory":"north","factory":"north"},"filter_stats":{"catalog-v1":{"evaluated":1,"rejected":0}}}`, category: CategoryFilterMismatch},
		{id: "missing-stats", probe: filtered, result: `{` + base + `,"filter":{"factory":"north"}}`, category: CategoryFilterMismatch},
		{id: "stats-extra-list", probe: filtered, result: `{` + base + `,"filter":{"factory":"north"},"filter_stats":{"catalog-v1":{"evaluated":1,"rejected":0},"other":{"evaluated":0,"rejected":0}}}`, category: CategoryFilterMismatch},
		{id: "stats-wrong-list", probe: filtered, result: `{` + base + `,"filter":{"factory":"north"},"filter_stats":{"other":{"evaluated":1,"rejected":0}}}`, category: CategoryFilterMismatch},
		{id: "stats-missing-evaluated", probe: filtered, result: `{` + base + `,"filter":{"factory":"north"},"filter_stats":{"catalog-v1":{"rejected":0}}}`, category: CategoryBackendProtocol},
		{id: "stats-missing-rejected", probe: filtered, result: `{` + base + `,"filter":{"factory":"north"},"filter_stats":{"catalog-v1":{"evaluated":1}}}`, category: CategoryBackendProtocol},
		{id: "stats-negative", probe: filtered, result: `{` + base + `,"filter":{"factory":"north"},"filter_stats":{"catalog-v1":{"evaluated":-1,"rejected":0}}}`, category: CategoryBackendProtocol},
		{id: "stats-impossible", probe: filtered, result: `{` + base + `,"filter":{"factory":"north"},"filter_stats":{"catalog-v1":{"evaluated":1,"rejected":2}}}`, category: CategoryBackendProtocol},
		{id: "stats-wrong-type", probe: filtered, result: `{` + base + `,"filter":{"factory":"north"},"filter_stats":{"catalog-v1":{"evaluated":"1","rejected":0}}}`, category: CategoryBackendProtocol},
		{id: "stats-null", probe: filtered, result: `{` + base + `,"filter":{"factory":"north"},"filter_stats":{"catalog-v1":{"evaluated":null,"rejected":null}}}`, category: CategoryBackendProtocol},
		{id: "stats-duplicate", probe: filtered, result: `{` + base + `,"filter":{"factory":"north"},"filter_stats":{"catalog-v1":{"evaluated":1,"evaluated":1,"rejected":0}}}`, category: CategoryBackendProtocol},
		{id: "unfiltered-echo", probe: unfiltered, result: `{` + base + `,"filter":{"factory":"north"}}`, category: CategoryFilterMismatch},
		{id: "unfiltered-stats", probe: unfiltered, result: `{` + base + `,"filter_stats":{"catalog-v1":{"evaluated":1,"rejected":0}}}`, category: CategoryFilterMismatch},
	}
	for _, test := range tests {
		t.Run(test.id, func(t *testing.T) {
			_, err := decodeBatch([]byte(batchEnvelope(`[`+test.result+`]`)), []Probe{test.probe})
			requireCategory(t, err, test.category)
		})
	}
}

func TestMembershipExpectedWireSnapshotAndRawEvidenceCopies(t *testing.T) {
	terms := []MembershipTerm{
		{Name: "tenant_id", Values: []string{"\u2028<&"}},
		{Name: "category", Values: []string{"&", "A"}},
		{Name: "partition_id", Values: []string{"\u2029>"}},
	}
	filter, err := NewMembershipFilter(terms)
	if err != nil {
		t.Fatal(err)
	}
	canonical := "{\"category\":{\"in\":[\"&\",\"A\"]},\"partition_id\":\"\u2029>\",\"tenant_id\":\"\u2028<&\"}"
	const wire = `{"category":{"in":["\u0026","A"]},"partition_id":"\u2029\u003e","tenant_id":"\u2028\u003c\u0026"}`
	spec := validProbeSpec()
	spec.Membership = filter
	probe := mustProbe(t, spec)
	if string(probe.filterJSON) != canonical || string(probe.expectedEchoJSON) != wire {
		t.Fatal("request and wire snapshots are not the independent goldens")
	}
	terms[0].Values[0] = "changed"
	copyCanonical := filter.CanonicalJSON()
	copyCanonical[0] = '!'
	filter.canonical[0] = '!'
	filter.terms[0].Values[0] = "changed"
	if string(probe.filterJSON) != canonical || string(probe.expectedEchoJSON) != wire {
		t.Fatal("probe snapshots alias input/filter/accessor bytes")
	}
	// Even the two private byte snapshots must not alias each other.
	probe.filterJSON[0] = '!'
	if string(probe.expectedEchoJSON) != wire {
		t.Fatal("wire snapshot aliases canonical request")
	}
	probe.filterJSON[0] = '{'
	probe.expectedEchoJSON[0] = '!'
	if string(probe.filterJSON) != canonical {
		t.Fatal("canonical request aliases wire snapshot")
	}
	probe.expectedEchoJSON[0] = '{'
	requestBefore := bytes.Clone(marshalBatch([]Probe{probe}))
	raw := []byte(membershipEchoResponse(wire))
	result, err := decodeBatch(raw, []Probe{probe})
	if err != nil || len(result.Results()) != 1 {
		t.Fatal("affected trusted scope refused", err)
	}
	for i := range raw {
		raw[i] = '!'
	}
	echoCopy := result.Results()[0].AppliedFilter()
	echoCopy[0] = '!'
	if string(result.Results()[0].AppliedFilter()) != wire || !bytes.Equal(marshalBatch([]Probe{probe}), requestBefore) {
		t.Fatal("returned raw transport evidence aliases buffers or request")
	}
	_, err = decodeBatch([]byte(membershipEchoResponse(wire)), []Probe{mustProbe(t, validProbeSpec())})
	requireCategory(t, err, CategoryFilterMismatch)
	legacySpec := validProbeSpec()
	legacySpec.Filter = mustFilter(t, FilterTerm{Name: "tenant_id", Value: "\u2028<&"}, FilterTerm{Name: "partition_id", Value: "\u2029>"})
	legacy := mustProbe(t, legacySpec)
	const legacyWire = `{"partition_id":"\u2029\u003e","tenant_id":"\u2028\u003c\u0026"}`
	if _, err = decodeBatch([]byte(membershipEchoResponse(legacyWire)), []Probe{legacy}); err != nil {
		t.Fatal("scalar fixed-encoder scope echo refused", err)
	}
	_, err = decodeBatch([]byte(membershipEchoResponse(string(legacy.filterJSON))), []Probe{legacy})
	requireCategory(t, err, CategoryFilterMismatch)
}

func TestScalarExpectedWireSnapshotAndRawEvidenceCopies(t *testing.T) {
	terms := []FilterTerm{{Name: "tenant_id", Value: "\u2028<&"}, {Name: "category", Value: "A&"}, {Name: "partition_id", Value: "\u2029>"}}
	filter, err := NewStringFilter(terms)
	if err != nil {
		t.Fatal(err)
	}
	canonical := "{\"category\":\"A&\",\"partition_id\":\"\u2029>\",\"tenant_id\":\"\u2028<&\"}"
	const wire = `{"category":"A\u0026","partition_id":"\u2029\u003e","tenant_id":"\u2028\u003c\u0026"}`
	spec := validProbeSpec()
	spec.Filter = filter
	probe := mustProbe(t, spec)
	terms[0] = FilterTerm{Name: "changed", Value: "changed"}
	canonicalCopy := filter.CanonicalJSON()
	canonicalCopy[0] = '!'
	termCopy := filter.Terms()
	termCopy[0] = FilterTerm{Name: "changed", Value: "changed"}
	if string(filter.CanonicalJSON()) != canonical || filter.Terms()[0].Name != "category" {
		t.Fatal("scalar constructor or accessors alias source terms")
	}
	filter.canonical[0] = '!'
	filter.terms[0] = FilterTerm{Name: "changed", Value: "changed"}
	if string(probe.filterJSON) != canonical || string(probe.expectedEchoJSON) != wire {
		t.Fatal("scalar probe snapshots alias constructor/filter buffers")
	}
	probe.filterJSON[0] = '!'
	if string(probe.expectedEchoJSON) != wire {
		t.Fatal("scalar echo aliases request snapshot")
	}
	probe.filterJSON[0] = '{'
	probe.expectedEchoJSON[0] = '!'
	if string(probe.filterJSON) != canonical {
		t.Fatal("scalar request aliases echo snapshot")
	}
	probe.expectedEchoJSON[0] = '{'
	before := bytes.Clone(marshalBatch([]Probe{probe}))
	raw := []byte(membershipEchoResponse(wire))
	result, err := decodeBatch(raw, []Probe{probe})
	if err != nil || len(result.Results()) != 1 {
		t.Fatal("scalar golden refused", err)
	}
	for i := range raw {
		raw[i] = '!'
	}
	echoCopy := result.Results()[0].AppliedFilter()
	echoCopy[0] = '!'
	if string(result.Results()[0].AppliedFilter()) != wire || !bytes.Equal(marshalBatch([]Probe{probe}), before) {
		t.Fatal("scalar raw response/accessor aliases evidence or request")
	}
	spec.Filter = mustFilter(t, FilterTerm{Name: "category", Value: "A&"})
	spec.Membership, err = NewMembershipFilter([]MembershipTerm{{Name: "category", Values: []string{"A&"}}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = NewProbe(spec)
	requireCategory(t, err, CategoryInvalidRequest)
}

func TestFilteredAffectedNamesAndScalarEmptyValue(t *testing.T) {
	for _, tc := range []struct{ name, field, canonical, wire string }{
		{"less", "f<", `{"f<":"yes"}`, `{"f\u003c":"yes"}`},
		{"greater", "f>", `{"f>":"yes"}`, `{"f\u003e":"yes"}`},
		{"ampersand", "f&", `{"f&":"yes"}`, `{"f\u0026":"yes"}`},
		{"line-separator", "f\u2028", "{\"f\u2028\":\"yes\"}", `{"f\u2028":"yes"}`},
		{"paragraph-separator", "f\u2029", "{\"f\u2029\":\"yes\"}", `{"f\u2029":"yes"}`},
		{"mixed", "f<a>&/'\u2028\u2029z", "{\"f<a>&/'\u2028\u2029z\":\"yes\"}", `{"f\u003ca\u003e\u0026/'\u2028\u2029z":"yes"}`},
	} {
		for _, member := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/membership-%t", tc.name, member), func(t *testing.T) {
				spec := validProbeSpec()
				if member {
					var err error
					spec.Membership, err = NewMembershipFilter([]MembershipTerm{{Name: tc.field, Values: []string{"yes"}}})
					if err != nil {
						t.Fatal(err)
					}
				} else {
					spec.Filter = mustFilter(t, FilterTerm{Name: tc.field, Value: "yes"})
				}
				probe := mustProbe(t, spec)
				if string(probe.filterJSON) != tc.canonical || string(probe.expectedEchoJSON) != tc.wire {
					t.Fatal("affected-name snapshots disagree with literal goldens")
				}
				result, err := decodeBatch([]byte(membershipEchoResponse(tc.wire)), []Probe{probe})
				if err != nil || string(result.Results()[0].AppliedFilter()) != tc.wire {
					t.Fatal("affected name refused", err)
				}
				_, err = decodeBatch([]byte(membershipEchoResponse(tc.canonical)), []Probe{probe})
				requireCategory(t, err, CategoryFilterMismatch)
			})
		}
	}
	t.Run("scalar-empty", func(t *testing.T) {
		spec := validProbeSpec()
		spec.Filter = mustFilter(t, FilterTerm{Name: "f<", Value: ""})
		probe := mustProbe(t, spec)
		if string(probe.filterJSON) != `{"f<":""}` {
			t.Fatal("empty scalar canonical drift")
		}
		result, err := decodeBatch([]byte(membershipEchoResponse(`{"f\u003c":""}`)), []Probe{probe})
		if err != nil || string(result.Results()[0].AppliedFilter()) != `{"f\u003c":""}` {
			t.Fatal("empty scalar value refused", err)
		}
	})
}

// B09/B10 execute hostile evidence against the membership probe itself. The
// legacy table above stays intact; its executions are not counted as these.
func TestDecodeMembershipEchoAndStatsInvariants(t *testing.T) {
	filter, err := NewMembershipFilter([]MembershipTerm{
		{Name: "category", Values: []string{"filter", "bearing"}},
		{Name: "tenant_id", Values: []string{"tenant-a"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	spec := validProbeSpec()
	spec.Membership = filter
	probe := mustProbe(t, spec)
	const echo = `{"category":{"in":["bearing","filter"]},"tenant_id":"tenant-a"}`
	const stats = `{"catalog-v1":{"evaluated":2,"rejected":2}}`
	base := fmt.Sprintf(`"candidates":[],"versions":{"catalog-v1":%q},"took_us":0`, testStamp)
	decode := func(fields string) (BatchResult, error) {
		return decodeBatch([]byte(batchEnvelope(`[{`+base+fields+`}]`)), []Probe{probe})
	}
	t.Run("valid-control", func(t *testing.T) {
		result, err := decode(`,"filter":` + echo + `,"filter_stats":` + stats)
		if err != nil || len(result.Results()) != 1 || string(result.Results()[0].AppliedFilter()) != echo {
			t.Fatalf("membership control: %v", err)
		}
		got, ok := result.Results()[0].FilterStats()
		if !ok || got != (FilterStats{Evaluated: 2, Rejected: 2}) {
			t.Fatal("membership statistics missing")
		}
	})
	for _, test := range []struct{ name, field string }{
		{"missing-echo", ""},
		{"null-echo", `,"filter":null`},
		{"changed-echo", `,"filter":{"category":{"in":["bearing","other"]},"tenant_id":"tenant-a"}`},
		{"reordered-echo", `,"filter":{"tenant_id":"tenant-a","category":{"in":["bearing","filter"]}}`},
		{"noncanonical-echo", `,"filter":{ "category":{"in":["bearing","filter"]},"tenant_id":"tenant-a"}`},
		{"type-changed-echo", `,"filter":{"category":["bearing","filter"],"tenant_id":"tenant-a"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := decode(test.field + `,"filter_stats":` + stats)
			requireCategory(t, err, CategoryFilterMismatch)
		})
	}
	for _, test := range []struct {
		name, field string
		category    Category
	}{
		{"missing-stats", "", CategoryFilterMismatch},
		{"wrong-list", `,"filter_stats":{"other":{"evaluated":2,"rejected":2}}`, CategoryFilterMismatch},
		{"extra-list", `,"filter_stats":{"catalog-v1":{"evaluated":2,"rejected":2},"other":{"evaluated":0,"rejected":0}}`, CategoryFilterMismatch},
		{"missing-evaluated", `,"filter_stats":{"catalog-v1":{"rejected":0}}`, CategoryBackendProtocol},
		{"missing-rejected", `,"filter_stats":{"catalog-v1":{"evaluated":2}}`, CategoryBackendProtocol},
		{"negative-evaluated", `,"filter_stats":{"catalog-v1":{"evaluated":-1,"rejected":0}}`, CategoryBackendProtocol},
		{"negative-rejected", `,"filter_stats":{"catalog-v1":{"evaluated":2,"rejected":-1}}`, CategoryBackendProtocol},
		{"impossible-rejected", `,"filter_stats":{"catalog-v1":{"evaluated":1,"rejected":2}}`, CategoryBackendProtocol},
		{"wrong-type", `,"filter_stats":{"catalog-v1":{"evaluated":"2","rejected":2}}`, CategoryBackendProtocol},
		{"fractional", `,"filter_stats":{"catalog-v1":{"evaluated":2.5,"rejected":2}}`, CategoryBackendProtocol},
		{"null-values", `,"filter_stats":{"catalog-v1":{"evaluated":null,"rejected":null}}`, CategoryBackendProtocol},
		{"duplicate-stat", `,"filter_stats":{"catalog-v1":{"evaluated":2,"evaluated":2,"rejected":2}}`, CategoryBackendProtocol},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := decode(`,"filter":` + echo + test.field)
			requireCategory(t, err, test.category)
		})
	}
}
