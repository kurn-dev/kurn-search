// Package orchestrator executes bounded adapter plans through a Kurn batch
// backend. It owns no domain schema, authentication, service, or logging.
package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/kurn-dev/kurn-search/backend/kurnhttp"
)

type Category string

const (
	CategoryInvalidInput            Category = "invalid_input"
	CategoryInvalidProfile          Category = "invalid_profile"
	CategoryInvalidPlan             Category = "invalid_plan"
	CategoryInvalidFilter           Category = "invalid_filter"
	CategoryInvalidEvidence         Category = "invalid_evidence"
	CategoryAdapterResolution       Category = "adapter_resolution_failed"
	CategoryIncompleteAtPoolCeiling Category = "incomplete_at_pool_ceiling"
	CategoryListIdentityMismatch    Category = "list_identity_mismatch"
	CategoryTransportFailure        Category = "transport_failure"
	CategoryCanceled                Category = "canceled"
	CategoryDeadlineExceeded        Category = "deadline_exceeded"
	CategoryResponseTooLarge        Category = "response_too_large"
	CategoryBackendProtocol         Category = "backend_protocol_error"
	CategoryFilterMismatch          Category = "filter_identity_mismatch"
	CategoryBackendInline           Category = "backend_inline_error"
	CategoryBackendRateLimited      Category = "backend_rate_limited"
	CategoryBackendOverloaded       Category = "backend_overloaded"
	CategoryBackendUnauthorized     Category = "backend_unauthorized"
	CategoryBackendForbidden        Category = "backend_forbidden"
	CategoryBackendNotFound         Category = "backend_not_found"
	CategoryBackendConflict         Category = "backend_conflict"
	CategoryBackendHTTP             Category = "backend_http_error"
	CategoryBackendRedirect         Category = "backend_redirect"
)

type Error struct {
	Category   Category
	Retryable  bool
	RetryAfter time.Duration
	cause      error
}

func (e *Error) Error() string {
	if e == nil {
		return "orchestrator: invalid_input"
	}
	return fmt.Sprintf("orchestrator: %s", e.Category)
}

func (e *Error) Unwrap() error { return e.cause }

func failure(category Category) *Error { return &Error{Category: category} }

func contextFailure(err error) *Error {
	if errors.Is(err, context.Canceled) {
		return &Error{Category: CategoryCanceled, cause: context.Canceled}
	}
	return &Error{Category: CategoryDeadlineExceeded, cause: context.DeadlineExceeded}
}

func backendFailure(err error) *Error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return contextFailure(err)
	}
	var backend *kurnhttp.Error
	if !errors.As(err, &backend) {
		return failure(CategoryTransportFailure)
	}
	category := mapBackendCategory(backend.Category)
	result := &Error{Category: category, Retryable: backend.Retryable, RetryAfter: backend.RetryAfter}
	if category == CategoryCanceled {
		result.cause = context.Canceled
	} else if category == CategoryDeadlineExceeded {
		result.cause = context.DeadlineExceeded
	}
	return result
}

// artifactVerificationRefusal reports whether a backend failure is the
// prepared-artifact admission refusal. Unlike a legacy list identity
// mismatch it is never retried: a downgraded or foreign artifact does not
// become the expected one by restarting the pool ladder.
func artifactVerificationRefusal(err error) bool {
	var backend *kurnhttp.Error
	return errors.As(err, &backend) && backend.Category == kurnhttp.CategoryArtifactVerificationMismatch
}

func mapBackendCategory(category kurnhttp.Category) Category {
	switch category {
	case kurnhttp.CategoryInvalidRequest:
		return CategoryInvalidInput
	case kurnhttp.CategoryTransportFailure:
		return CategoryTransportFailure
	case kurnhttp.CategoryCanceled:
		return CategoryCanceled
	case kurnhttp.CategoryDeadlineExceeded:
		return CategoryDeadlineExceeded
	case kurnhttp.CategoryResponseTooLarge:
		return CategoryResponseTooLarge
	case kurnhttp.CategoryBackendProtocol:
		return CategoryBackendProtocol
	case kurnhttp.CategoryListIdentityMismatch:
		return CategoryListIdentityMismatch
	case kurnhttp.CategoryArtifactVerificationMismatch:
		// Internal prepared-artifact admission refusal: the public category
		// is the existing list identity mismatch; no new public category.
		return CategoryListIdentityMismatch
	case kurnhttp.CategoryFilterMismatch:
		return CategoryFilterMismatch
	case kurnhttp.CategoryBackendInline:
		return CategoryBackendInline
	case kurnhttp.CategoryBackendRateLimited:
		return CategoryBackendRateLimited
	case kurnhttp.CategoryBackendOverloaded:
		return CategoryBackendOverloaded
	case kurnhttp.CategoryBackendUnauthorized:
		return CategoryBackendUnauthorized
	case kurnhttp.CategoryBackendForbidden:
		return CategoryBackendForbidden
	case kurnhttp.CategoryBackendNotFound:
		return CategoryBackendNotFound
	case kurnhttp.CategoryBackendConflict:
		return CategoryBackendConflict
	case kurnhttp.CategoryBackendHTTP:
		return CategoryBackendHTTP
	case kurnhttp.CategoryBackendRedirect:
		return CategoryBackendRedirect
	default:
		return CategoryBackendProtocol
	}
}
