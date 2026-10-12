// Negative compile fixture: misusing the generated request types or the recipe
// wrapper must fail to compile. The acceptance harness requires TypeScript
// diagnostics here while src/ compiles cleanly.
import { KurnSearchClient } from "@kurn-dev/kurn-search-client-private";
import type { PartsCatalogSuggestRequest, SearchRequest } from "@kurn-dev/kurn-search-client-private";
import { searchPartsSuggest } from "../src/parts-suggest.js";
import type { PartsCatalogSuggestInput } from "../src/parts-suggest.js";

// The legacy generic request type is unchanged: arrays are not admitted there,
// whatever route string the call names.
const arrayOnLegacyType: SearchRequest = { query: "AX-100", limit: 25, filters: { category: ["bearing", "filter"] } };

// The fixed-route request type: unknown filter name, wrong member type and a
// status member outside the closed vocabulary.
const unknownFilterName: PartsCatalogSuggestRequest = { query: "AX-100", limit: 25, filters: { supplier: ["Northstar"] } };
const wrongMemberType: PartsCatalogSuggestRequest = { query: "AX-100", limit: 25, filters: { category: [1, 2] } };
const wrongStatusMember: PartsCatalogSuggestRequest = { query: "AX-100", limit: 25, filters: { status: ["active", "retired"] } };

// Recipe wrapper: the same closed vocabulary, and a missing limit.
const wrapperWrongStatus: PartsCatalogSuggestInput = { query: "AX-100", limit: 25, filters: { status: "retired" } };
const wrapperMissingLimit: PartsCatalogSuggestInput = { query: "AX-100" };

const client = new KurnSearchClient({ endpoint: "http://127.0.0.1:1", getToken: () => "" });
// Arrays through the legacy method are a compile error even on the new route literal.
void client.search("parts-catalog-suggest", { query: "AX-100", limit: 25, filters: { category: ["bearing"] } }, { timeoutMs: 2000 });
// The fixed-route method rejects a wrong option type.
void client.searchPartsCatalogSuggest({ query: "AX-100", limit: 25 }, { timeoutMs: "2000" });
void searchPartsSuggest(client, wrapperWrongStatus, { timeoutMs: 2000 });
void arrayOnLegacyType;
void unknownFilterName;
void wrongMemberType;
void wrongStatusMember;
void wrapperMissingLimit;
