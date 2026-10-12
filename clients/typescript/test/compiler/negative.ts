import {
  KurnSearchClient,
  PROTOCOL_ERROR_REASONS,
} from "@kurn-dev/kurn-search-client-private";
import type {
  SearchRequest,
  SearchSuccess,
  SearchRequest as TypeOnlyRequest,
} from "@kurn-dev/kurn-search-client-private";

// @ts-expect-error W5-N: query is required.
const missingQuery: SearchRequest = { limit: 1 };
// @ts-expect-error W5-N: query must be a string.
const wrongQuery: SearchRequest = { query: 1, limit: 1 };
// @ts-expect-error W5-N: limit is required.
const missingLimit: SearchRequest = { query: "neutral" };
// @ts-expect-error W5-N: limit must be a number.
const wrongLimit: SearchRequest = { query: "neutral", limit: "1" };
// @ts-expect-error W5-N: transport snake_case is not the client surface.
const snakeCase: SearchRequest = { query: "neutral", limit: 1, partition_id: "partition" };
// @ts-expect-error W5-N: trusted tenant scope is never caller input.
const trustedScope: SearchRequest = { query: "neutral", limit: 1, tenant: "tenant" };
// @ts-expect-error W5-N: unexpected request members are closed.
const unexpected: SearchRequest = { query: "neutral", limit: 1, extra: true };
// @ts-expect-error W5-N: exact optional properties reject explicit undefined.
const explicitUndefined: SearchRequest = { query: "neutral", limit: 1, partitionId: undefined };

// @ts-expect-error W5-N: token hooks return strings only.
new KurnSearchClient({ endpoint: "https://search.invalid", getToken: () => 7 });

const client = new KurnSearchClient({ endpoint: "https://search.invalid", getToken: () => "token" });
// @ts-expect-error W5-N: timeoutMs must be numeric.
void client.search("catalog-identifier", { query: "neutral", limit: 1 }, { timeoutMs: "5" });

declare const result: SearchSuccess;
// @ts-expect-error W5-N: success members are readonly.
result.complete = true;
// @ts-expect-error W5-N: runtime values are not declaration type aliases.
let valueUsedAsType: PROTOCOL_ERROR_REASONS;
// @ts-expect-error W5-N: an import-type binding cannot be used as a value.
void TypeOnlyRequest;

void missingQuery;
void wrongQuery;
void missingLimit;
void wrongLimit;
void snakeCase;
void trustedScope;
void unexpected;
void explicitUndefined;
void valueUsedAsType;
