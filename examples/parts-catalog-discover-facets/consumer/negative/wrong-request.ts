// This file must fail the example's negative TypeScript compilation.
import { KurnSearchClient } from "@kurn-dev/kurn-search-client-private";
import type { PartsCatalogDiscoverFacetsRequest, SearchFacetsSuccess } from "@kurn-dev/kurn-search-client-private";
import { searchPartsDiscoverFacets } from "../src/parts-discover-facets.js";
import type { PartsCatalogDiscoverFacetsInput } from "../src/parts-discover-facets.js";

const unknownFilter: PartsCatalogDiscoverFacetsRequest = { query: "gasket", limit: 25, filters: { supplier: "Halden" } };
const wrongMember: PartsCatalogDiscoverFacetsRequest = { query: "gasket", limit: 25, filters: { category: [7] } };
const wrongStatus: PartsCatalogDiscoverFacetsRequest = { query: "gasket", limit: 25, filters: { status: "retired" } };
const missingLimit: PartsCatalogDiscoverFacetsInput = { query: "gasket" };

const client = new KurnSearchClient({ endpoint: "http://127.0.0.1:1", getToken: () => "" });
void client.searchPartsCatalogDiscoverFacets({ query: "gasket", limit: 25 }, { timeoutMs: "2000" });
void searchPartsDiscoverFacets(client, missingLimit, { timeoutMs: 2000 });

declare const success: SearchFacetsSuccess;
const wrongTotal: string = success.total_matches;
const wrongGroup: readonly string[] = success.facets.supplier;
const wrongBucket: number = success.facets.category[0].value;
const wrongCount: string = success.facets.manufacturer[0].count;

void unknownFilter; void wrongMember; void wrongStatus;
void wrongTotal; void wrongGroup; void wrongBucket; void wrongCount;
