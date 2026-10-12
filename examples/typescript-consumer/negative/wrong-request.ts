// Negative compile fixture: misusing the generated request type must fail to
// compile against the package's declarations. The acceptance harness requires
// TypeScript diagnostics here while src/search.ts compiles cleanly.
import { KurnSearchClient } from "@kurn-dev/kurn-search-client-private";
import type { SearchRequest } from "@kurn-dev/kurn-search-client-private";

const missingLimit: SearchRequest = { query: "ABC-123" };
const wrongLimit: SearchRequest = { query: "ABC-123", limit: "10" };
const snakeCase: SearchRequest = { query: "ABC-123", limit: 10, partition_id: "partition-proof" };
const client = new KurnSearchClient({ endpoint: "http://127.0.0.1:1" });
void client.search("catalog-identifier", missingLimit, { timeoutMs: "2000" });
void wrongLimit;
void snakeCase;
