package service

import (
	"bytes"
	"encoding/json"
)

type contractDocument struct {
	Schema     string `json:"schema"`
	Route      string `json:"route"`
	RetryAfter struct {
		MaximumSeconds int    `json:"maximum_seconds"`
		OverBound      string `json:"over_bound"`
	} `json:"retry_after"`
	Server struct {
		ReadHeaderTimeoutSeconds int `json:"read_header_timeout_seconds"`
		ReadTimeoutSeconds       int `json:"read_timeout_seconds"`
		WriteGraceSeconds        int `json:"write_grace_seconds"`
		WriteHardCapSeconds      int `json:"write_hard_cap_seconds"`
		IdleTimeoutSeconds       int `json:"idle_timeout_seconds"`
		MaxHeaderBytes           int `json:"max_header_bytes"`
		ShutdownTimeoutSeconds   int `json:"shutdown_timeout_seconds"`
	} `json:"server"`
	Statuses map[string]int `json:"statuses"`
}

func ContractJSON() []byte {
	document := contractDocument{
		Schema: "kurn-search/service-contract/v1",
		Route:  "/v1/search/{adapter}",
		Statuses: map[string]int{
			"adapter_not_found": 404, "adapter_version_mismatch": 409,
			"backend_overloaded": 503, "backend_rate_limited": 429,
			"incomplete_at_pool_ceiling": 422, "internal_failure": 500,
			"invalid_request": 400, "method_not_allowed": 405,
			"operation_deadline": 504, "scope_refused": 403,
			"service_admission_unavailable": 503, "service_queue_full": 503, "unauthenticated": 401,
			"unsupported_media_type":    415,
			"adapter_resolution_failed": 502, "backend_conflict": 502,
			"backend_forbidden": 502, "backend_http_error": 502,
			"backend_inline_error": 502, "backend_not_found": 502,
			"backend_protocol_error": 502, "backend_redirect": 502,
			"backend_unauthorized": 502, "filter_identity_mismatch": 502,
			"invalid_evidence": 502, "list_identity_mismatch": 502,
			"response_too_large": 502, "transport_failure": 502,
		},
	}
	document.RetryAfter.MaximumSeconds = 5
	document.RetryAfter.OverBound = "drop"
	document.Server.ReadHeaderTimeoutSeconds = 2
	document.Server.ReadTimeoutSeconds = 5
	document.Server.WriteGraceSeconds = 5
	document.Server.WriteHardCapSeconds = 605
	document.Server.IdleTimeoutSeconds = 30
	document.Server.MaxHeaderBytes = MaxHeaderBytes
	document.Server.ShutdownTimeoutSeconds = 10
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(document); err != nil {
		return nil
	}
	return output.Bytes()
}
