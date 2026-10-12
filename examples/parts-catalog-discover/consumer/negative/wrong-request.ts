// This file must fail the example's negative TypeScript compilation.
import { KurnSearchClient } from "@kurn-dev/kurn-search-client-private";
import type { PartsCatalogDiscoverRequest, SearchRequest } from "@kurn-dev/kurn-search-client-private";
import { searchPartsDiscover } from "../src/parts-discover.js";
import type { PartsCatalogDiscoverInput } from "../src/parts-discover.js";

const legacyArray: SearchRequest = { query: "ball", limit: 25, filters: { category: ["bearing"] } };
const unknownFilter: PartsCatalogDiscoverRequest = { query: "ball", limit: 25, filters: { supplier: "Northstar" } };
const wrongMember: PartsCatalogDiscoverRequest = { query: "ball", limit: 25, filters: { category: [7] } };
const wrongStatus: PartsCatalogDiscoverRequest = { query: "ball", limit: 25, filters: { status: "retired" } };
const missingLimit: PartsCatalogDiscoverInput = { query: "ball" };

const client = new KurnSearchClient({ endpoint: "http://127.0.0.1:1", getToken: () => "" });
void client.searchPartsCatalogDiscover({ query: "ball", limit: 25 }, { timeoutMs: "2000" });
void client.search("parts-catalog-discover", { query: "ball", limit: 25, filters: { category: ["bearing"] } }, { timeoutMs: 2000 });
void searchPartsDiscover(client, missingLimit, { timeoutMs: 2000 });
void legacyArray; void unknownFilter; void wrongMember; void wrongStatus;
