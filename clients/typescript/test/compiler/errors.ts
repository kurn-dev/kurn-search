import type { ApiErrorValue } from "@kurn-dev/kurn-search-client-private";

type BackendCategory =
  | "adapter_resolution_failed"
  | "backend_conflict"
  | "backend_forbidden"
  | "backend_http_error"
  | "backend_inline_error"
  | "backend_not_found"
  | "backend_protocol_error"
  | "backend_redirect"
  | "backend_unauthorized"
  | "filter_identity_mismatch"
  | "invalid_evidence"
  | "list_identity_mismatch"
  | "response_too_large"
  | "transport_failure";

export function exhaustError(value: ApiErrorValue): string {
  switch (value.status) {
    case 400: {
      const category: "invalid_request" = value.category;
      return category;
    }
    case 401: {
      const category: "unauthenticated" = value.category;
      return category;
    }
    case 403: {
      const category: "scope_refused" = value.category;
      return category;
    }
    case 404: {
      const category: "adapter_not_found" = value.category;
      return category;
    }
    case 405: {
      const category: "method_not_allowed" = value.category;
      return category;
    }
    case 409: {
      const category: "adapter_version_mismatch" = value.category;
      return category;
    }
    case 415: {
      const category: "unsupported_media_type" = value.category;
      return category;
    }
    case 422: {
      const category: "incomplete_at_pool_ceiling" = value.category;
      return category;
    }
    case 429: {
      const category: "backend_rate_limited" = value.category;
      const hint: number | undefined = value.retry_after_seconds;
      void hint;
      return category;
    }
    case 500: {
      const category: "internal_failure" = value.category;
      return category;
    }
    case 502: {
      const category: BackendCategory = value.category;
      return category;
    }
    case 503: {
      const category: "backend_overloaded" | "service_admission_unavailable" = value.category;
      const hint: number | undefined = value.retry_after_seconds;
      void hint;
      return category;
    }
    case 504: {
      const category: "operation_deadline" = value.category;
      return category;
    }
    default: {
      const unreachable: never = value;
      return unreachable;
    }
  }
}

export function retryHint(value: ApiErrorValue): number | undefined {
  if (value.status === 429 || value.status === 503) return value.retry_after_seconds;
  // @ts-expect-error W5-X TS2339: retry hints are not present on other branches.
  return value.retry_after_seconds;
}

// @ts-expect-error W5-X TS2322: status 400 cannot carry a 401 category.
const mismatchedPair: ApiErrorValue = { status: 400, category: "unauthenticated" };
void mismatchedPair;
