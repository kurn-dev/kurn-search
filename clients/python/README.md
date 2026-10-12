# Private Python client

The dependency-free `kurn_search_client` package provides
only transport, contract-shaped models, authentication hooks, bounded
timeouts, cooperative cancellation, stable API/protocol errors, and opt-in
semantic verification. It does not compile or rank searches and never
retries or follows redirects.

```python
from kurn_search_client import KurnSearchClient, SearchRequest

client = KurnSearchClient(endpoint, get_token)
result = client.search(
    "catalog-identifier",
    SearchRequest(query="AX-104", limit=25),
    timeout_seconds=5,
    cancellation=cancellation,
)
```

## Parts-catalog field filters

Use Python 3.14.5 with this checkout's `clients/python` directory on
`PYTHONPATH`; no package install is needed. The
[manual](../../docs/user-manual.md) and
[parts example](../../examples/parts-catalog/README.md) include the checked
source, bundle preparation, both daemons and a complete executable script.
The service must be configured for `parts-catalog`, not the original catalog.

```python
result = client.search(
    "parts-catalog",
    SearchRequest("AX-100", 25, filters={
        "manufacturer": "Northstar", "category": "bearing", "status": "active",
    }),
    timeout_seconds=2,
)
```

`filters` is the final defaulted dataclass field, so existing positional calls
remain valid. `None` omits it; `{}` sends an empty object. Neither adds business
restrictions. On the wire, JSON null is invalid. The only admitted names are
`category`, `manufacturer` and `status`, with literal case-sensitive strings
of 1–512 Unicode scalar values, no implicit trimming and AND combination.
Unknown names and wrong value shapes raise `ClientError` before authentication
or transport. The generic client leaves status vocabulary to the adapter;
parts rejects statuses other than `active`/`discontinued` with an HTTP 400
`ApiError`. Unknown well-formed manufacturer/category values may yield no hits.
Server-owned tenant scope is not a filter argument; `partition_id` remains
separate. The example's recipe-only `TypedDict`/`Literal` describes intended
typing, not a claim that a Python static checker ran.

The original catalog route still rejects any present filters object, even
`{}`; omitted-filter serialization remains byte-identical. Results and opt-in
semantic verification are unchanged. Filtering happens before Kurn candidate
limits, not in this client after truncation.

## Multi-select parts

Configure the service for `parts-catalog-select` using the
[multi-select walkthrough](../../docs/user-manual.md#multi-select-parts-catalog-choose-several-values-per-field).
Use the parts recipe's `client-token-a` fixture handle. Then:

```python
from kurn_search_client import PartsCatalogSelectRequest

result = client.search_parts_catalog_select(
    PartsCatalogSelectRequest("AX-100", 25, filters={
        "category": ["bearing", "filter"], "status": "active",
    }),
    timeout_seconds=2,
)
```

Only strings or actual lists/tuples are accepted, not sets, generators or
arbitrary iterables. OR applies within one field; fields and trusted scope
combine with AND. Category/manufacturer allow up to 16 supplied members each;
status allows 2, each `active` or `discontinued`. Members are literal,
case-sensitive, untrimmed strings of 1–512 Unicode scalars. Empty arrays and
invalid members refuse with `ClientError` before token lookup/transport.
Order and duplicates are preserved on the wire; counts apply before
deduplication. The server owns the combined 16,384-byte canonical-filter
limit including trusted scope and may still refuse otherwise valid fields.

Both parts routes now support literal `<`, `>`, `&`, U+2028 and U+2029 in
valid selection or scope values. Keep using `search` for scalar filters and
`search_parts_catalog_select` for arrays; these characters do not require a
route migration or hand-escaped values. The server checks the daemon's exact
escaped echo while preserving its canonical request. A genuinely changed or
incorrectly encoded echo still yields HTTP 502 `filter_identity_mismatch`,
without automatic retry. See the [manual](../../docs/user-manual.md#troubleshooting).

`PartsCatalogSelectRequest` is a separate frozen dataclass with the same field
order/defaults, not a widened `SearchRequest`. The old `search` method remains
scalar-only, even with the new route string. The new method uses identical
transport, timeout/cancellation, error and optional verification behavior.
No Python static-checker claim is implied by these annotations.

## Part-number similarity suggestions

Prepare the dedicated `parts-catalog-suggest` bundle and daemon using the
[suggestion example](../../examples/parts-catalog-suggest/README.md). The
fixed-route request retains the bounded scalar-or-list filter grammar:

```python
from kurn_search_client import PartsCatalogSuggestRequest

result = client.search_parts_catalog_suggest(
    PartsCatalogSuggestRequest("GX-201", 25, filters={"category": ["gasket", "filter"]}),
    timeout_seconds=2,
)
```

The checked example validates each result as the seven part fields plus an
integer `score`. Similarity hits report Kurn's rounded coverage of corpus-known character
2/3-grams at the legacy recipe's threshold 0.65, not probability, confidence, edit distance or a
character-match percentage. Unknown query grams are excluded from the
denominator, so an inexact identifier may score 100. Scores and order can
change when the corpus changes.

The same method also serves the
[prepared-version workflow](../../examples/parts-catalog-suggest-prepared/README.md).
New prepared versions default to `osa1-060`: 0.60 generation, edit-distance-one
acceptance, then exact transposition fallback if nothing is accepted. Policy
belongs to the service binding, not a client request option. Exact companion
hits receive 100 from whole-key membership of a generated transposition
variant and carry `transposition: "yes"` evidence. Under the union policy,
transposition evidence wins for an entry found by both branches. Neither score
is probability, confidence or edit distance. Use the version's endpoint and
tenant handle together.

Admission overload raises `ApiError` with `value.status == 503` and category
`service_queue_full` (no waiting slot) or `service_admission_unavailable`
(admission wait failed). `backend_overloaded` is a separate backend refusal.
The error value exposes the optional validated `retry_after_seconds` hint.
There is no automatic retry: an application retry needs its own bounded
deadline and retry budget, and must not retry authentication or identity failures.

## Multi-field parts discovery

Prepare the independent `parts-catalog-discover` exact bundle using the
[discovery walkthrough](../../docs/user-manual.md#multi-field-parts-catalog-discovery).
The fixed request accepts the same bounded scalar-or-list fields as the other
membership routes while the query searches both identifier and name:

```python
from kurn_search_client import PartsCatalogDiscoverRequest

result = client.search_parts_catalog_discover(
    PartsCatalogDiscoverRequest(
        "ball bearing", 25,
        filters={"manufacturer": "Northstar", "status": ["active", "discontinued"]},
    ),
    timeout_seconds=2,
)
```

Use this fixed method for list-valued filters; generic `search` remains the
legacy scalar contract regardless of route spelling. The client checks the
closed field and status shapes. The service owns query term/probe bounds,
complete-or-refuse execution, trusted tenant scope and ranking. Result payloads
are ordinary dictionaries and should be validated by application code; the
[checked Python journey](../../examples/parts-catalog-discover/search.py)
shows the eight-field discovery shape. No score or match explanation is
returned.

## Faceted parts discovery

`search_parts_catalog_discover_facets` returns the unchanged discovery answer
plus `total_matches` and the closed three-group `facets` summary over the
complete matched set before the caller limit:

```python
from kurn_search_client import PartsCatalogDiscoverFacetsRequest

result = client.search_parts_catalog_discover_facets(
    PartsCatalogDiscoverFacetsRequest(
        "gasket", 25, filters={"status": "discontinued"}
    ),
    timeout_seconds=2,
)
```

`result["hits"]` is the prefix of length `min(limit, result["total_matches"])`
of the same ordered matched set the discovery route returns. Buckets order by
count descending, then by value in JavaScript UTF-16 code-unit order, and are
payload-exact; using one as a filter value reproduces its own count. The
strict decoder requires both members on success. A matched set at the
1,000-row ceiling refuses HTTP 422 `incomplete_at_pool_ceiling` with no
partial count. See the
[checked Python facet journey](../../examples/parts-catalog-discover-facets/search.py)
for result validation. For interactive alternatives, call
`search_parts_catalog_discover_facet_alternatives` with
`PartsCatalogDiscoverFacetAlternativesRequest` and the same discovery fields.
Each `facets` group contains `basis_total` and `buckets` counted without that
group's own selection; `hits` and `total_matches` apply all selections. See
the [interactive example](../../examples/parts-catalog-discover-facet-alternatives/README.md).

## Runtime and generation

The checked runtime target is exactly Python 3.14.5. The shipped
standard-library transport uses `http.client`; cancellation during resolver
or connect work is cooperatively bounded by the active socket timeout. This
MIT-licensed source package has no build backend and is not published to PyPI. Its generated bytes
come from the same Go-owned template set as the TypeScript client, bound to
the current OpenAPI document/fixtures and the original upstream receipts.
[`../current-manifest.json`](../current-manifest.json) is a filtered provenance
summary of those bytes; private source-layout metadata is omitted. Its upstream
receipt digest is distinct from the summary file's own hash. See
[release verification](../../docs/release.md).
Historical generation-format/base labels do not identify the public source
snapshot.
