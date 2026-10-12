# TypeScript client

This directory contains the generated ESM client runtime and TypeScript
declarations for the `kurn-searchd` HTTP contract, licensed under MIT:

- `generated/client.mjs` — runtime;
- `generated/client.d.ts` — declarations; and
- [`../current-manifest.json`](../current-manifest.json) — filtered upstream provenance summary.

The package is not published to npm. Its legacy package name and registry-private
flag are retained for source-example compatibility, not a restriction on its
MIT license. Use it from this checkout
as a local file dependency, or import the generated module directly for a
local test.

The [separate consumer example](../../examples/typescript-consumer/README.md)
and the [manual's quick path](../../docs/user-manual.md) show the real offline
npm local-path install from a fresh external ESM project, then compilation
and execution against the running example index. No install scripts run.
The local client install is offline; the separately pinned compiler may
need its registry when not cached. Nothing is published to npm.

The checked runtime target is Node.js 25.6.1, and the declarations are checked
with pinned TypeScript 6.0.3.

```ts
import { KurnSearchClient } from "@kurn-dev/kurn-search-client-private";

const client = new KurnSearchClient({
  endpoint: "http://127.0.0.1:8081",
  getToken: () => "client-token",
});

const result = await client.search(
  "catalog-identifier",
  { query: "ABC-123", partitionId: "partition-proof", limit: 10 },
  { timeoutMs: 2_000 },
);
```

## Parts-catalog field filters

Prepare and run the parts recipe first, following the
[parts example](../../examples/parts-catalog/README.md) or
[manual](../../docs/user-manual.md). A daemon is configured for one recipe;
changing the route in client code does not load another dataset.

```ts
const parts = await client.search(
  "parts-catalog",
  { query: "AX-100", limit: 25,
    filters: { manufacturer: "Northstar", category: "bearing", status: "active" } },
  { timeoutMs: 2_000 },
);
```

`filters` admits only `category`, `manufacturer`, and `status`. Values are
literal, case-sensitive strings of 1–512 Unicode scalar values. Supplied
fields are combined with AND before candidate limits, not filtered from a
truncated client result. Omission or `{}` adds no business-field restriction;
`undefined` optional fields are omitted, while `null` is invalid. Values are
not trimmed. Trusted tenant scope comes from the service, not this object;
caller `partitionId` remains a separate optional request field.

The generic client leaves status vocabulary to the adapter. Parts accepts
only `active` or `discontinued`; an invalid status reaches the service as a
typed `ApiError` with HTTP 400. Unknown well-formed manufacturer/category
values may return zero hits. The example's `searchParts` wrapper narrows
status at compile time and delegates to this client without retries or
post-filtering. Unknown object-literal names and nonstring values fail the
generated types too. TypeScript structural typing/excess-property checks
are not a security boundary: runtime validation still applies.

Original `catalog-identifier` callers that omit filters keep identical
request bytes. That adapter rejects any present filters object, even `{}`.

The client validates requests and responses against the generated protocol. It
does not retry or follow redirects. API failures are typed as `ApiError`;
malformed or inconsistent responses are `ProtocolError`; invalid caller input
is `ClientError`.

For the complete backend, service and example-index setup, use the
[local user manual](../../docs/user-manual.md).

## Multi-select parts

Prepare the `parts-catalog-select` recipe using the
[multi-select walkthrough](../../docs/user-manual.md#multi-select-parts-catalog-choose-several-values-per-field).
Use that recipe's `client-token-a` fixture handle, not the minimal catalog's
handle in the introductory snippet. A different client route does not
reconfigure a daemon.

```ts
import type { PartsCatalogSelectRequest } from "@kurn-dev/kurn-search-client-private";

const selection: PartsCatalogSelectRequest = {
  query: "AX-100", limit: 25,
  filters: { category: ["bearing", "filter"], status: "active" },
};
const result = await client.searchPartsCatalogSelect(selection, { timeoutMs: 2_000 });
```

This fixed-route method accepts strings or readonly arrays: OR within a field,
AND across fields and trusted scope. Category/manufacturer accept at most 16
supplied members; status accepts 2, each `active` or `discontinued`. Every member
is 1–512 Unicode scalars. Empty arrays, invalid status members and wrong shapes
raise `ClientError` before token lookup or transport. Arrays retain their input
order and duplicates on the wire; supplied counts apply before deduplication.
The server owns normalization and the 16,384-byte whole-filter limit including
scope, so a valid client request can still get HTTP 400 `invalid_request`.

Both parts routes now support literal `<`, `>`, `&`, U+2028 and U+2029 in
valid selection or scope values. Keep using `search` for scalar filters and
`searchPartsCatalogSelect` for arrays; these characters do not require a
route migration or hand-escaped values. The server checks the daemon's exact
escaped echo while preserving its canonical request. A genuinely changed or
incorrectly encoded echo still yields HTTP 502 `filter_identity_mismatch`,
without automatic retry. See the [manual](../../docs/user-manual.md#troubleshooting).

Legacy `SearchRequest` and `search` remain scalar-only, including when the
adapter argument is a dynamic string or `parts-catalog-select`. Old request
readers and serializers retain their behavior. Both methods share the same
response decoders, error types, timeout/cancellation and no-retry/no-redirect
policy. Optional semantic `verify` remains opt-in. TypeScript cannot guarantee
array length or validate every structural value; runtime checks still apply.

## Part-number similarity suggestions

Prepare the dedicated `parts-catalog-suggest` bundle and daemon using the
[suggestion example](../../examples/parts-catalog-suggest/README.md). Then use
the fixed-route method; it accepts the same bounded scalar-or-array filter
shape as the select method:

```ts
import type { PartsCatalogSuggestRequest } from "@kurn-dev/kurn-search-client-private";

const request: PartsCatalogSuggestRequest = {
  query: "GX-201", limit: 25, filters: { category: ["gasket", "filter"] },
};
const result = await client.searchPartsCatalogSuggest(request, { timeoutMs: 2_000 });
```

Each hit's `result` is still `unknown`; the example validates the checked
seven-field part record plus integer `score`. Similarity scores are Kurn's rounded
coverage of corpus-known character 2/3-grams at the legacy recipe's 0.65 threshold. It is
not probability, confidence, edit distance or a character-match percentage.
Unknown query grams do not enter the denominator, so an inexact identifier may
score 100. A corpus update can change scores and ordering.

The same client method also serves the
[prepared-version workflow](../../examples/parts-catalog-suggest-prepared/README.md).
New prepared versions default to `osa1-060`: 0.60 generation, edit-distance-one
acceptance, then exact transposition fallback if nothing is accepted. Policy
belongs to the service binding, not a client request option; it does not turn
the reported score into edit distance. Exact companion hits instead receive
100 from whole-key membership of a generated transposition variant and carry
`transposition: "yes"` evidence. Under the union policy, transposition evidence
wins for an entry found by both branches. Neither score is probability or
confidence. Use that version's endpoint and tenant handle together, not the
bundle example's fixed handles.

Admission overload is an `ApiError` with `value.status === 503` and category
`service_queue_full` (no waiting slot) or `service_admission_unavailable`
(admission wait failed). `backend_overloaded` is a separate backend refusal.
The client exposes the optional validated `retry_after_seconds` hint and does
not retry automatically. Any application retry must fit its own bounded
deadline and retry budget; do not retry authentication or identity failures.

## Multi-field parts discovery

Prepare the independent `parts-catalog-discover` exact bundle with the
[discovery walkthrough](../../docs/user-manual.md#multi-field-parts-catalog-discovery).
The fixed method accepts the same scalar-or-array fields as select/suggest,
but its query searches both the part identifier and the human-readable name:

```ts
import type { PartsCatalogDiscoverRequest } from "@kurn-dev/kurn-search-client-private";

const request: PartsCatalogDiscoverRequest = {
  query: "ball bearing",
  limit: 25,
  filters: { manufacturer: "Northstar", status: ["active", "discontinued"] },
};
const result = await client.searchPartsCatalogDiscover(request, { timeoutMs: 2_000 });
```

Use this fixed method for array-valued filters. Generic `search` remains the
legacy scalar contract even if its route string is `parts-catalog-discover`.
Client validation checks shape and closed status vocabulary; the service owns
term/probe bounds, completeness, trusted scope and ranking. Hits expose
`unknown` payloads, so application code should validate or narrow the checked
eight-field discovery result, as the
[separate consumer](../../examples/parts-catalog-discover/consumer/src/parts-discover.ts)
does. No score or match explanation is returned.

## Faceted parts discovery

`searchPartsCatalogDiscoverFacets` returns the unchanged discovery answer plus
`total_matches` and the closed three-group `facets` summary over the complete
matched set before the caller limit:

```ts
import type { PartsCatalogDiscoverFacetsRequest } from "@kurn-dev/kurn-search-client-private";

const request: PartsCatalogDiscoverFacetsRequest = {
  query: "gasket",
  limit: 25,
  filters: { status: "discontinued" },
};
const result = await client.searchPartsCatalogDiscoverFacets(request, { timeoutMs: 2_000 });
```

`result.hits` is the prefix of length `min(limit, result.total_matches)` of
the same ordered matched set the discovery route returns. Buckets order by
count descending, then by value in JavaScript UTF-16 code-unit order, and are
payload-exact; using one as a filter value reproduces its own count. The
strict decoder requires both members on success. A matched set at the
1,000-row ceiling refuses HTTP 422 `incomplete_at_pool_ceiling` with no
partial count. See the [facet consumer](../../examples/parts-catalog-discover-facets/consumer/src/parts-discover-facets.ts)
for narrowing of the `unknown` hit payloads. For interactive selection, use
`searchPartsCatalogDiscoverFacetAlternatives` with the same discovery request.
Its `facets` groups contain `basis_total` and `buckets`; each group omits only
its own filter, while `hits` and `total_matches` apply every filter. See the
[interactive example](../../examples/parts-catalog-discover-facet-alternatives/README.md).

## Contract identities

The generated headers preserve upstream API and fixture provenance. The current
manifest is a filtered summary, without private source-layout metadata; its
upstream receipt digest is not the summary's own hash. The public release inventory and executed tests are
separate; see [release verification](../../docs/release.md). Legacy client
compatibility uses digest-pinned fixtures included with the tests, without
requiring private Git history. A source install alone is not TypeScript compiler
verification.
