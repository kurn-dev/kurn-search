# adapter

Pure paired-adapter contracts, normalization/key codecs,
bounded query plans, evidence merging, ranking, completeness decisions, and
behavioral identity fixtures. No transport, clock, environment, or mutable
global state belongs here.

`adapter/analyzer` contains the frozen local analyzer. Its five views are
fixture-backed under Go 1.27.0 / Unicode
17.0.0. It is not a Lucene analyzer or a general Elasticsearch compatibility
claim.

The common immutable values and synchronous pure interfaces are:

- `ProjectedEntry` binds a stable ID, ordered physical keys, and one opaque
  non-null JSON payload;
- `Probe` and `CompiledPlan` carry only adapter-declared evidence kind,
  logical term, physical key, and list role;
- `ProbeEvidence` carries the exact probe, one attempt pool rung, and ordered
  candidate tuples; and
- `Resolution` can represent only a complete ordered result.

An independent immutable exact-filter plan keeps trusted and caller-
controlled terms remain provenance-separated until an orchestrator validates
them against a closed descriptor. `FilterCompiler` and
`IncompleteClassifier` are pure seams; they add no transport or retry policy.

The additive `MembershipFilterTerm`, `MembershipFilterPlan` and
`MembershipFilterCompiler` support bounded exact-string alternatives without
widening those old values/interfaces. Trusted scalar and caller membership
terms remain separately typed, validated and copied. The complete canonical
expression is capped at 16,384 bytes. The
[multi-select adapter](../adapters/partscatalogselect/README.md) owns the narrower
field/status vocabulary. All supplied members are checked before sorting and
deduplication; constructor/accessor copies preserve validated plan identity.

Attempt filters, thresholds, transport, versions, admission, and retries do
not enter these values. A concrete adapter owns evidence interpretation,
saturation, payload consistency, union, ordering, ranking, and projection.

Behavioral identities use SHA-256 over exact unsigned-64-bit big-endian
length frames. The four closed domains separately bind component, codec,
search, and overall behavior bytes. Nested codec/search digests enter the
overall frame as raw 32-byte values, not hexadecimal text. These mechanisms
prove identity construction; they do not make arbitrary JSON canonical or
make distinct adapters semantically compatible.

The [catalog identifier](../adapters/catalogid/README.md) and
[parts catalog](../adapters/partscatalog/README.md) are separate concrete
consumers. Their semantics remain pure. The bounded orchestrator executes
plans; the service owns authentication, routing, admission and readiness.
Use the [user manual](../docs/user-manual.md) to run either local example.
