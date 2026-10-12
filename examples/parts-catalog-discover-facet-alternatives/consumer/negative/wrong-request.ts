// The negative compiler run must reject each of these route-specific mistakes.
import { KurnSearchClient } from "@kurn-dev/kurn-search-client-private";
import type { PartsCatalogDiscoverFacetAlternativesRequest, SearchFacetAlternativesSuccess } from "@kurn-dev/kurn-search-client-private";
const wrongField: PartsCatalogDiscoverFacetAlternativesRequest = { query: "bearing", limit: 25, filters: { supplier: "Northstar" } };
const wrongStatus: PartsCatalogDiscoverFacetAlternativesRequest = { query: "bearing", limit: 25, filters: { status: "retired" } };
const client = new KurnSearchClient({ endpoint: "http://127.0.0.1:1", getToken: () => "" });
void client.searchPartsCatalogDiscoverFacetAlternatives({ query: "bearing", limit: 25 }, { timeoutMs: "2000" });
declare const success: SearchFacetAlternativesSuccess;
const wrongBase: string = success.facets.manufacturer.basis_total;
const wrongBucket: number = success.facets.manufacturer.buckets[0].value;
void wrongField; void wrongStatus; void wrongBase; void wrongBucket;
