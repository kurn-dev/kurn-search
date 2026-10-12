package openapi

import (
	"sort"
)

func BuildFixtures() (FixtureCorpus, error) {
	contract, err := ServiceContract()
	if err != nil {
		return FixtureCorpus{}, err
	}
	fixtures := []Fixture{
		{ID: "request-minimal", Class: "request", Expected: "admit", Proof: "schema", Body: map[string]any{"limit": 25, "query": "AX-104"}},
		{ID: "request-fully-pinned", Class: "request", Expected: "admit", Proof: "schema", Body: map[string]any{"adapter_version": "v1", "limit": 25, "partition_id": "partition-neutral", "query": "AX-104"}},
		{ID: "request-filters-empty", Class: "request", Expected: "admit", Proof: "schema", Body: filteredRequest(map[string]any{})},
		{ID: "request-filters-single", Class: "request", Expected: "admit", Proof: "schema", Body: filteredRequest(map[string]any{"manufacturer": "Northstar"})},
		{ID: "request-filters-all", Class: "request", Expected: "admit", Proof: "schema", Body: filteredRequest(map[string]any{"category": "bearing", "manufacturer": "Northstar", "status": "active"})},
		{ID: "request-filters-value-array", Class: "request", Expected: "admit", Proof: "schema", Body: filteredRequest(map[string]any{"category": []any{"bearing"}})},
		{ID: "request-filters-multiselect", Class: "request", Expected: "admit", Proof: "schema", Body: filteredRequest(map[string]any{"category": []any{"bearing", "filter"}, "manufacturer": "Northstar", "status": []any{"active", "discontinued"}})},
		{ID: "request-filters-array-duplicates", Class: "request", Expected: "admit", Proof: "schema", Body: filteredRequest(map[string]any{"status": []any{"active", "active"}})},
		{ID: "request-filters-array-unicode-bound", Class: "request", Expected: "admit", Proof: "schema", Body: filteredRequest(map[string]any{"manufacturer": []any{repeat("😀", 512)}})},
	}
	success := successExamples()
	fixtures = append(fixtures,
		Fixture{ID: "success-zero-hit", Class: "success", Expected: "admit", Proof: "schema", Status: 200, Headers: successHeaders(), Body: exampleValue(success["zero_hit"])},
		Fixture{ID: "success-nonempty", Class: "success", Expected: "admit", Proof: "schema", Status: 200, Headers: successHeaders(), Body: exampleValue(success["nonempty"])},
		Fixture{ID: "success-facets", Class: "success", Expected: "admit", Proof: "schema", Status: 200, Headers: successHeaders(), Body: exampleValue(success["facets"])},
		Fixture{ID: "success-facet-alternatives", Class: "success", Expected: "admit", Proof: "schema", Status: 200, Headers: successHeaders(), Body: exampleValue(success["facet_alternatives"])},
	)
	categories := make([]string, 0, len(contract.Statuses))
	for category := range contract.Statuses {
		categories = append(categories, category)
	}
	sort.Strings(categories)
	for _, category := range categories {
		status := contract.Statuses[category]
		headers := errorHeaders(status)
		fixtures = append(fixtures, Fixture{
			ID: "error-" + category, Class: "error", Expected: "admit", Proof: "schema", Status: status,
			Category: category, Headers: headers, Body: map[string]any{"category": category},
		})
	}
	fixtures = append(fixtures, mutationFixtures()...)
	fixtures = append(fixtures, handlerFixtures()...)
	sort.Slice(fixtures, func(i, j int) bool { return fixtures[i].ID < fixtures[j].ID })
	counts := FixtureCounts{StatusPairs: len(contract.Statuses), Total: len(fixtures)}
	for _, fixture := range fixtures {
		switch {
		case fixture.Class == "request" && fixture.Expected == "admit":
			counts.AdmittedRequests++
		case fixture.Class == "success" && fixture.Expected == "admit":
			counts.AdmittedSuccess++
		case fixture.Class == "error" && fixture.Expected == "admit":
			counts.AdmittedErrors++
		case fixture.Class == "handler":
			counts.HandlerCases++
		case fixture.Class == "mutation":
			counts.Mutations++
		}
		switch fixture.Proof {
		case "schema":
			counts.SchemaProofs++
		case "semantic":
			counts.SemanticProofs++
		case "handler":
			counts.HandlerProofs++
		}
	}
	return FixtureCorpus{Claim: Claim, Counts: counts, Fixtures: fixtures, Schema: FixtureSchema}, nil
}

func BuildFixturesJSON() ([]byte, error) {
	fixtures, err := BuildFixtures()
	if err != nil {
		return nil, err
	}
	return CanonicalJSON(fixtures)
}

func mutationFixtures() []Fixture {
	specs := []Fixture{
		{ID: "request-filters-null", Target: "request", Body: filteredRequest(nil)},
		{ID: "request-filters-array", Target: "request", Body: filteredRequest([]any{})},
		{ID: "request-filters-string", Target: "request", Body: filteredRequest("active")},
		{ID: "request-filters-number", Target: "request", Body: filteredRequest(1)},
		{ID: "request-filters-unknown-name", Target: "request", Body: filteredRequest(map[string]any{"tenant_id": "tenant-a"})},
		{ID: "request-filters-value-null", Target: "request", Body: filteredRequest(map[string]any{"category": nil})},
		{ID: "request-filters-value-boolean", Target: "request", Body: filteredRequest(map[string]any{"category": true})},
		{ID: "request-filters-value-number", Target: "request", Body: filteredRequest(map[string]any{"category": 1})},
		{ID: "request-filters-array-empty", Target: "request", Body: filteredRequest(map[string]any{"category": []any{}})},
		{ID: "request-filters-array-category-over", Target: "request", Body: filteredRequest(map[string]any{"category": repeatedMembers("bearing", 17)})},
		{ID: "request-filters-array-manufacturer-over", Target: "request", Body: filteredRequest(map[string]any{"manufacturer": repeatedMembers("Northstar", 17)})},
		{ID: "request-filters-array-status-over", Target: "request", Body: filteredRequest(map[string]any{"status": repeatedMembers("active", 3)})},
		{ID: "request-filters-array-member-null", Target: "request", Body: filteredRequest(map[string]any{"category": []any{"bearing", nil}})},
		{ID: "request-filters-array-member-boolean", Target: "request", Body: filteredRequest(map[string]any{"category": []any{"bearing", true}})},
		{ID: "request-filters-array-member-number", Target: "request", Body: filteredRequest(map[string]any{"category": []any{"bearing", 1}})},
		{ID: "request-filters-array-member-array", Target: "request", Body: filteredRequest(map[string]any{"category": []any{[]any{"bearing"}}})},
		{ID: "request-filters-array-member-object", Target: "request", Body: filteredRequest(map[string]any{"category": []any{map[string]any{"eq": "bearing"}}})},
		{ID: "request-filters-array-member-empty", Target: "request", Body: filteredRequest(map[string]any{"category": []any{"bearing", ""}})},
		{ID: "request-filters-array-member-over", Target: "request", Body: filteredRequest(map[string]any{"category": []any{repeat("😀", 513)}})},
		{ID: "request-filters-array-member-surrogate", Target: "request", Wire: `{"filters":{"category":["\uD800"]},"limit":25,"query":"AX-100"}`},
		{ID: "request-filters-value-object", Target: "request", Body: filteredRequest(map[string]any{"category": map[string]any{"eq": "bearing"}})},
		{ID: "request-filters-value-empty", Target: "request", Body: filteredRequest(map[string]any{"category": ""})},
		{ID: "request-filters-value-over-scalars", Target: "request", Body: filteredRequest(map[string]any{"category": repeat("😀", 513)})},
		{ID: "request-adapter-version-empty", Target: "request", Mutation: "empty adapter_version"},
		{ID: "request-adapter-version-null", Target: "request", Wire: `{"adapter_version":null,"limit":25,"query":"AX-104"}`},
		{ID: "request-adapter-version-33-bytes", Target: "request", Mutation: "adapter_version bytes", Value: 33, ValueUnit: "utf8_bytes"},
		{ID: "request-body-over-limit", Target: "request", Mutation: "body bytes", Value: 1048577, ValueUnit: "bytes"},
		{ID: "request-duplicate-query", Target: "request", Wire: `{"query":"AX-104","query":"AX-105","limit":25}`},
		{ID: "request-exponent-limit", Target: "request", Wire: `{"limit":1e1,"query":"AX-104"}`},
		{ID: "request-fractional-limit", Target: "request", Wire: `{"limit":1.5,"query":"AX-104"}`},
		{ID: "request-invalid-utf8", Target: "request", WireHex: "7b227175657279223a22ff222c226c696d6974223a32357d"},
		{ID: "request-limit-below", Target: "request", Body: map[string]any{"limit": 0, "query": "AX-104"}},
		{ID: "request-limit-over", Target: "request", Body: map[string]any{"limit": 1001, "query": "AX-104"}},
		{ID: "request-missing-limit", Target: "request", Body: map[string]any{"query": "AX-104"}},
		{ID: "request-missing-query", Target: "request", Body: map[string]any{"limit": 25}},
		{ID: "request-null-query", Target: "request", Body: map[string]any{"limit": 25, "query": nil}},
		{ID: "request-partition-empty", Target: "request", Body: map[string]any{"limit": 25, "partition_id": "", "query": "AX-104"}},
		{ID: "request-partition-4097-bytes", Target: "request", Mutation: "partition_id bytes", Value: 4097, ValueUnit: "utf8_bytes"},
		{ID: "request-query-empty", Target: "request", Body: map[string]any{"limit": 25, "query": ""}},
		{ID: "request-query-over-runes", Target: "request", Mutation: "query code points", Value: 257, ValueUnit: "unicode_code_points"},
		{ID: "request-reversed-surrogates", Target: "request", Wire: `{"limit":25,"query":"\uDC00\uD800"}`},
		{ID: "request-string-limit", Target: "request", Wire: `{"limit":"25","query":"AX-104"}`},
		{ID: "request-trailing-json", Target: "request", Wire: `{"limit":25,"query":"AX-104"}{}`},
		{ID: "request-trusted-scope-smuggling", Target: "request", Body: map[string]any{"limit": 25, "query": "AX-104", "scope_input": "opaque-neutral"}},
		{ID: "request-unknown-member", Target: "request", Body: map[string]any{"extra": true, "limit": 25, "query": "AX-104"}},
		{ID: "request-unpaired-high-surrogate", Target: "request", Wire: `{"limit":25,"query":"\uD800"}`},
		{ID: "request-unpaired-low-surrogate", Target: "request", Wire: `{"limit":25,"query":"\uDC00"}`},
		{ID: "request-wrong-media-type", Target: "request", Mutation: "content-type is not exact application/json"},
		{ID: "request-wrong-query-type", Target: "request", Body: map[string]any{"limit": 25, "query": 104}},
		{ID: "success-adapter-missing", Target: "success", Mutation: "remove adapter"},
		{ID: "success-behavior-digest-invalid", Target: "success", Mutation: "behavior_sha256 is not 64 lowercase hex"},
		{ID: "success-complete-false", Target: "success", Mutation: "complete false"},
		{ID: "success-duplicate-entry-id", Target: "success", Mutation: "duplicate hit entry_id"},
		{ID: "success-entry-id-empty", Target: "success", Mutation: "empty entry_id"},
		{ID: "success-hits-null", Target: "success", Mutation: "hits null"},
		{ID: "success-hits-over-limit", Target: "success", Mutation: "1001 hits", Value: 1001, ValueUnit: "items"},
		{ID: "success-lists-empty", Target: "success", Mutation: "empty lists"},
		{ID: "success-lists-out-of-order", Target: "success", Mutation: "lists not role-then-name ordered"},
		{ID: "success-missing-adapter-behavior", Target: "success", Mutation: "remove adapter.behavior_sha256"},
		{ID: "success-missing-adapter-name", Target: "success", Mutation: "remove adapter.name"},
		{ID: "success-missing-adapter-version", Target: "success", Mutation: "remove adapter.version"},
		{ID: "success-missing-complete", Target: "success", Mutation: "remove complete"},
		{ID: "success-missing-hit-entry-id", Target: "success", Mutation: "remove hit.entry_id"},
		{ID: "success-missing-hit-result", Target: "success", Mutation: "remove hit.result"},
		{ID: "success-missing-hits", Target: "success", Mutation: "remove hits"},
		{ID: "success-missing-list-name", Target: "success", Mutation: "remove list.name"},
		{ID: "success-missing-list-role", Target: "success", Mutation: "remove list.role"},
		{ID: "success-missing-list-stamp", Target: "success", Mutation: "remove list.stamp"},
		{ID: "success-missing-lists", Target: "success", Mutation: "remove lists"},
		{ID: "success-missing-pool", Target: "success", Mutation: "remove pool"},
		{ID: "success-missing-pool-step", Target: "success", Mutation: "remove pool_step"},
		{ID: "success-missing-timing", Target: "success", Mutation: "remove timing_us"},
		{ID: "success-missing-timing-backend", Target: "success", Mutation: "remove timing_us.backend"},
		{ID: "success-missing-timing-residual", Target: "success", Mutation: "remove timing_us.residual"},
		{ID: "success-missing-timing-service", Target: "success", Mutation: "remove timing_us.service"},
		{ID: "success-missing-version-attempt", Target: "success", Mutation: "remove version_attempt"},
		{ID: "success-null-result", Target: "success", Mutation: "hit result null"},
		{ID: "success-non-array-hits", Target: "success", Mutation: "hits is not an array"},
		{ID: "success-extra-adapter-member", Target: "success", Mutation: "add adapter member"},
		{ID: "success-extra-hit-member", Target: "success", Mutation: "add hit member"},
		{ID: "success-extra-list-member", Target: "success", Mutation: "add list member"},
		{ID: "success-extra-timing-member", Target: "success", Mutation: "add timing member"},
		{ID: "success-pool-step-zero", Target: "success", Mutation: "pool_step zero"},
		{ID: "success-pool-zero", Target: "success", Mutation: "pool zero"},
		{ID: "success-timing-negative", Target: "success", Mutation: "negative timing member"},
		{ID: "success-undocumented-member", Target: "success", Mutation: "add response member"},
		{ID: "success-version-attempt-zero", Target: "success", Mutation: "version_attempt zero"},
		{ID: "error-category-status-mismatch", Target: "error", Mutation: "category mapped to a different status"},
		{ID: "error-fractional-hint", Target: "error", Mutation: "fractional body hint"},
		{ID: "error-hint-header-body-mismatch", Target: "error", Mutation: "Retry-After differs from retry_after_seconds"},
		{ID: "error-ineligible-hint", Target: "error", Mutation: "hint on status other than 429 or 503"},
		{ID: "error-raw-backend-prose", Target: "error", Mutation: "undocumented raw prose member"},
		{ID: "error-six-second-hint", Target: "error", Mutation: "hint over maximum", Value: 6, ValueUnit: "seconds"},
		{ID: "error-undocumented-member", Target: "error", Mutation: "add response member"},
		{ID: "error-zero-second-hint", Target: "error", Mutation: "hint below minimum", ValueUnit: "seconds"},
	}
	for index := range specs {
		specs[index].Class = "mutation"
		specs[index].Expected = "refuse"
		specs[index].Proof = "schema"
		switch specs[index].ID {
		case "success-duplicate-entry-id", "success-lists-out-of-order", "error-hint-header-body-mismatch":
			specs[index].Proof = "semantic"
		case "request-wrong-media-type":
			specs[index].Proof = "handler"
			specs[index].HandlerCase = "handler-exact-media-type"
			specs[index].Status = 415
			specs[index].Category = "unsupported_media_type"
		case "request-adapter-version-33-bytes":
			specs[index].Proof = "handler"
			specs[index].HandlerCase = "handler-adapter-version-33"
			specs[index].Status = 400
			specs[index].Category = "invalid_request"
		case "request-partition-4097-bytes":
			specs[index].Proof = "handler"
			specs[index].HandlerCase = "handler-partition-4097"
			specs[index].Status = 400
			specs[index].Category = "invalid_request"
		}
	}
	return specs
}

func filteredRequest(filters any) map[string]any {
	return map[string]any{"filters": filters, "limit": 25, "query": "AX-100"}
}

func repeatedMembers(value string, count int) []any {
	result := make([]any, count)
	for index := range result {
		result[index] = value
	}
	return result
}

func handlerFixtures() []Fixture {
	return []Fixture{
		{ID: "handler-filters-catalog-refusal", Class: "handler", Expected: "refuse", Proof: "handler", HandlerCase: "handler-filters-catalog-refusal", Status: 400, Category: "invalid_request", Body: filteredRequest(map[string]any{"category": []any{"bearing"}})},
		{ID: "handler-filters-parts-refusal", Class: "handler", Expected: "refuse", Proof: "handler", HandlerCase: "handler-filters-parts-refusal", Status: 400, Category: "invalid_request", Body: filteredRequest(map[string]any{"category": []any{"bearing"}})},
		{ID: "handler-filters-select-success", Class: "handler", Expected: "admit", Proof: "handler", HandlerCase: "handler-filters-select-success", Status: 200, Body: filteredRequest(map[string]any{"category": []any{"bearing"}})},
		{ID: "handler-adapter-version-32", Class: "handler", Expected: "refuse", Proof: "handler", HandlerCase: "handler-adapter-version-32", Category: "adapter_version_mismatch", Status: 409, Mutation: "adapter_version bytes", Value: 32, ValueUnit: "utf8_bytes"},
		{ID: "handler-adapter-version-33", Class: "handler", Expected: "refuse", Proof: "handler", HandlerCase: "handler-adapter-version-33", Category: "invalid_request", Status: 400, Mutation: "adapter_version bytes", Value: 33, ValueUnit: "utf8_bytes"},
		{ID: "handler-auth-before-route", Class: "handler", Expected: "refuse", Proof: "handler", HandlerCase: "handler-auth-before-route", Category: "unauthenticated", Status: 401, Mutation: "unauthenticated unknown route"},
		{ID: "handler-exact-media-type", Class: "handler", Expected: "refuse", Proof: "handler", HandlerCase: "handler-exact-media-type", Category: "unsupported_media_type", Status: 415, Mutation: "application/json with parameters"},
		{ID: "handler-method-before-route", Class: "handler", Expected: "refuse", Proof: "handler", HandlerCase: "handler-method-before-route", Category: "method_not_allowed", Status: 405, Mutation: "authenticated non-POST unknown route"},
		{ID: "handler-partition-4096", Class: "handler", Expected: "admit", Proof: "handler", HandlerCase: "handler-partition-4096", Status: 200, Mutation: "partition_id bytes", Value: 4096, ValueUnit: "utf8_bytes"},
		{ID: "handler-partition-4097", Class: "handler", Expected: "refuse", Proof: "handler", HandlerCase: "handler-partition-4097", Category: "invalid_request", Status: 400, Mutation: "partition_id bytes", Value: 4097, ValueUnit: "utf8_bytes"},
		{ID: "handler-zero-hit-array", Class: "handler", Expected: "admit", Proof: "handler", HandlerCase: "handler-zero-hit-array", Status: 200, Mutation: "zero-hit response encodes hits array"},
	}
}

func successHeaders() map[string]string {
	return map[string]string{"Cache-Control": "no-store", "Content-Type": MediaType}
}

func errorHeaders(status int) map[string]string {
	result := map[string]string{"Cache-Control": "no-store", "Content-Type": MediaType, "X-Content-Type-Options": "nosniff"}
	if status == 401 {
		result["WWW-Authenticate"] = "Bearer"
	}
	if status == 405 {
		result["Allow"] = "POST"
	}
	return result
}

func exampleValue(value any) any {
	example, _ := value.(map[string]any)
	return example["value"]
}
