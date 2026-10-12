// Package openapi builds and validates the canonical external description of
// the private kurn-search service. It is a conformance package, not a client
// runtime and not an alternate service implementation.
package openapi

import "encoding/json"

const (
	DocumentSchema = "kurn-search/openapi-document/v1"
	FixtureSchema  = "kurn-search/openapi-fixtures/v1"
	OpenAPIVersion = "3.1.0"
	InfoVersion    = "1"
	DialectURI     = "https://spec.openapis.org/oas/3.1/dialect/base"
	OperationID    = "search"
	Route          = "/v1/search/{adapter}"
	MediaType      = "application/json"
	Claim          = "one deterministic canonical OpenAPI description conforms to the delivered private S5.1 service surface under pinned neutral fixtures"
)

var SemanticIdentities = []string{
	"canonical_list_order",
	"duplicate_entry_id",
	"retry_hint_eligibility",
	"status_category_header_coupling",
}

type Contract struct {
	Schema     string         `json:"schema"`
	Route      string         `json:"route"`
	RetryAfter RetryContract  `json:"retry_after"`
	Server     map[string]int `json:"server"`
	Statuses   map[string]int `json:"statuses"`
}

type RetryContract struct {
	MaximumSeconds int    `json:"maximum_seconds"`
	OverBound      string `json:"over_bound"`
}

type FixtureCorpus struct {
	Claim    string        `json:"claim"`
	Counts   FixtureCounts `json:"counts"`
	Fixtures []Fixture     `json:"fixtures"`
	Schema   string        `json:"schema"`
}

type FixtureCounts struct {
	AdmittedErrors   int `json:"admitted_errors"`
	AdmittedRequests int `json:"admitted_requests"`
	AdmittedSuccess  int `json:"admitted_successes"`
	HandlerCases     int `json:"handler_cases"`
	HandlerProofs    int `json:"handler_proofs"`
	Mutations        int `json:"mutations"`
	SchemaProofs     int `json:"schema_proofs"`
	SemanticProofs   int `json:"semantic_proofs"`
	StatusPairs      int `json:"status_pairs"`
	Total            int `json:"total"`
}

type Fixture struct {
	Body        any               `json:"body,omitempty"`
	Category    string            `json:"category,omitempty"`
	Class       string            `json:"class"`
	Expected    string            `json:"expected"`
	HandlerCase string            `json:"handler_case,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
	ID          string            `json:"id"`
	Mutation    string            `json:"mutation,omitempty"`
	Proof       string            `json:"proof"`
	Status      int               `json:"status,omitempty"`
	Target      string            `json:"target,omitempty"`
	Value       int               `json:"value,omitempty"`
	ValueUnit   string            `json:"value_unit,omitempty"`
	Wire        string            `json:"wire,omitempty"`
	WireHex     string            `json:"wire_hex,omitempty"`
}

type HandlerCheck struct {
	Category string `json:"category,omitempty"`
	ID       string `json:"id"`
	Status   int    `json:"status"`
}

type HandlerReport struct {
	Checks []HandlerCheck `json:"checks"`
	Result string         `json:"result"`
	Schema string         `json:"schema"`
}

type Envelope struct {
	Headers map[string]string
	Status  int
	Body    json.RawMessage
}
