import {
  ApiError,
  ClientError,
  KurnSearchClient,
  PROTOCOL_ERROR_REASONS,
  ProtocolError,
  SEMANTIC_CHECKS,
  verify,
} from "@kurn-dev/kurn-search-client-private";
import type {
  ApiErrorValue,
  SearchRequest,
  SearchSuccess,
} from "@kurn-dev/kurn-search-client-private";

const syncClient = new KurnSearchClient({
  endpoint: "https://search.invalid",
  getToken: () => "token",
  fetch: globalThis.fetch,
});
const asyncClient = new KurnSearchClient({
  endpoint: "https://search.invalid/prefix",
  getToken: async () => "token",
});
const request: SearchRequest = {
  query: "neutral-query",
  limit: 25,
  partitionId: "neutral-partition",
  adapterVersion: "v1",
};
const minimalRequest: SearchRequest = { query: "neutral-query", limit: 1 };

function consumeSuccess(result: SearchSuccess): number {
  const complete: true = result.complete;
  const adapterName: string = result.adapter.name;
  const firstResult: unknown = result.hits[0]?.result;
  const firstStamp: string | undefined = result.lists[0]?.stamp;
  const timing: number = result.timing_us.backend + result.timing_us.service + result.timing_us.residual;
  void complete;
  void adapterName;
  void firstResult;
  void firstStamp;
  return result.version_attempt + result.pool_step + result.pool + timing;
}

async function exerciseClients(signal: AbortSignal): Promise<SearchSuccess> {
  const first = await syncClient.search("catalog-identifier", request, { timeoutMs: 5000, signal });
  const second = await asyncClient.search("catalog-identifier", minimalRequest, { timeoutMs: 1 });
  consumeSuccess(first);
  return second;
}

const apiErrorValue: ApiErrorValue = { status: 429, category: "backend_rate_limited", retry_after_seconds: 1 };
const values: readonly unknown[] = [
  new ApiError(apiErrorValue),
  new ClientError("neutral"),
  syncClient,
  PROTOCOL_ERROR_REASONS,
  new ProtocolError(undefined, "neutral"),
  SEMANTIC_CHECKS,
  verify(apiErrorValue, { retryAfter: "1" }),
];

void exerciseClients;
void values;
