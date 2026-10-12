# kurn-searchd

`kurn-searchd` is the executable composition boundary for the private search
service. Each process registers exactly one configured adapter:
`catalog-identifier` for the original example, or `parts-catalog` for identifier
queries with exact manufacturer/category/status filters, or `parts-catalog-select`
for bounded multi-select membership within those fields, or
`parts-catalog-discover` for bounded identifier/name term discovery with the
same membership grammar, its separate full-match and interactive-alternative
facet routes, or
`parts-catalog-suggest` for fixed-threshold scored 2/3-gram suggestions with
the same membership grammar. It uses a strict Kurn
HTTP backend and accepts only local fixture authentication.

Run it with exactly one absolute private config path:

```sh
kurn-searchd -config /absolute/private/root/kurn-searchd.json
```

The config root must be mode `0700`; referenced files must be regular,
non-symlink private files under that root; and the runtime status path must be
absent. Startup checks the configured Kurn list, exact configuration digest and
readiness golden before publishing the status file.
The readiness golden must name the configured adapter; a golden for the other
recipe is refused. Run separate processes/configurations if you need multiple
examples at once; this is not a multi-adapter configuration format.

Use [the local preparation helper](../kurn-search-local/README.md) with
`-recipe parts-catalog` for the checked filtered-parts bundle, or omit `-recipe`
for the original catalog. The helper prepares files; it does not start daemons.
Use `-recipe parts-catalog-select` for multi-select over the same parts bundle.
It explicitly selects the additive membership executor; old recipes keep
their original executor constructor and filter grammar.
Use `-recipe parts-catalog-discover` with the independent exact discovery
bundle. It serves `parts-catalog-discover-v1` and cannot substitute the older
parts bundle because the source includes names and domain-separated keys.
The `parts-catalog-discover-facets` and
`parts-catalog-discover-facet-alternatives` recipes serve that same list with
distinct success contracts; the latter counts each group without its own
selection and may need a broader superset pool.
Use `-recipe parts-catalog-suggest` with its dedicated n-gram bundle; it cannot
serve the exact parts bundle under a different route name.

For a complete working config and the TypeScript request, follow the
[local user manual](../../docs/user-manual.md).

## Prepared versions

The [prepared-artifact walkthrough](../../examples/parts-catalog-suggest-prepared/README.md)
uses the same `-config` invocation with a separately generated
`kurn-search-prepared-local/v1` binding. Start the version's own bundled
executables: full artifact verification in `kurnd` is required for searchd
readiness. Trusted artifact startup is not a supported shortcut for this
composition. The status file is `state/search-status.json` inside the version.

Configuration, manifest and profile diagnostic bind the selected suggestion
policy, admission limits and artifact identity. New preparations default to
`osa1-060`; an absent policy member in an existing binding remains legacy.
Do not edit controls or swap binaries in place to change policy or upgrade a
version: prepare a fresh version and coordinate its endpoint and credentials.
Prepared controls are mode `0400`, not the ordinary workspace's `0600`.

`adapter.max_queue` bounds waiting admission slots, not all HTTP connections
or total process memory. Queue-full requests return `503 service_queue_full`;
a failed admission wait returns `503 service_admission_unavailable`. Both are
refusals, not partial search results. See the prepared guide for limits and
client handling.

The delivered composition is loopback/fixture-only. It does not provide
production authentication, TLS, tenant network isolation or a service-manager
integration.
