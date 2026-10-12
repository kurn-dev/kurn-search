package openapi

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
)

func TestCanonicalDocumentAndFixtures(t *testing.T) {
	t.Parallel()
	document, err := BuildDocumentJSON()
	validationErr := ValidateDocument(document)
	if err != nil || validationErr != nil {
		t.Fatal("canonical document failed", err, validationErr)
	}
	again, _ := BuildDocumentJSON()
	if !bytes.Equal(document, again) {
		t.Fatal("document generation is not byte-identical")
	}
	fixtures, err := BuildFixturesJSON()
	parsedFixtures, parseErr := StrictDecode(fixtures)
	canonicalFixtures, canonicalErr := CanonicalJSON(parsedFixtures)
	if parseErr != nil || canonicalErr != nil || !bytes.Equal(fixtures, canonicalFixtures) {
		t.Fatal("fixture canonical parseback failed", parseErr, canonicalErr)
	}
	fixtureErr := ValidateFixtures(fixtures, document, fixtureHandlerReport())
	if err != nil || fixtureErr != nil {
		t.Fatal("fixture corpus failed", err, fixtureErr)
	}
	againFixtures, _ := BuildFixturesJSON()
	if !bytes.Equal(fixtures, againFixtures) {
		t.Fatal("fixture generation is not byte-identical")
	}
}

func TestFixtureProofMustMatchRefusalLayer(t *testing.T) {
	t.Parallel()
	document, _ := BuildDocumentJSON()
	corpus, _ := BuildFixtures()
	for index := range corpus.Fixtures {
		if corpus.Fixtures[index].ID == "success-duplicate-entry-id" {
			corpus.Fixtures[index].Proof = "schema"
			corpus.Counts.SemanticProofs--
			corpus.Counts.SchemaProofs++
			break
		}
	}
	raw, _ := CanonicalJSON(corpus)
	if ValidateFixtures(raw, document, fixtureHandlerReport()) == nil {
		t.Fatal("schema proof admitted for semantic-only refusal")
	}
}

func TestStrictDocumentRefusesNonCanonicalAndExcludedForms(t *testing.T) {
	t.Parallel()
	document, _ := BuildDocumentJSON()
	cases := [][]byte{
		append(bytes.Clone(document), ' '),
		[]byte(`{"openapi":"3.1.0","openapi":"3.1.0"}`),
		[]byte(`{"openapi":"3.1.0"}{}`),
		{0xff},
	}
	for _, input := range cases {
		if ValidateDocument(input) == nil {
			t.Fatal("invalid document admitted")
		}
	}

	value, _ := StrictDecode(document)
	mutated := value.(map[string]any)
	components := mutated["components"].(map[string]any)
	schemas := components["schemas"].(map[string]any)
	request := schemas["SearchRequest"].(map[string]any)
	request["oneOf"] = []any{}
	raw, _ := CanonicalJSON(mutated)
	if ValidateDocument(raw) == nil {
		t.Fatal("excluded schema keyword admitted")
	}
}

func TestSemanticChecksAreOutsideSchemaSubset(t *testing.T) {
	t.Parallel()
	document, _ := BuildDocumentJSON()
	fixtures, _ := BuildFixtures()
	var success map[string]any
	for _, fixture := range fixtures.Fixtures {
		if fixture.ID == "success-nonempty" {
			success = fixture.Body.(map[string]any)
			break
		}
	}
	if success == nil {
		t.Fatal("success fixture missing")
	}
	hits := success["hits"].([]any)
	success["hits"] = append(hits, hits[0])
	raw, _ := CanonicalJSON(success)
	if ValidateSuccess(document, raw, successHeaders()) == nil {
		t.Fatal("duplicate entry identity admitted")
	}

	hintBody, _ := CanonicalJSON(map[string]any{"category": "backend_rate_limited", "retry_after_seconds": 3})
	if ValidateError(document, Envelope{Status: 429, Body: hintBody, Headers: map[string]string{
		"Cache-Control": "no-store", "Content-Type": MediaType, "X-Content-Type-Options": "nosniff", "Retry-After": "3",
	}}) != nil {
		t.Fatal("eligible bounded hint refused")
	}
	if ValidateError(document, Envelope{Status: 429, Body: hintBody, Headers: map[string]string{
		"Cache-Control": "no-store", "Content-Type": MediaType, "X-Content-Type-Options": "nosniff", "Retry-After": "4",
	}}) == nil {
		t.Fatal("mismatched hint admitted")
	}
}

func TestRequestSchemaCountsCodePoints(t *testing.T) {
	t.Parallel()
	document, _ := BuildDocumentJSON()
	good, _ := CanonicalJSON(map[string]any{"limit": 1, "query": "😀"})
	if ValidateRequest(document, good) != nil {
		t.Fatal("single astral code point refused")
	}
	bad, _ := CanonicalJSON(map[string]any{"limit": 1001, "query": "AX-104"})
	if ValidateRequest(document, bad) == nil {
		t.Fatal("over-limit request admitted")
	}
}

func fixtureHandlerReport() HandlerReport {
	report := HandlerReport{Result: "PASS-S6-C", Schema: "kurn-search-s6-handler/v1"}
	for _, fixture := range handlerFixtures() {
		report.Checks = append(report.Checks, HandlerCheck{ID: fixture.ID, Status: fixture.Status, Category: fixture.Category})
	}
	return report
}

func TestFilteredRequestSchemaAndHistoricalSubset(t *testing.T) {
	t.Parallel()
	document, err := BuildDocumentJSON()
	if err != nil || ValidateDocument(document) != nil {
		t.Fatal("current document invalid", err)
	}
	// Removing the additive surfaces must reconstruct the complete historic
	// document: the S4 filters request member, the W7-S1 facet additions
	// and the W9 bounded-queue category (503 service_queue_full).
	oldDocument, _ := StrictDecode(document)
	schemas, _ := componentSchemaMap(oldDocument.(map[string]any))
	delete(schemas["SearchRequest"].(map[string]any)["properties"].(map[string]any), "filters")
	schemas["Error503"].(map[string]any)["properties"].(map[string]any)["category"].(map[string]any)["enum"] = []any{"backend_overloaded", "service_admission_unavailable"}
	for _, name := range []string{"SearchFacetsSuccess", "Facets", "FacetBucket", "FacetBucketList", "SearchFacetAlternativesSuccess", "FacetAlternatives", "FacetAlternativeGroup"} {
		delete(schemas, name)
	}
	operation := oldDocument.(map[string]any)["paths"].(map[string]any)["/v1/search/{adapter}"].(map[string]any)["post"].(map[string]any)
	operation["responses"].(map[string]any)["200"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"] = map[string]any{"$ref": "#/components/schemas/SearchSuccess"}
	delete(operation["responses"].(map[string]any)["200"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["examples"].(map[string]any), "facets")
	delete(operation["responses"].(map[string]any)["200"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["examples"].(map[string]any), "facet_alternatives")
	operation["responses"].(map[string]any)["503"].(map[string]any)["description"] = "Closed service refusal. Categories: backend_overloaded, service_admission_unavailable."
	oldDocumentRaw, _ := CanonicalJSON(oldDocument)
	if fmt.Sprintf("%x", sha256.Sum256(oldDocumentRaw)) != "a2156300b863ccb26c78e3961bd7d68fe273813bed19dce70cf0951386687a68" {
		t.Fatal("historical document changed outside the additive filters member")
	}
	corpus, err := BuildFixtures()
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := CanonicalJSON(corpus)
	if err := ValidateFixtures(raw, document, fixtureHandlerReport()); err != nil {
		t.Fatal(err)
	}
	// The handler report above is only a package-unit fixture. The conformance
	// generator separately executes all eleven real handler checks.
	old := corpus
	old.Fixtures = nil
	addedRequests, addedMutations, addedHandlers, addedFacet, addedQueue := 0, 0, 0, 0, 0
	for _, fixture := range corpus.Fixtures {
		if fixture.ID == "error-service_queue_full" {
			// W9 additive bounded-queue refusal category.
			if fixture.Class != "error" || fixture.Proof != "schema" || fixture.Status != 503 {
				t.Fatal("queue-full fixture mislabeled")
			}
			addedQueue++
			continue
		}
		if fixture.ID == "success-facet-alternatives" {
			addedFacet++
			continue
		}
		if fixture.ID == "success-facets" {
			// W7-S1 additive facet success fixture.
			addedFacet++
			continue
		}
		if strings.HasPrefix(fixture.ID, "handler-filters-") {
			if fixture.Class != "handler" || fixture.Proof != "handler" {
				t.Fatal("filter handler proof mislabeled", fixture.ID)
			}
			addedHandlers++
			continue
		}
		if !strings.HasPrefix(fixture.ID, "request-filters-") {
			old.Fixtures = append(old.Fixtures, fixture)
			continue
		}
		if fixture.Proof != "schema" {
			t.Fatal("filter fixture mislabels its proof layer", fixture.ID)
		}
		switch fixture.Class {
		case "request":
			addedRequests++
		case "mutation":
			addedMutations++
		default:
			t.Fatal("unexpected new fixture class", fixture.ID)
		}
	}
	if addedRequests != 7 || addedMutations != 23 || addedHandlers != 3 || addedFacet != 2 || addedQueue != 1 || len(old.Fixtures) != 111 {
		t.Fatal("additive fixture inventory changed", addedRequests, addedMutations, addedHandlers, addedFacet, addedQueue, len(old.Fixtures))
	}
	old.Counts.Total -= addedRequests + addedMutations + addedHandlers + addedFacet + addedQueue
	old.Counts.SchemaProofs -= addedRequests + addedMutations + addedFacet + addedQueue
	old.Counts.AdmittedErrors -= addedQueue
	old.Counts.StatusPairs -= addedQueue
	old.Counts.AdmittedRequests -= addedRequests
	old.Counts.Mutations -= addedMutations
	old.Counts.HandlerCases -= addedHandlers
	old.Counts.HandlerProofs -= addedHandlers
	old.Counts.AdmittedSuccess -= addedFacet
	oldRaw, _ := CanonicalJSON(old)
	if fmt.Sprintf("%x", sha256.Sum256(oldRaw)) != "ceede62c8fcd1a2749b9af3c650cd5328ea44d5c779533548fc7b7054cc6cc8e" {
		t.Fatal("complete historical 111-fixture corpus changed")
	}
}

func TestFilterScalarBoundsAndAdapterOwnedVocabulary(t *testing.T) {
	t.Parallel()
	document, _ := BuildDocumentJSON()
	for _, name := range []string{"category", "manufacturer", "status"} {
		for _, value := range []string{"x", " unknown ", strings.Repeat("😀", 512)} {
			raw, _ := CanonicalJSON(filteredRequest(map[string]any{name: value}))
			if err := ValidateRequest(document, raw); err != nil {
				t.Fatal("schema imposed adapter vocabulary or byte/UTF-16 length", name, err)
			}
		}
		for _, value := range []string{"", strings.Repeat("😀", 513)} {
			raw, _ := CanonicalJSON(filteredRequest(map[string]any{name: value}))
			if ValidateRequest(document, raw) == nil {
				t.Fatal("schema admitted filter length violation", name)
			}
		}
	}
}

func TestExactClosedFilterSchemaCannotDrift(t *testing.T) {
	t.Parallel()
	mutations := map[string]func(map[string]any, map[string]any){
		"required-outer": func(r, f map[string]any) { r["required"] = []any{"filters", "limit", "query"} },
		"missing":        func(r, f map[string]any) { delete(r["properties"].(map[string]any), "filters") },
		"additional":     func(r, f map[string]any) { f["additionalProperties"] = true },
		"required-inner": func(r, f map[string]any) { f["required"] = []any{"status"} },
		"extra-property": func(r, f map[string]any) {
			f["properties"].(map[string]any)["tenant_id"] = map[string]any{"type": "string"}
		},
		"missing-property": func(r, f map[string]any) { delete(f["properties"].(map[string]any), "category") },
		"status-enum": func(r, f map[string]any) {
			f["properties"].(map[string]any)["status"].(map[string]any)["enum"] = []any{"active", "discontinued"}
		},
		"length": func(r, f map[string]any) {
			f["properties"].(map[string]any)["category"].(map[string]any)["maxLength"] = 513
		},
		"value-type": func(r, f map[string]any) {
			f["properties"].(map[string]any)["manufacturer"].(map[string]any)["type"] = "number"
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			document, _ := BuildDocumentJSON()
			value, _ := StrictDecode(document)
			schemas, _ := componentSchemaMap(value.(map[string]any))
			request := schemas["SearchRequest"].(map[string]any)
			filters := request["properties"].(map[string]any)["filters"].(map[string]any)
			mutate(request, filters)
			raw, _ := CanonicalJSON(value)
			if ValidateDocument(raw) == nil {
				t.Fatal("changed filter contract admitted")
			}
		})
	}
}

func TestMembershipSchemaExactUnionAndTypeSpecificBounds(t *testing.T) {
	document, err := BuildDocumentJSON()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"manufacturer", "category", "status"} {
		maxItems := 16
		if name == "status" {
			maxItems = 2
		}
		// A long scalar is not subjected to maxItems; a small array is not
		// subjected to the serialized string's length. Vocabulary stays owned
		// by the selected adapter, not this route-agnostic wire schema.
		for _, value := range []any{repeat("😀", 512), repeatedMembers(repeat("😀", 512), maxItems), []any{"unknown"}} {
			raw, _ := CanonicalJSON(filteredRequest(map[string]any{name: value}))
			if err := ValidateRequest(document, raw); err != nil {
				t.Fatal("valid actual-type boundary refused", name, err)
			}
		}
		for _, value := range []any{repeat("x", 513), []any{}, repeatedMembers("x", maxItems+1), []any{true}, []any{nil}, []any{1}, []any{""}, []any{repeat("😀", 513)}, []any{[]any{"x"}}, []any{map[string]any{"in": []any{"x"}}}} {
			raw, _ := CanonicalJSON(filteredRequest(map[string]any{name: value}))
			if ValidateRequest(document, raw) == nil {
				t.Fatal("invalid actual-type boundary admitted", name)
			}
		}
	}
	mutations := map[string]func(map[string]any){
		"union-reordered": func(f map[string]any) { f["type"] = []any{"array", "string"} },
		"union-null":      func(f map[string]any) { f["type"] = []any{"string", "array", "null"} },
		"union-duplicate": func(f map[string]any) { f["type"] = []any{"string", "string"} },
		"union-number":    func(f map[string]any) { f["type"] = []any{"string", "number"} },
		"scalar-only":     func(f map[string]any) { f["type"] = "string" },
		"array-minimum":   func(f map[string]any) { f["minItems"] = 0 },
		"array-maximum":   func(f map[string]any) { f["maxItems"] = 17 },
		"wrong-field-cap": func(f map[string]any) { f["maxItems"] = 2 },
		"missing-items":   func(f map[string]any) { delete(f, "items") },
		"items-number":    func(f map[string]any) { f["items"].(map[string]any)["type"] = "number" },
		"items-length":    func(f map[string]any) { f["items"].(map[string]any)["maxLength"] = 513 },
		"items-enum":      func(f map[string]any) { f["items"].(map[string]any)["enum"] = []any{"x"} },
		"combinator":      func(f map[string]any) { f["anyOf"] = []any{} },
		"unknown-keyword": func(f map[string]any) { f["contains"] = map[string]any{"type": "string"} },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			value, _ := StrictDecode(document)
			schemas, _ := componentSchemaMap(value.(map[string]any))
			filters := schemas["SearchRequest"].(map[string]any)["properties"].(map[string]any)["filters"].(map[string]any)
			mutate(filters["properties"].(map[string]any)["manufacturer"].(map[string]any))
			raw, _ := CanonicalJSON(value)
			if ValidateDocument(raw) == nil {
				t.Fatal("unsupported union shape admitted")
			}
		})
	}
}
