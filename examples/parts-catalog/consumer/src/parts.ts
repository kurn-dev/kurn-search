// Typed convenience layer for the parts-catalog recipe.
//
// The generated client is recipe-agnostic: its request type accepts the
// closed `filters` object with three optional string fields. This module
// narrows `status` to the recipe's two-value vocabulary so that a wrong
// status literal fails at compile time, and names the adapter route once.
// It adds no retries, no client-side filtering and no result reshaping: the
// service still validates every request at runtime, including values that
// arrive dynamically rather than as object literals.
import type { KurnSearchClient, SearchRequest, SearchSuccess } from "@kurn-dev/kurn-search-client-private";

export const PARTS_CATALOG_ADAPTER = "parts-catalog";

export type PartsCatalogStatus = "active" | "discontinued";

export interface PartsCatalogFilters {
  readonly manufacturer?: string;
  readonly category?: string;
  readonly status?: PartsCatalogStatus;
}

export interface PartsCatalogRequest {
  readonly query: string;
  readonly limit: number;
  readonly partitionId?: string;
  readonly filters?: PartsCatalogFilters;
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

export function toSearchRequest(request: PartsCatalogRequest): SearchRequest {
  const wire: SearchRequest = { query: request.query, limit: request.limit };
  if (request.partitionId !== undefined) {
    wire.partitionId = request.partitionId;
  }
  if (request.filters !== undefined) {
    wire.filters = request.filters;
  }
  return wire;
}

export function searchParts(
  client: KurnSearchClient,
  request: PartsCatalogRequest,
  options: { timeoutMs: number; signal?: AbortSignal },
): Promise<SearchSuccess> {
  return client.search(PARTS_CATALOG_ADAPTER, toSearchRequest(request), options);
}
