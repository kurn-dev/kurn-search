# Search architecture

Kurn Search supplies example middleware over an unmodified public Kurn process.
Applications call the TypeScript or Python client; the service chooses a
reviewed adapter, supplies trusted tenant context, executes its bounded plan
against Kurn, and returns complete ordered results or a closed refusal.

Start with the [user manual](user-manual.md). The [minimal example](../examples/typescript-consumer/README.md)
teaches the setup; the [parts recipe](../examples/parts-catalog/README.md)
adds named filters; [multi-select parts](../examples/parts-catalog-select/README.md)
adds selections within those fields;
[parts discovery](../examples/parts-catalog-discover/README.md) adds one search
box over identifiers and names;
[faceted parts discovery](../examples/parts-catalog-discover-facets/README.md)
adds exact manufacturer/category/status counts over the complete matched set
to that same answer; [interactive alternatives](../examples/parts-catalog-discover-facet-alternatives/README.md)
retain all-filter hits while excluding each group's own selection from its
basis; and
[part-number suggestions](../examples/parts-catalog-suggest/README.md) adds a
dedicated scored n-gram list. All use fictional local data.

## Responsibilities

| Component | Owns | Does not own |
|---|---|---|
| Adapter | Source projection, identifier keys, query/filter compilation, evidence validation, deterministic rank | HTTP, authentication, clocks, retries |
| Profile/descriptor | Closed list vocabulary, semantic identities, operational binding and bounds | Customer schema inference or production policy discovery |
| Backend client | Independent public Kurn wire types, canonical filters, stamps, echo/stats checks | Engine implementation |
| Orchestrator | Complete batch per pool rung, bounded identity restart, producing-rung evidence | Service auth or merging incomplete attempts |
| Service | Authentication, trusted tenant, route/version checks, admission, deadline, readiness, closed errors | A general query language |
| Local helper | Exact checked-bundle verification and private configuration staging | Arbitrary ingestion, process supervision or deployment |
| Generated clients | Request validation/serialization, response decoding, opt-in semantic checks | Search semantics, automatic retries or production identity |

## Seven recipes, one configured adapter per process

The original `catalog-identifier` recipe and the new `parts-catalog`
recipe share the frozen identifier analyzer and exact/starts/ends/word
evidence approach. Parts uses separate source/request/result schemas,
behavioral artifacts and descriptor identities. It does not change the
original catalog's input grammar or semantics.

Parts source records include identifier, manufacturer, category, status and
tenant, with optional partition. Source strings reject edge whitespace
because public ingestion would otherwise trim them; filter values remain
literal. The checked projection produces physical identifier keys and retains
the complete source payload. Metadata is not indexed as free text.

Parts accepts only optional category/manufacturer/status string equality
terms, combined with AND. Status is active or discontinued. Omission or an
empty object adds no business restriction; unknown well-formed category or
manufacturer values can match nothing. The original catalog refuses any
present filters member, even an empty object. Each local daemon is configured
for one adapter, with a matching physical list and readiness golden.

`parts-catalog-select` reuses the exact parts projection/data but has its own
request codec, descriptor and behavioral artifacts. Fields accept scalars or
nonempty arrays: OR within a field, AND across fields and trusted scope.
Supplied counts are checked before deduplication (16 manufacturer / 16 category /
2 status); names/members are byte-sorted and singletons collapse to scalars.
The complete scoped canonical expression must fit 16,384 bytes. Old adapter
files, scalar constructors and service production bytes stay unchanged.
`NewWithMembership` explicitly selects the additive executor strategy; it
never probes optional interfaces or retries without the selected filter.

`parts-catalog-suggest` projects one raw identifier key per source record to a
dedicated Kurn list using lowercase-plus-trim character 2/3-grams and an exact
companion. The ordinary bundle recipe uses legacy 0.65 generation and takes
the exact transposition step only on an empty similarity pool. New prepared
versions default to `osa1-060`: 0.60 generation, optimal-string-alignment
distance-one acceptance, then the transposition step when nothing is accepted.
The 0.45 experiment and always-combined union policy are separate options;
see the [adapter policy reference](../adapters/partscatalogsuggest/README.md).

Similarity hits report rounded corpus-known coverage; exact companion hits
receive 100 for whole-key membership of a generated transposition variant.
Transposition hits carry `transposition: "yes"` evidence, which wins under
union when both branches find an entry. Results order by score, normalized
exact match, identifier and entry ID. Unknown query grams are excluded from
coverage, so an inexact value may score 100. Neither score is probability,
confidence or edit distance. Filtering executes inside Kurn before the
candidate cut. Corpus changes can change similarity scores and result order.

`parts-catalog-discover` projects domain-separated exact identifier,
identifier prefix/word and name prefix/word keys into a dedicated exact list.
Its checked source adds one required, trimmed, 1–80-scalar human-readable
name. A query has 1–8 deduplicated whitespace terms; 3–20-scalar terms may use
prefixes, shorter or longer terms use whole words only, and a term with no
usable key refuses. An exact identifier wins. Otherwise every term must be
satisfied by one projection of the same row; different terms may choose
different projections. Whole-word rows rank before prefix-needed rows, then
identifier, name and ID use JavaScript UTF-16 ordering. Filters and trusted
scope are attached to every probe before Kurn's cut. The 64-probe and
1000-candidate ceilings refuse rather than truncate or silently broaden the
query.

`parts-catalog-discover-facets` reuses that exact list and the delegated
discovery resolution unchanged. Its summary resolver substitutes the caller
limit with the hits ceiling, reads the complete validated ordered matched set
from the delivered resolution, and only then applies the caller limit to the
hit prefix. `total_matches` and exactly three payload-exact groups
(`category`, `manufacturer`, `status`) are tallied over that complete,
currently filtered set; buckets order by count descending, then value in
JavaScript UTF-16 order, and each group's counts sum to the total. A matched
set of exactly 1,000 rows cannot prove its total within the ceiling and
refuses immediately with the existing HTTP 422 `incomplete_at_pool_ceiling`,
with no extra backend round and no partial count. The orchestrator's facet
strategy is selected explicitly at construction by `NewWithFacets`, never by
runtime interface probing; the service asserts summary presence against the
constructed binding in both directions before encoding.

`parts-catalog-discover-facet-alternatives` also reuses the exact discovery
list but executes one complete tenant/partition-scoped superset with only the
caller facet terms removed from the backend filter. It validates and orders
that full superset, then applies all selections to hits and `total_matches`.
For each group, it applies the other two selections to `basis_total` and its
buckets. The distinct immutable alternative summary supports different group
bases without weakening the full-match facet invariant. At 1,000 superset
hits it refuses rather than reporting partial alternatives; its pool rung can
differ from the full-match route even when both routes' hits agree. Readiness
checks only the route-independent complete/hit-ID golden while each route
encoder and generated client verify its own success shape.

## Filtering, completeness and ranking

Authentication precedes body reads. Exactly one authenticated tenant becomes
service-built trusted scope; the caller's optional partition remains
separate. The service never accepts tenant ownership from request JSON.
Strict parsing and adapter preflight complete before per-list admission.

For parts, trusted tenant plus optional partition and up to three business
fields form one canonical exact-string AND filter on every probe. Predicates
run in Kurn before candidate cuts, not as middleware filtering of a truncated
hit set. Public filter charge uses active terms, not the number of declared
filterable columns.
For multi-select the one predicate includes membership terms; it does not
become one request per alternative. Full-set status remains charged.

The orchestrator executes one complete batch per pool rung, using the
descriptor's unchanged 128/256/512/1000 ladder. All probes in a producing rung
must be below their requested pool before resolution can declare completeness.
A saturated rung is discarded and a larger rung requested; the final ceiling
refuses without a partial result. Stats describe predicate work, not
completeness.

Each successful probe must carry the bound configuration digest and exact
filter evidence. New membership mode compares the raw echo against the fixed
daemon's JSON re-encoding of the canonical request; it never normalizes an
untrusted echo into equivalence. Canonical request bytes and their admission
limit are unchanged. Legacy scalar mode keeps its original raw-canonical echo
comparison and the documented affected-character limitation. All evidence in
an attempt batch for one physical list
must share the full served stamp. A typed list-identity mismatch can restart
the whole operation at rung one, within two identity attempts. Retired rungs
and attempts are never accumulated. Independent saturated rungs are not
cross-compared by stamp. Inline backend errors remain opaque refusals.

Only complete producing-rung evidence enters deterministic ranking and the
requested final limit. Parts checks candidate ID, canonical payload,
tenant/partition/business predicates, keys and score; disagreement refuses
rather than silently dropping candidates. Exact, starts, ends and word
evidence rank in that order, followed by distinct term counts and JS UTF-16 ID
ties. This is a reproducible identifier contract, not a relevance-quality
claim.

## Runtime and local preparation boundaries

One request-receipt deadline covers queueing, execution and encoding.
Per-list inflight admission defaults to one without stronger capacity
evidence. Private admission and public prepared-query accounting are distinct.
The service maps closed error categories, bounds retry hints, and does not
automatically retry or follow redirects.

The helper accepts only the exact checked four-file bundle for the selected
recipe and refuses existing output, bad identities, paths, links or modes.
It creates mode-controlled fixture/configuration inputs under a private,
quiescent parent. Its cooperating-writer reservation is not adversarial
OS-enforced no-replace protection. It does not start the two daemons.
The manual keeps developer-created bundle/configuration files after shutdown.

The shipped local composition uses fixture authentication, loopback HTTP and
a shared service/management listener. Readiness is checked against the
configured recipe before status publication. Query text, payloads, tenant
scope, raw errors, addresses and credentials are excluded from observer
labels and proof reports.

## Current contracts and historical evidence

Current OpenAPI admits the closed three-name scalar/array wire superset.
Legacy `SearchRequest` and `search` stay scalar-only; a distinct
`PartsCatalogSelectRequest` and fixed-route methods add array support.
The service/adapter remain the behavior authorities; the schema is
not a general evaluator or a complete model of duplicate JSON names,
surrogates, tenant ownership or status semantics. Clients retain permissive
response decoding and optional stricter semantic verification.

`openapi/current-manifest.json` and `clients/current-manifest.json` contain
filtered upstream provenance summaries, without private source-layout metadata.
Their original receipt hashes remain provenance, not identities of the filtered
files. They do not attest the selected public source
tree. Its release inventory, client/runtime tests and prepared walkthrough
checks are separate; see [release verification](release.md). Historical
deployment proofs and their generators are not part of this distribution.

## Hard boundaries and non-goals

There is no Go import from the public Kurn module: the backend uses its
independent reviewed HTTP contract. Analyzer, shared executor/backend and
public engine semantics remain unchanged by the parts recipe.

No nonblank-query exemption, general full-text search, second domain,
arbitrary OR/NOT language, ranges, arbitrary aggregations beyond the checked
facet recipes, custom sort, paging, browser UI or arbitrary importer
is included. Local fixture proofs do not establish production authentication,
TLS, network isolation, supervisor integration, availability, durability,
capacity, relevance, customer fit or customer deployability. New production
obligations and customer-specific mappings require separately reviewed scope.
