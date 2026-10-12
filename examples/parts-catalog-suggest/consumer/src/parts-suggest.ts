// Typed convenience layer for the parts-catalog-suggest recipe.
//
// The generated client exposes the recipe through one fixed-route method,
// `searchPartsCatalogSuggest`, whose request type admits a string or a readonly
// string array per business field. This module names the recipe's record
// shape, narrows status to the recipe's two-value vocabulary (a wrong literal
// fails at compile time) and passes the request through unchanged. It adds no
// retries, no client-side filtering and no result reshaping: the service
// validates every request at runtime, including values that arrive
// dynamically rather than as object literals, and refuses an empty array.
import type { KurnSearchClient, PartsCatalogSuggestRequest, SearchSuccess } from "@kurn-dev/kurn-search-client-private";

export const PARTS_CATALOG_SUGGEST_ADAPTER = "parts-catalog-suggest";

export type PartsCatalogStatus = "active" | "discontinued";

// A field is one value or several alternatives (OR within the field). Fields
// combine with AND. Member order and duplicates do not change the answer; a
// one-member array means the same as the scalar.
export interface PartsCatalogSuggestFilters {
  readonly manufacturer?: string | readonly string[];
  readonly category?: string | readonly string[];
  readonly status?: PartsCatalogStatus | readonly PartsCatalogStatus[];
}

export interface PartsCatalogSuggestInput {
  readonly query: string;
  readonly limit: number;
  readonly partitionId?: string;
  readonly filters?: PartsCatalogSuggestFilters;
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

export function toSuggestRequest(request: PartsCatalogSuggestInput): PartsCatalogSuggestRequest {
  const wire: PartsCatalogSuggestRequest = { query: request.query, limit: request.limit };
  if (request.partitionId !== undefined) {
    wire.partitionId = request.partitionId;
  }
  if (request.filters !== undefined) {
    wire.filters = request.filters;
  }
  return wire;
}

export function searchPartsSuggest(
  client: KurnSearchClient,
  request: PartsCatalogSuggestInput,
  options: { timeoutMs: number; signal?: AbortSignal },
): Promise<SearchSuccess> {
  return client.searchPartsCatalogSuggest(toSuggestRequest(request), options);
}
