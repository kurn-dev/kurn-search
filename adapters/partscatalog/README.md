# Parts catalog adapter

This adapter combines short part-identifier matching with exact metadata
filters. It is a separate recipe named `parts-catalog`, version `v1`; it does
not change the original catalog-identifier adapter.

For several selections per field, use the separate
[multi-select adapter](../partscatalogselect/README.md). This adapter and its
scalar-only contract remain unchanged.

Start with the [field-filter walkthrough](../../docs/user-manual.md#parts-catalog-search-with-field-filters)
to build the checked index and query it from a separate TypeScript project or
Python. The [example guide](../../examples/parts-catalog/README.md) shows the
readable source, feed, mapping and expected answers.

Identifiers support exact, prefix, suffix and word evidence using the existing
Kurn Search analyzer. Metadata is not full-text indexed. Optional `category`,
`manufacturer` and `status` filters are case-sensitive string equality terms,
combined with AND before candidate limits. Status is `active` or
`discontinued`. Omission and an empty filter object impose no business-field
restriction. Tenant ownership always comes from trusted server context;
optional caller partition remains separate.

## Pure adapter interface

`New()` returns stateless semantics implementing `Project`, `Compile`,
`CompileFilter`, `Resolve` and `IsIncomplete`. `NewDescriptor` adds the
explicit physical list, configuration identity, tested engine and resource
policy; it does not change semantic identity when only operational binding
changes.

Source records require `id`, `identifier`, `manufacturer`, `category`,
`status` and `tenant_id`. `partition_id` is optional. IDs are at most 512
UTF-8 bytes; other source strings are at most 512 Unicode scalar values.
Source strings must be nonempty and have no leading/trailing whitespace as
recognized by Go's `strings.TrimSpace`: the public ingest path would otherwise
alter them. Query filter strings remain literal, including spaces. Unknown
manufacturer/category values simply match nothing.

The adapter rejects malformed JSON, duplicate/unknown members, null or
non-string metadata, unpaired surrogate escapes, scope overrides, invalid
status, and configured size/probe/key overflows. It never silently drops
malformed records or payloads that disagree with tenant, partition or business
predicates.

Resolution needs a complete producing rung before ranking or applying the
requested result limit. Saturated evidence requests the unchanged bounded pool
ladder through the orchestrator; it does not authorize unbounded retries or
post-filtering truncated candidates.

## Checked fictional input

[The neutral fixture](testdata/neutral-fixture.json) contains the twelve core
records and 140-record below-cut block, plus 24 named requests.
`NeutralFixture()` returns a defensive copy. `GenerateArtifacts()` evaluates
that fixture to produce separately identified projection, query-codec,
compiled-plan and resolved-result components.

The pure fixture evidence is synthetic. A unit-test pass is not a live engine,
client setup or production qualification. The runnable examples, local
preparation and user-manual journey are integrated and independently verified
as part of the same recipe before delivery.

No ranges, multi-select/IN, OR/NOT, facets, custom sorting, pagination,
filter-only browsing or general text search are implemented by this adapter.
