package kurnhttp

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Category is a stable, bounded failure classification. It deliberately
// carries no backend prose or request data.
type Category string

const (
	CategoryInvalidRequest       Category = "invalid_request"
	CategoryTransportFailure     Category = "transport_failure"
	CategoryCanceled             Category = "canceled"
	CategoryDeadlineExceeded     Category = "deadline_exceeded"
	CategoryResponseTooLarge     Category = "response_too_large"
	CategoryBackendProtocol      Category = "backend_protocol_error"
	CategoryListIdentityMismatch Category = "list_identity_mismatch"
	CategoryFilterMismatch       Category = "filter_identity_mismatch"
	CategoryBackendInline        Category = "backend_inline_error"
	CategoryBackendRateLimited   Category = "backend_rate_limited"
	CategoryBackendOverloaded    Category = "backend_overloaded"
	CategoryBackendUnauthorized  Category = "backend_unauthorized"
	CategoryBackendForbidden     Category = "backend_forbidden"
	CategoryBackendNotFound      Category = "backend_not_found"
	CategoryBackendConflict      Category = "backend_conflict"
	CategoryBackendHTTP          Category = "backend_http_error"
	CategoryBackendRedirect      Category = "backend_redirect"
)

// CategoryArtifactVerificationMismatch is internal to Search. Only the
// AdmissionPreparedFull policy produces it, for its added verification
// evidence refusals (header, version basis, base/entries/journal). Every
// pre-existing classification is unchanged.
const CategoryArtifactVerificationMismatch Category = "artifact_verification_mismatch"

// Error is the only reusable backend error shape. Error() never includes the
// query, list, payload, backend message, URL, host, credential, or local path.
type Error struct {
	Category   Category
	StatusCode int
	Retryable  bool
	RetryAfter time.Duration
	cause      error
}

func (e *Error) Error() string {
	if e == nil {
		return "kurnhttp: unknown_error"
	}
	return fmt.Sprintf("kurnhttp: %s", e.Category)
}

// Unwrap preserves only context cancellation identities. Transport errors
// such as url.Error are intentionally not retained because they expose the
// backend URL through their Error strings.
func (e *Error) Unwrap() error { return e.cause }

func classified(category Category, status int, cause error) *Error {
	e := &Error{Category: category, StatusCode: status}
	switch {
	case errors.Is(cause, context.Canceled):
		e.Category = CategoryCanceled
		e.cause = context.Canceled
	case errors.Is(cause, context.DeadlineExceeded):
		e.Category = CategoryDeadlineExceeded
		e.cause = context.DeadlineExceeded
	}
	return e
}
