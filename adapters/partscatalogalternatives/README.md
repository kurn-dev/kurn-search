# Interactive facet alternatives

`parts-catalog-discover-facet-alternatives` is a separate local recipe over the
same checked `parts-catalog-discover-v1` physical list as parts discovery.
Its request grammar, search terms, trusted tenant and optional partition are
unchanged. Hits and `total_matches` apply every selected field. Each facet
group's `basis_total` and buckets apply the other two selections but omit
the group's own selection. This makes alternatives visible when a user has
already selected a value in that group. It is not a general aggregation API.

The route executes one complete unfiltered facet superset through the bounded
128/256/512/1000 candidate ladder. At 1,000 superset hits it refuses HTTP
422 `incomplete_at_pool_ceiling`; it never returns a partial tally. There is
no second query, retry, redirect or backend-side aggregation. See the
[user manual](../../docs/user-manual.md#interactive-facet-alternatives) for
setup and [example](../../examples/parts-catalog-discover-facet-alternatives/README.md)
for TypeScript and Python calls.
