# Parts-catalog faceted discovery adapter

`partscatalogfacets` implements the closed `parts-catalog-discover-facets` v1
route: the delivered multi-field parts discovery answer, unchanged, plus exact
manufacturer/category/status counts over the same complete matched set.

The adapter adds no search behavior of its own. Plan compilation, membership
filter compilation, incompleteness classification and resolution delegate to
the byte-unchanged `partscatalogdiscover` adapter, and the physical list
`parts-catalog-discover-v1` is reused byte-for-byte. The only added seam is
the summary resolver:

- the caller's `limit` is substituted by the hits ceiling (1,000) through the
  delegate's own strict request grammar;
- the delivered discover resolution then yields the complete, validated,
  ordered matched set — every probe complete below its pool, every candidate
  revalidated against trusted tenant, caller partition and literal filters;
- `total_matches` and the three groups are tallied over that set, and only
  then is the caller's limit applied to the ordered hit prefix. Hits are the
  prefix of length `min(limit, total_matches)`.

There is no second backend request, no fabricated hit, no approximate,
sampled, self-excluding or inferred count. If the delegate returns exactly
1,000 hits, the exact total cannot be proven within the ceiling, and the
adapter returns the typed summary-at-ceiling signal; the facet executor maps
it immediately to HTTP 422 `incomplete_at_pool_ceiling` with no extra backend
round and no partial answer.

Facet values are payload-exact canonical source strings (never normalized),
so a bucket used as a filter value reproduces its own count. Buckets order by
count descending, then by value in JavaScript UTF-16 code-unit order. Only
values present in the matched set appear. The route exposes no caller-chosen
field, threshold, analyzer or weight.

The checked fictional source and fixture are shared with the
`parts-catalog-discover` adapter; this package's own oracle fixture freezes 25
requests with their exact ordered answers and summaries, executed through the
delegating resolution in its artifact generation and through the live service
in the W7-S1 acceptance harness.

This is a fictional loopback development recipe: not a production service,
not a general full-text, relevance, aggregation or customer-facing contract.
