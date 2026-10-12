# Interactive parts facets

This example adds a selected-value-friendly facet view to the existing parts
search box. The new `parts-catalog-discover-facet-alternatives` route shares
the checked 152-row `parts-catalog-discover-v1` list, source and mapping with
[parts discovery](../parts-catalog-discover/README.md). It builds no second
index. The [manual](../../docs/user-manual.md#interactive-facet-alternatives)
gives the complete local build, prepare, start, call and stop sequence.

Prepare a separate local run root with
`kurn-search-local prepare -recipe parts-catalog-discover-facet-alternatives`
after building that shared list. One daemon configuration selects one route.
The generated TypeScript method is `searchPartsCatalogDiscoverFacetAlternatives`;
the Python method is `search_parts_catalog_discover_facet_alternatives`.

After the service is ready, set `KURN_SEARCHD_ADDR` to its loopback listener.
To run the checked TypeScript consumer outside the checkout, use a fresh
directory and the pinned offline TypeScript compiler cache described in the
[manual's compiler setup](../../docs/user-manual.md#faceted-parts-catalog-discovery):

```sh
mkdir -p "$KURN_SEARCH_RUN_ROOT/consumer/src" "$KURN_SEARCH_RUN_ROOT/consumer/negative"
cp "$KURN_SEARCH_REPO/examples/parts-catalog-discover-facet-alternatives/consumer/package.json" \
   "$KURN_SEARCH_REPO/examples/parts-catalog-discover-facet-alternatives/consumer/tsconfig.json" \
   "$KURN_SEARCH_REPO/examples/parts-catalog-discover-facet-alternatives/consumer/tsconfig.negative.json" \
   "$KURN_SEARCH_RUN_ROOT/consumer/"
cp -R "$KURN_SEARCH_REPO/examples/parts-catalog-discover-facet-alternatives/consumer/src/." "$KURN_SEARCH_RUN_ROOT/consumer/src/"
cp -R "$KURN_SEARCH_REPO/examples/parts-catalog-discover-facet-alternatives/consumer/negative/." "$KURN_SEARCH_RUN_ROOT/consumer/negative/"
(cd "$KURN_SEARCH_RUN_ROOT/consumer" && npm install --offline --ignore-scripts --no-audit --no-fund --package-lock=false "$KURN_SEARCH_REPO/clients/typescript")
node "$KURN_SEARCH_RUN_ROOT/compiler/node_modules/typescript/bin/tsc" -p "$KURN_SEARCH_RUN_ROOT/consumer/tsconfig.json" --pretty false
node "$KURN_SEARCH_RUN_ROOT/consumer/dist/search.js"
```

The negative TypeScript configuration must fail compilation with the wrong
filter, status, timeout and bucket types. The Python journey uses the private
client from this checkout without an install:

```sh
KURN_SEARCH_REPO="$KURN_SEARCH_REPO" KURN_SEARCHD_ADDR="$KURN_SEARCHD_ADDR" \
  python3 -B "$KURN_SEARCH_REPO/examples/parts-catalog-discover-facet-alternatives/search.py"
```

For query `bearing` with `manufacturer: "Northstar"`, the checked fictional
data yields five hits and `total_matches: 5`. The manufacturer group has
`basis_total: 7`: Northstar 5, Halden 1, northstar 1. Other groups retain
the Northstar selection. Selecting a manufacturer absent from that superset
can therefore give zero hits while the manufacturer group still offers the
available alternatives.

Counts are exact over the complete local superset, before the caller limit.
A superset of 1,000 hits refuses HTTP 422 `incomplete_at_pool_ceiling` instead
of returning partial counts. This is a fictional local example, not a
production-readiness or general aggregation claim.
