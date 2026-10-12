# catalogid

`catalogid` is the pure paired adapter behind the public service route
`catalog-identifier`. It handles short catalog identifiers using a
neutral source contract (`id`, `identifier`, `tenant_id`, optional
`partition_id`) and composes exact, prefix, suffix, and word evidence over one
exact Kurn list.

The package owns strict parsing, physical-key projection, query compilation,
single-rung completeness checks, evidence union, deterministic ranking, and
result projection. A successful hit always includes the full neutral payload
and all four evidence arrays, including empty arrays.

The package also compiles an immutable exact-filter plan: required
`tenant_id` comes only from trusted-scope bytes and optional `partition_id`
comes only from request bytes. Its typed incomplete result is the sole pool-
escalation signal consumed by the neutral orchestrator.

The package deliberately does not perform transport, authentication,
admission, retries, pool-ladder orchestration, filter echo validation,
version handling, service routing, or logging. The `orchestrator` owns the
bounded execution mechanics; an eventual authenticated boundary must still
supply the trusted scope.

Its behavioral identity is constructed from deterministic neutral fixture
outputs. Operational list name, resolved configuration digest, tested engine
versions, timeout, concurrency, and charge evidence are explicit descriptor
inputs and do not change the behavior digest.

This is one reviewed Kurn-native adapter, not Elasticsearch compatibility,
Lucene equivalence, arbitrary text search, or a production-sizing claim.

To build the checked example list and search it through `kurn-searchd` with the
generated TypeScript client, follow the
[local user manual](../../docs/user-manual.md).
