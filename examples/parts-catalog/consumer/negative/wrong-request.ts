// Negative compile fixture: misusing the generated request type or the recipe
// wrapper must fail to compile. The acceptance harness requires TypeScript
// diagnostics here while src/ compiles cleanly.
import { KurnSearchClient } from "@kurn-dev/kurn-search-client-private";
import type { SearchRequest } from "@kurn-dev/kurn-search-client-private";
import { searchParts } from "../src/parts.js";
import type { PartsCatalogRequest } from "../src/parts.js";

// Generic client type: unknown filter name and wrong value type.
const unknownFilterName: SearchRequest = { query: "AX-100", limit: 25, filters: { colour: "red" } };
const wrongFilterType: SearchRequest = { query: "AX-100", limit: 25, filters: { status: 1 } };
const missingLimit: SearchRequest = { query: "AX-100" };

// Recipe wrapper: status outside the closed vocabulary and an unknown name.
const wrongStatus: PartsCatalogRequest = { query: "AX-100", limit: 25, filters: { status: "retired" } };
const wrapperUnknownName: PartsCatalogRequest = { query: "AX-100", limit: 25, filters: { supplier: "Northstar" } };

const client = new KurnSearchClient({ endpoint: "http://127.0.0.1:1", getToken: () => "" });
void client.search("parts-catalog", missingLimit, { timeoutMs: "2000" });
void searchParts(client, wrongStatus, { timeoutMs: 2000 });
void unknownFilterName;
void wrongFilterType;
void wrongStatus;
void wrapperUnknownName;
