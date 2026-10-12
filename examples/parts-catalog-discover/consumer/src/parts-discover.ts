import type {
  KurnSearchClient,
  PartsCatalogDiscoverRequest,
  SearchSuccess,
} from "@kurn-dev/kurn-search-client-private";

export const PARTS_CATALOG_DISCOVER_ADAPTER = "parts-catalog-discover";

export type PartsCatalogStatus = "active" | "discontinued";

export interface PartsCatalogDiscoverFilters {
  readonly manufacturer?: string | readonly string[];
  readonly category?: string | readonly string[];
  readonly status?: PartsCatalogStatus | readonly PartsCatalogStatus[];
}

export interface PartsCatalogDiscoverInput {
  readonly query: string;
  readonly limit: number;
  readonly partitionId?: string;
  readonly filters?: PartsCatalogDiscoverFilters;
}

export interface PartsCatalogRecord {
  readonly id: string;
  readonly identifier: string;
  readonly name: string;
  readonly manufacturer: string;
  readonly category: string;
  readonly status: PartsCatalogStatus;
  readonly tenant_id: string;
  readonly partition_id?: string;
}

export function toDiscoverRequest(request: PartsCatalogDiscoverInput): PartsCatalogDiscoverRequest {
  const wire: PartsCatalogDiscoverRequest = { query: request.query, limit: request.limit };
  if (request.partitionId !== undefined) wire.partitionId = request.partitionId;
  if (request.filters !== undefined) wire.filters = request.filters;
  return wire;
}

export function searchPartsDiscover(
  client: KurnSearchClient,
  request: PartsCatalogDiscoverInput,
  options: { timeoutMs: number; signal?: AbortSignal },
): Promise<SearchSuccess> {
  return client.searchPartsCatalogDiscover(toDiscoverRequest(request), options);
}
