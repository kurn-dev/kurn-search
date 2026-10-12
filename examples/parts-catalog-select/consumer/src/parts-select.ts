// Typed convenience layer for the parts-catalog-select recipe.
//
// The generated client exposes the recipe through one fixed-route method,
// `searchPartsCatalogSelect`, whose request type admits a string or a readonly
// string array per business field. This module names the recipe's record
// shape, narrows status to the recipe's two-value vocabulary (a wrong literal
// fails at compile time) and passes the request through unchanged. It adds no
// retries, no client-side filtering and no result reshaping: the service
// validates every request at runtime, including values that arrive
// dynamically rather than as object literals, and refuses an empty array.
import type { KurnSearchClient, PartsCatalogSelectRequest, SearchSuccess } from "@kurn-dev/kurn-search-client-private";

export const PARTS_CATALOG_SELECT_ADAPTER = "parts-catalog-select";

export type PartsCatalogStatus = "active" | "discontinued";

// A field is one value or several alternatives (OR within the field). Fields
// combine with AND. Member order and duplicates do not change the answer; a
// one-member array means the same as the scalar.
export interface PartsCatalogSelectFilters {
  readonly manufacturer?: string | readonly string[];
  readonly category?: string | readonly string[];
  readonly status?: PartsCatalogStatus | readonly PartsCatalogStatus[];
}

export interface PartsCatalogSelectInput {
  readonly query: string;
  readonly limit: number;
  readonly partitionId?: string;
  readonly filters?: PartsCatalogSelectFilters;
}

export interface PartsCatalogRecord {
  readonly id: string;
  readonly identifier: string;
  readonly manufacturer: string;
  readonly category: string;
  readonly status: PartsCatalogStatus;
  readonly tenant_id: string;
  readonly partition_id?: string;
}

export function toSelectRequest(request: PartsCatalogSelectInput): PartsCatalogSelectRequest {
  const wire: PartsCatalogSelectRequest = { query: request.query, limit: request.limit };
  if (request.partitionId !== undefined) {
    wire.partitionId = request.partitionId;
  }
  if (request.filters !== undefined) {
    wire.filters = request.filters;
  }
  return wire;
}

export function searchPartsSelect(
  client: KurnSearchClient,
  request: PartsCatalogSelectInput,
  options: { timeoutMs: number; signal?: AbortSignal },
): Promise<SearchSuccess> {
  return client.searchPartsCatalogSelect(toSelectRequest(request), options);
}
