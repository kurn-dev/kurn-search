# Multi-select parts catalog

This recipe searches the same fictional part identifiers as
[parts-catalog](../partscatalog/README.md), with several exact selections per
manufacturer or category. Values within one field are ORed; different fields,
server-owned tenant, and optional partition are ANDed.

The request below means identifier `AX-100`, category bearing or filter, and
active status:

```json
{"query":"AX-100","filters":{"category":["bearing","filter"],"status":"active"},"limit":25}
```

Use the `parts-catalog-select` route, version `v1`. The existing
`parts-catalog` and `catalog-identifier` routes retain their old contracts.
An empty object or omitted filters imposes no business-field restriction.
An empty array refuses; it never means unrestricted.

Selections are literal, case-sensitive, and untrimmed. Category/manufacturer
accept up to 16 supplied strings; status accepts up to two, each exactly
`active` or `discontinued`. Every string is 1–512 Unicode scalars. Duplicate
members are counted before deduplication. Canonical scoped filters have a
16,384-byte ceiling, including tenant and optional scalar partition.

Filtering happens in Kurn before each candidate cut. Complete results use the
existing identifier ranking, then the requested limit. Saturation escalates
the fixed 128/256/512/1000 pool ladder or refuses at its ceiling; the adapter
never returns a silently partial union of separately filtered searches.

Source projection delegates to the delivered parts recipe. The embedded
fixture pins that recipe's unchanged 152-record source and supplies twenty
independently frozen multi-select answers. This is a fictional local recipe,
not filter-only browsing, general boolean search, or production qualification.

The [user manual](../../docs/user-manual.md) and
[client documentation](../../clients/typescript/README.md) are the setup entry
points. The complete multi-select setup/client journey is still under
implementation and is not yet a delivered acceptance claim.
