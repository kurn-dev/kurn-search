# Parts-catalog-select example: identifier search with multi-select filters

This example extends the [parts-catalog example](../parts-catalog/README.md):
the same 152 fictional records, the same identifier matching, and field
filters that now accept **several values per field**. A request such as
"`AX-100` in category `bearing` **or** `filter`, **and** status `active`" is
one search, answered completely or refused. Everything runs on your machine,
on loopback addresses, with fixture authentication; nothing here is a
deployment.

Two rules describe every filter:

- **OR within a field.** A field given as an array matches a record whose
  value equals any listed member. `["bearing", "filter"]` matches bearings and
  filters. A field given as a single string is the one-member case.
- **AND across fields.** Distinct fields, the server-owned tenant and the
  optional `partitionId` all constrain the same record. There is no OR across
  fields, no NOT, no ranges and no wildcards.

Values are literal, case-sensitive and untrimmed: `northstar` and `Northstar`
are different members. Member order and duplicates never change the answer:
`["Halden", "Northstar"]`, `["Northstar", "Halden"]` and
`["Northstar", "Northstar", "Halden"]` are the same filter. A one-member array
is the same filter as the scalar string. An empty array is refused; it never
means "no restriction" (omit the field for that). Manufacturer and category
accept at most 16 members each and any well-formed string, which may match
nothing; status accepts at most 2 members, each `active` or `discontinued`.

Filtering runs inside the search engine before any result limit, so a filtered
answer is either complete or the service says it is not.

The step-by-step recipe, including building the pinned Kurn binaries,
preparing the local configuration with `kurn-search-local prepare -recipe
parts-catalog-select`, starting both daemons and running the programs below,
is the multi-select section of [`docs/user-manual.md`](../../docs/user-manual.md).

## Files

| File | Purpose |
|---|---|
| (data) | This example adds no data. It reuses `../parts-catalog/source.ndjson`, `../parts-catalog/list-feed.ndjson` and `../parts-catalog/list-mapping.json` by reference; the bundle you build for the parts recipe is the bundle this recipe serves. |
| `search.py` | Python example: runs the documented request set through the fixed-route client method and checks every answer. |
| `consumer/` | A separate TypeScript project that installs the private client package locally, compiles with the pinned compiler, and runs the same request set through the fixed-route client method. `src/parts-select.ts` is a small typed wrapper for the recipe; `negative/wrong-request.ts` must fail to compile. |

## What the programs check

Both programs send the query `AX-100` for tenant-a with limit 25 and compare
the complete answer, identifiers in order and full payloads, with the checked
expectation. Brackets are arrays.

| Request | Expected identifiers, in order |
|---|---|
| no filters | PC-001, PC-002, PC-003, PC-004, PC-010, PC-011, PC-006, PC-007, PC-008, PC-012 |
| category `[bearing, filter]`, status `active` | PC-001, PC-002, PC-004, PC-010, PC-011, PC-006, PC-008 |
| manufacturer `[Northstar, Halden]`, category `bearing` | PC-001, PC-002, PC-003, PC-010, PC-006 |
| manufacturer `[Halden, Northstar]`, category `bearing` (permuted) | the same five |
| manufacturer `[Northstar, Vega]` | PC-001, PC-003, PC-004, PC-010, PC-006, PC-007, PC-012 |
| category `[bearing, Bearing]` (case-distinct members) | PC-001, PC-002, PC-003, PC-010, PC-011, PC-006, PC-012 |
| manufacturer `[Northstar, Northstar]` (duplicate = scalar) | PC-001, PC-003, PC-004, PC-010, PC-006, PC-012 |
| status `[active, discontinued]` (full set, still a filter) | the ten unfiltered identifiers |
| manufacturer `[Unknown, Other]` | none, with `complete: true` |
| category `[]` | refused by the client before any request is sent |
| status `[active, retired]` | refused by the client before any request is sent; the manual's raw `curl` step shows the service's own HTTP 400 `invalid_request` for a bad status |

Notice that `PC-012` (category `Bearing`) appears only when `Bearing` is a
member, and that `PC-011` (manufacturer `northstar`) never appears for the
member `Northstar`: members are exact.

Each program prints one line per request, `ok` or `MISMATCH`, followed by the
identifiers returned, and exits 0 only when every request matched. Exit 2
means the environment or a request failed; exit 1 means an answer differed.

Environment for both: `KURN_SEARCHD_ADDR` must be the documented loopback
address of the running service, configured for `parts-catalog-select`. The
Python program also needs `KURN_SEARCH_REPO` (the checkout) to import the
checked client package without installation. The bearer value
`client-token-a` is a fixture handle accepted only by the local fixture
authenticator; it is not a credential.

## Using the clients directly

TypeScript, through the fixed-route method (arrays are not accepted by the
generic `search` method, whose request type is unchanged):

```ts
const parts = await client.searchPartsCatalogSelect(
  { query: "AX-100", limit: 25, filters: { category: ["bearing", "filter"], status: "active" } },
  { timeoutMs: 2_000 },
);
```

Python:

```python
result = client.search_parts_catalog_select(
    PartsCatalogSelectRequest("AX-100", 25, filters={"category": ["bearing", "filter"], "status": "active"}),
    timeout_seconds=2,
)
```

The original `parts-catalog` route keeps refusing arrays, and the original
`catalog-identifier` route keeps refusing any `filters` member.

## Limits

Local developer ergonomics only. Set membership within a field and AND across
fields; no OR across fields, no NOT, no full-text search, no ranges or
wildcards, no sorting options, no pagination, no browser UI, no hosted
service, and no production security, availability or capacity claim.
