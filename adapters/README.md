# Search recipes

Each adapter pairs source projection with query compilation and result
resolution. Pick a recipe before preparing a local daemon; it loads one
configured adapter, not an arbitrary collection of mappings.

- [Catalog identifier](catalogid/README.md): the original neutral identifier
  contract. Start with the [one-record quick path](../docs/user-manual.md#quick-path-prepare-the-example-then-use-a-typescript-application).
- [Parts catalog](partscatalog/README.md): short identifiers plus exact
  manufacturer/category/status filters. Start with the [filtered recipe](../docs/user-manual.md#parts-catalog-search-with-field-filters)
  and its [readable examples](../examples/parts-catalog/README.md).

- [Multi-select parts](partscatalogselect/README.md): the same parts data with
  several exact values per named field. Use the
  [walkthrough](../docs/user-manual.md#multi-select-parts-catalog-choose-several-values-per-field)
  and [TypeScript/Python examples](../examples/parts-catalog-select/README.md).
- [Parts discovery](partscatalogdiscover/README.md): one bounded search box
  over identifiers and human-readable names, combined with the same
  multi-select fields. Use the
  [walkthrough](../docs/user-manual.md#multi-field-parts-catalog-discovery)
  and [checked clients](../examples/parts-catalog-discover/README.md).
- [Faceted parts discovery](partscatalogfacets/README.md): the unchanged
  discovery answer plus exact manufacturer/category/status counts over the
  complete matched set before limiting. Use the
  [walkthrough](../docs/user-manual.md#faceted-parts-catalog-discovery)
  and [checked clients](../examples/parts-catalog-discover-facets/README.md).
- [Interactive facet alternatives](partscatalogalternatives/README.md): the
  same filtered hits, but each category/manufacturer/status group counts
  matches to the other two filters while excluding its own selection. Use the
  [walkthrough](../docs/user-manual.md#interactive-facet-alternatives)
  and [checked example](../examples/parts-catalog-discover-facet-alternatives/README.md).
- [Part-number suggestions](partscatalogsuggest/README.md): one raw identifier
  key in a dedicated n-gram list, fixed coverage threshold and scored results.
  Use the [local examples](../examples/parts-catalog-suggest/README.md).

All use bounded execution policy. The exact recipes retain the unchanged
analyzer; the suggestion recipe binds lowercase-plus-trim 2/3-gram analysis.
Every route has separate behavioral and descriptor identities. Discovery
adds domain-separated identifier/name keys rather than widening an older
route. Metadata filters combine with server-owned tenant and optional caller
partition before candidate limits. These are checked fictional local examples,
not general importers, full-text engines or customer qualification.

Customer-specific mappings require separately reviewed scope. The pure
[adapter contract](../adapter/README.md) explains the extension boundary.
Each membership-capable adapter has its own descriptor/compiler/behavior
identity; older scalar contracts are not widened.
