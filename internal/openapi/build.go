package openapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"

	"github.com/kurn-dev/kurn-search/adapter"
	"github.com/kurn-dev/kurn-search/profile"
	"github.com/kurn-dev/kurn-search/service"
)

const precedence = "authenticate (401/403) → method (405) → media type (415) → route (404) → body bound/parse (400) → adapter-version pin (409) → trusted scope (403) → preflight (400/504) → admission (503/504) → execution."

func ServiceContract() (Contract, error) {
	var document Contract
	decoder := json.NewDecoder(bytes.NewReader(service.ContractJSON()))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil || document.Schema != "kurn-search/service-contract/v1" ||
		document.Route != Route || document.RetryAfter.MaximumSeconds != 5 || document.RetryAfter.OverBound != "drop" ||
		len(document.Server) != 7 || len(document.Statuses) != 28 {
		return Contract{}, errors.New("openapi: invalid service contract")
	}
	return document, nil
}

func BuildDocument() (map[string]any, error) {
	contract, err := ServiceContract()
	if err != nil {
		return nil, err
	}
	groups := statusGroups(contract.Statuses)
	responses := map[string]any{"200": successResponseObject()}
	for _, status := range sortedStatuses(groups) {
		responses[strconv.Itoa(status)] = errorResponseObject(status, groups[status])
	}
	document := map[string]any{
		"openapi":           OpenAPIVersion,
		"jsonSchemaDialect": DialectURI,
		"info": map[string]any{
			"title":       "Kurn Search API",
			"version":     InfoVersion,
			"description": "Canonical external description of kurn-search/service-contract/v1.",
		},
		"paths": map[string]any{
			Route: map[string]any{
				"post": map[string]any{
					"operationId": OperationID,
					"summary":     "Search one reviewed adapter",
					"description": "Refusal precedence: " + precedence + " Physical-list names and stamps in successful responses are opaque provenance, not request inputs or management identifiers.",
					"security":    []any{map[string]any{"BearerAuth": []any{}}},
					"parameters": []any{map[string]any{
						"name": "adapter", "in": "path", "required": true,
						"description": "Stable reviewed adapter route.",
						"schema":      map[string]any{"type": "string", "minLength": 1, "maxLength": 64, "pattern": "^[a-z0-9]+(-[a-z0-9]+)*$"},
					}},
					"requestBody": map[string]any{
						"required": true,
						"content": map[string]any{MediaType: map[string]any{
							"schema":   map[string]any{"$ref": "#/components/schemas/SearchRequest"},
							"examples": requestExamples(),
						}},
					},
					"responses": responses,
				},
			},
		},
		"components": map[string]any{
			"securitySchemes": map[string]any{
				"BearerAuth": map[string]any{"type": "http", "scheme": "bearer", "description": "Bearer authentication; trusted tenant scope is constructed only by the service."},
			},
			"schemas": componentSchemas(groups),
		},
	}
	return document, nil
}

func BuildDocumentJSON() ([]byte, error) {
	document, err := BuildDocument()
	if err != nil {
		return nil, err
	}
	return CanonicalJSON(document)
}

func componentSchemas(groups map[int][]string) map[string]any {
	schemas := map[string]any{
		"AdapterIdentity": closedObject([]string{"behavior_sha256", "name", "version"}, map[string]any{
			"behavior_sha256": map[string]any{"type": "string", "pattern": "^[0-9a-f]{64}$", "description": "Deterministic adapter behavior identity."},
			"name":            nonemptyString("Reviewed adapter name."),
			"version":         nonemptyString("Reviewed adapter version."),
		}),
		"Hit": closedObject([]string{"entry_id", "result"}, map[string]any{
			"entry_id": map[string]any{"type": "string", "minLength": 1, "description": fmt.Sprintf("Opaque entry identity; runtime maximum %d UTF-8 bytes.", adapter.MaxEntryIDBytes)},
			"result":   map[string]any{"type": []any{"object", "array", "string", "number", "boolean"}, "description": "Non-null opaque JSON result. Thin clients map this to unknown or Any."},
		}),
		"ListProvenance": closedObject([]string{"name", "role", "stamp"}, map[string]any{
			"name":  nonemptyString("Opaque physical-list name returned only as audit provenance."),
			"role":  nonemptyString("Reviewed adapter list role."),
			"stamp": nonemptyString("Opaque served list stamp returned only as audit provenance."),
		}),
		"SearchRequest": closedObject([]string{"limit", "query"}, map[string]any{
			"adapter_version": map[string]any{"type": "string", "minLength": 1, "description": "Optional exact adapter-version pin; runtime maximum 32 UTF-8 bytes."},
			"filters":         requestFiltersSchema(),
			"limit":           map[string]any{"type": "integer", "minimum": 1, "maximum": profile.HardMaxResultLimit},
			"partition_id":    map[string]any{"type": "string", "minLength": 1, "description": "Optional caller-controlled partition identity; runtime maximum 4096 UTF-8 bytes."},
			"query":           map[string]any{"type": "string", "minLength": 1, "maxLength": profile.HardMaxQueryRunes, "description": "Search text; length counts Unicode code points."},
		}),
		"SearchSuccess": closedObject([]string{"adapter", "complete", "hits", "lists", "pool", "pool_step", "timing_us", "version_attempt"}, map[string]any{
			"adapter":         map[string]any{"$ref": "#/components/schemas/AdapterIdentity"},
			"complete":        map[string]any{"type": "boolean", "const": true},
			"hits":            map[string]any{"type": "array", "minItems": 0, "maxItems": profile.HardMaxResultLimit, "items": map[string]any{"$ref": "#/components/schemas/Hit"}, "description": "Complete hits in adapter-defined ranking order."},
			"lists":           map[string]any{"type": "array", "minItems": 1, "items": map[string]any{"$ref": "#/components/schemas/ListProvenance"}, "description": "Opaque provenance in canonical role-then-name order."},
			"pool":            positiveInteger(),
			"pool_step":       positiveInteger(),
			"timing_us":       map[string]any{"$ref": "#/components/schemas/TimingUS"},
			"version_attempt": positiveInteger(),
		}),
		"SearchFacetsSuccess": closedObject([]string{"adapter", "complete", "facets", "hits", "lists", "pool", "pool_step", "timing_us", "total_matches", "version_attempt"}, map[string]any{
			"adapter":         map[string]any{"$ref": "#/components/schemas/AdapterIdentity"},
			"complete":        map[string]any{"type": "boolean", "const": true},
			"facets":          map[string]any{"$ref": "#/components/schemas/Facets"},
			"hits":            map[string]any{"type": "array", "minItems": 0, "maxItems": profile.HardMaxResultLimit, "items": map[string]any{"$ref": "#/components/schemas/Hit"}, "description": "Complete hits in adapter-defined ranking order: the prefix of the complete matched set with length min(limit, total_matches)."},
			"lists":           map[string]any{"type": "array", "minItems": 1, "items": map[string]any{"$ref": "#/components/schemas/ListProvenance"}, "description": "Opaque provenance in canonical role-then-name order."},
			"pool":            positiveInteger(),
			"pool_step":       positiveInteger(),
			"timing_us":       map[string]any{"$ref": "#/components/schemas/TimingUS"},
			"total_matches":   map[string]any{"type": "integer", "minimum": 0, "maximum": 999, "description": "Exact size of the complete, trusted, currently filtered matched set before the caller limit is applied."},
			"version_attempt": positiveInteger(),
		}),
		"SearchFacetAlternativesSuccess": closedObject([]string{"adapter", "complete", "facets", "hits", "lists", "pool", "pool_step", "timing_us", "total_matches", "version_attempt"}, map[string]any{
			"adapter":  map[string]any{"$ref": "#/components/schemas/AdapterIdentity"},
			"complete": map[string]any{"type": "boolean", "const": true},
			"facets":   map[string]any{"$ref": "#/components/schemas/FacetAlternatives"},
			"hits": map[string]any{"type": "array", "minItems": 0, "maxItems": profile.HardMaxResultLimit,
				"items":       map[string]any{"$ref": "#/components/schemas/Hit"},
				"description": "All-filter ranked hit prefix. The group-specific alternative bases precede caller limiting."},
			"lists": map[string]any{"type": "array", "minItems": 1,
				"items": map[string]any{"$ref": "#/components/schemas/ListProvenance"}},
			"pool": positiveInteger(), "pool_step": positiveInteger(),
			"timing_us": map[string]any{"$ref": "#/components/schemas/TimingUS"},
			"total_matches": map[string]any{"type": "integer", "minimum": 0, "maximum": 999,
				"description": "Exact all-filter total before the hit limit."},
			"version_attempt": positiveInteger(),
		}),
		"FacetBucket": closedObject([]string{"count", "value"}, map[string]any{
			"count": map[string]any{"type": "integer", "minimum": 1, "maximum": 999, "description": "Rows in the complete matched set carrying this payload-exact value."},
			"value": map[string]any{"type": "string", "minLength": 1, "maxLength": 512, "description": "Payload-exact source string; at most 512 Unicode scalar values."},
		}),
		"FacetBucketList": map[string]any{
			"type": "array", "minItems": 0, "maxItems": 999,
			"items":       map[string]any{"$ref": "#/components/schemas/FacetBucket"},
			"description": "Buckets ordered by count descending, then by value in JavaScript UTF-16 code-unit order; each group's counts sum exactly to total_matches.",
		},
		"Facets": closedObject([]string{"category", "manufacturer", "status"}, map[string]any{
			"category":     map[string]any{"$ref": "#/components/schemas/FacetBucketList"},
			"manufacturer": map[string]any{"$ref": "#/components/schemas/FacetBucketList"},
			"status":       map[string]any{"$ref": "#/components/schemas/FacetBucketList"},
		}),
		"FacetAlternativeGroup": closedObject([]string{"basis_total", "buckets"}, map[string]any{
			"basis_total": map[string]any{"type": "integer", "minimum": 0, "maximum": 999,
				"description": "Exact matched count after all selections except this group's own selection."},
			"buckets": map[string]any{"type": "array", "minItems": 0, "maxItems": 999,
				"items":       map[string]any{"$ref": "#/components/schemas/FacetBucket"},
				"description": "Positive counts sum exactly to basis_total; ordered by count then UTF-16 value."},
		}),
		"FacetAlternatives": closedObject([]string{"category", "manufacturer", "status"}, map[string]any{
			"category":     map[string]any{"$ref": "#/components/schemas/FacetAlternativeGroup"},
			"manufacturer": map[string]any{"$ref": "#/components/schemas/FacetAlternativeGroup"},
			"status":       map[string]any{"$ref": "#/components/schemas/FacetAlternativeGroup"},
		}),
		"TimingUS": closedObject([]string{"backend", "residual", "service"}, map[string]any{
			"backend":  nonnegativeInteger(),
			"residual": nonnegativeInteger(),
			"service":  nonnegativeInteger(),
		}),
	}
	for _, status := range sortedStatuses(groups) {
		properties := map[string]any{
			"category": map[string]any{"type": "string", "enum": stringValues(groups[status])},
		}
		if status == 429 || status == 503 {
			properties["retry_after_seconds"] = map[string]any{"type": "integer", "minimum": 1, "maximum": 5, "description": "Optional bounded backend or service hint; it is not an automatic-retry policy."}
		}
		schemas[errorSchemaName(status)] = closedObject([]string{"category"}, properties)
	}
	return schemas
}

func successResponseObject() map[string]any {
	return map[string]any{
		"description": "Complete answer only; partial answers are never returned.",
		"headers": map[string]any{
			"Cache-Control": headerConst("no-store"),
			"Content-Type":  headerConst(MediaType),
		},
		"content": map[string]any{MediaType: map[string]any{
			"schema": map[string]any{
				"description": "Ordinary routes return SearchSuccess; parts-catalog-discover-facets returns SearchFacetsSuccess, and parts-catalog-discover-facet-alternatives returns SearchFacetAlternativesSuccess with self-excluding bases.",
				"oneOf": []any{
					map[string]any{"$ref": "#/components/schemas/SearchSuccess"},
					map[string]any{"$ref": "#/components/schemas/SearchFacetsSuccess"},
					map[string]any{"$ref": "#/components/schemas/SearchFacetAlternativesSuccess"},
				},
			},
			"examples": successExamples(),
		}},
	}
}

func errorResponseObject(status int, categories []string) map[string]any {
	headers := map[string]any{
		"Cache-Control":          headerConst("no-store"),
		"Content-Type":           headerConst(MediaType),
		"X-Content-Type-Options": headerConst("nosniff"),
	}
	if status == 401 {
		headers["WWW-Authenticate"] = headerConst("Bearer")
	}
	if status == 405 {
		headers["Allow"] = headerConst("POST")
	}
	if status == 429 || status == 503 {
		headers["Retry-After"] = map[string]any{"description": "Optional integral 1–5 second hint, identical to the body carrier.", "schema": map[string]any{"type": "integer", "minimum": 1, "maximum": 5}}
	}
	return map[string]any{
		"description": errorDescription(status, categories),
		"headers":     headers,
		"content": map[string]any{MediaType: map[string]any{
			"schema":   map[string]any{"$ref": "#/components/schemas/" + errorSchemaName(status)},
			"examples": errorExamples(status, categories),
		}},
	}
}

func closedObject(required []string, properties map[string]any) map[string]any {
	sort.Strings(required)
	return map[string]any{"type": "object", "additionalProperties": false, "properties": properties, "required": stringValues(required)}
}

func requestFiltersSchema() map[string]any {
	properties := map[string]any{}
	for _, name := range []string{"category", "manufacturer", "status"} {
		maxItems := 16
		if name == "status" {
			maxItems = 2
		}
		properties[name] = map[string]any{
			"type": []any{"string", "array"}, "minLength": 1, "maxLength": 512,
			"minItems": 1, "maxItems": maxItems,
			"items": map[string]any{"type": "string", "minLength": 1, "maxLength": 512},
		}
	}
	schema := closedObject([]string{}, properties)
	schema["description"] = "Optional adapter-dependent literal filters: parts-catalog accepts scalar equality; parts-catalog-select additionally accepts arrays (OR within a field, AND across fields and trusted scope). Omission or {} adds no business restriction; an empty array is invalid. Supplied array counts apply before deduplication. Status vocabulary is adapter-owned; catalog-identifier rejects any present filters member. Values are not trimmed or case-folded; lengths count Unicode scalar values."
	return schema
}

func nonemptyString(description string) map[string]any {
	return map[string]any{"type": "string", "minLength": 1, "description": description}
}

func positiveInteger() map[string]any    { return map[string]any{"type": "integer", "minimum": 1} }
func nonnegativeInteger() map[string]any { return map[string]any{"type": "integer", "minimum": 0} }

func headerConst(value string) map[string]any {
	return map[string]any{"required": true, "schema": map[string]any{"type": "string", "const": value}}
}

func errorSchemaName(status int) string { return "Error" + strconv.Itoa(status) }

func errorDescription(status int, categories []string) string {
	if status == 405 {
		return "Method refusal returned for a non-POST request, including an authenticated unknown route. Categories: " + joinCategories(categories)
	}
	return "Closed service refusal. Categories: " + joinCategories(categories)
}

func joinCategories(categories []string) string {
	var output string
	for index, category := range categories {
		if index != 0 {
			output += ", "
		}
		output += category
	}
	return output + "."
}

func statusGroups(statuses map[string]int) map[int][]string {
	groups := map[int][]string{}
	for category, status := range statuses {
		groups[status] = append(groups[status], category)
	}
	for status := range groups {
		sort.Strings(groups[status])
	}
	return groups
}

func sortedStatuses(groups map[int][]string) []int {
	statuses := make([]int, 0, len(groups))
	for status := range groups {
		statuses = append(statuses, status)
	}
	sort.Ints(statuses)
	return statuses
}

func stringValues(values []string) []any {
	result := make([]any, len(values))
	for index, value := range values {
		result[index] = value
	}
	return result
}

func requestExamples() map[string]any {
	return map[string]any{
		"minimal": map[string]any{"summary": "Minimal request", "value": map[string]any{"limit": 25, "query": "AX-104"}},
		"pinned":  map[string]any{"summary": "Caller partition and adapter pin", "value": map[string]any{"adapter_version": "v1", "limit": 25, "partition_id": "partition-neutral", "query": "AX-104"}},
	}
}

func successExamples() map[string]any {
	base := map[string]any{
		"adapter":  map[string]any{"behavior_sha256": repeat("a", 64), "name": "catalog-identifier", "version": "v1"},
		"complete": true,
		"lists":    []any{map[string]any{"name": "catalog-v1", "role": "identifiers", "stamp": repeat("b", 64) + "@1+j0+c" + repeat("c", 64)}},
		"pool":     128, "pool_step": 1,
		"timing_us":       map[string]any{"backend": 7, "residual": 4, "service": 11},
		"version_attempt": 1,
	}
	zero := cloneMap(base)
	zero["hits"] = []any{}
	nonempty := cloneMap(base)
	nonempty["hits"] = []any{map[string]any{"entry_id": "neutral-1", "result": map[string]any{"label": "AX-104"}}}
	faceted := cloneMap(base)
	faceted["hits"] = []any{map[string]any{"entry_id": "neutral-1", "result": map[string]any{
		"category": "gasket", "id": "neutral-1", "identifier": "AX-104", "manufacturer": "Northstar",
		"name": "Exhaust gasket", "status": "active", "tenant_id": "tenant-a"}}}
	faceted["adapter"] = map[string]any{"behavior_sha256": repeat("a", 64), "name": "parts-catalog-discover-facets", "version": "v1"}
	faceted["total_matches"] = 1
	faceted["facets"] = map[string]any{
		"category":     []any{map[string]any{"count": 1, "value": "gasket"}},
		"manufacturer": []any{map[string]any{"count": 1, "value": "Northstar"}},
		"status":       []any{map[string]any{"count": 1, "value": "active"}},
	}
	alternatives := cloneMap(faceted)
	alternatives["adapter"] = map[string]any{"behavior_sha256": repeat("a", 64), "name": "parts-catalog-discover-facet-alternatives", "version": "v1"}
	alternatives["facets"] = map[string]any{
		"category":     map[string]any{"basis_total": 1, "buckets": []any{map[string]any{"count": 1, "value": "gasket"}}},
		"manufacturer": map[string]any{"basis_total": 2, "buckets": []any{map[string]any{"count": 1, "value": "Northstar"}, map[string]any{"count": 1, "value": "Vega"}}},
		"status":       map[string]any{"basis_total": 1, "buckets": []any{map[string]any{"count": 1, "value": "active"}}},
	}
	return map[string]any{
		"zero_hit":           map[string]any{"summary": "Complete zero-hit answer", "value": zero},
		"nonempty":           map[string]any{"summary": "Complete ranked answer", "value": nonempty},
		"facets":             map[string]any{"summary": "Complete faceted answer", "value": faceted},
		"facet_alternatives": map[string]any{"summary": "Complete self-excluding facet alternatives", "value": alternatives},
	}
}

func errorExamples(status int, categories []string) map[string]any {
	ordinary := map[string]any{"category": categories[0]}
	result := map[string]any{"ordinary": map[string]any{"value": ordinary}}
	if status == 429 || status == 503 {
		result["bounded_hint"] = map[string]any{"value": map[string]any{"category": categories[0], "retry_after_seconds": 3}}
	}
	return result
}

func cloneMap(input map[string]any) map[string]any {
	result := make(map[string]any, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

func repeat(value string, count int) string {
	var output string
	for range count {
		output += value
	}
	return output
}
