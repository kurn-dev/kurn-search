import type {
  KurnSearchClient,
  PartsCatalogDiscoverFacetsRequest,
  SearchFacetsSuccess,
} from "@kurn-dev/kurn-search-client-private";

export const PARTS_CATALOG_DISCOVER_FACETS_ADAPTER = "parts-catalog-discover-facets";

export type PartsCatalogStatus = "active" | "discontinued";

export interface PartsCatalogDiscoverFacetsFilters {
  readonly manufacturer?: string | readonly string[];
  readonly category?: string | readonly string[];
  readonly status?: PartsCatalogStatus | readonly PartsCatalogStatus[];
}

export interface PartsCatalogDiscoverFacetsInput {
  readonly query: string;
  readonly limit: number;
  readonly partitionId?: string;
  readonly filters?: PartsCatalogDiscoverFacetsFilters;
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

export interface PartsCatalogFacetBucket {
  readonly count: number;
  readonly value: string;
}

export interface PartsCatalogFacets {
  readonly category: readonly PartsCatalogFacetBucket[];
  readonly manufacturer: readonly PartsCatalogFacetBucket[];
  readonly status: readonly PartsCatalogFacetBucket[];
}

export function toDiscoverFacetsRequest(request: PartsCatalogDiscoverFacetsInput): PartsCatalogDiscoverFacetsRequest {
  const wire: PartsCatalogDiscoverFacetsRequest = { query: request.query, limit: request.limit };
  if (request.partitionId !== undefined) wire.partitionId = request.partitionId;
  if (request.filters !== undefined) wire.filters = request.filters;
  return wire;
}

export function searchPartsDiscoverFacets(
  client: KurnSearchClient,
  request: PartsCatalogDiscoverFacetsInput,
  options: { timeoutMs: number; signal?: AbortSignal },
): Promise<SearchFacetsSuccess> {
  return client.searchPartsCatalogDiscoverFacets(toDiscoverFacetsRequest(request), options);
}
