# Part-number similarity suggestions

`parts-catalog-suggest` v1 is the scored suggestion recipe for the checked
fictional parts catalog. It projects one raw `identifier` key per record into
the dedicated `parts-catalog-suggest-v1` list.

The legacy bundle recipe uses Kurn n-gram mode with analyzer steps `lowercase`, `trim`, grams
`[2,3]`, `strip_spaces: false`, threshold `0.65` and `exact_companion: true`
(an exact index over the same keys, beside the n-gram index). Each request
compiles to one similarity probe. The adapter always supplies an explicit pool
from `128/256/512/1000`; a candidate count below the pool is complete,
equality escalates, and equality at 1000 refuses instead of returning a
partial result.

When the similarity probe observes no candidate and the query has adjacent
transpositions of alphanumeric characters, a second step runs in the same
attempt: one exact any-of probe carrying every such variant (at most 24, in
position order) against the list's exact companion index, under the same
filters and pool. A hit is an identifier equal to one variant under the
analyzer; it carries `score` 100 and the evidence member `"transposition":
"yes"`, which similarity hits never carry. The fallback never runs beside
similarity hits, and a resolver recomputes the variant list from the request,
so a plan that does not match it is refused.

**Policies.** The adapter's empty policy is the legacy behaviour described
above (threshold 0.65, the transposition step on an empty pool); an existing
bound configuration without a policy member denotes it and is never
reinterpreted. `kurn-search-local prepare-artifact` selects `osa1-060`
(below) for a new version unless `-policy` says otherwise (`legacy`
prepares the empty policy explicitly).

**Opt-in experimental policy `osa1`** (default off). A prepared version built
with `kurn-search-local prepare-artifact … -policy osa1`, or a searchd
configuration whose `adapter` object carries `"policy": "osa1"`, binds the
adapter with its own profile diagnostic (`"policy": "osa1"`, threshold
450,000 millionths) and behaviour identity. Under it the similarity probe
runs at threshold 0.45, a similarity candidate is accepted only when its
identifier is within optimal-string-alignment edit distance one of the
analyzed query (one substitution, insertion, deletion or adjacent
transposition under `lowercase, trim`), similarity scores stay ranking inputs
in `[45, 100]`, and the transposition fallback is taken when no candidate is
accepted even though the pool was not empty. Malformed or foreign evidence is
refused before any acceptance decision, so it can never turn into a fallback
miss. The mapping and artifact are the default ones; the policy lives in the
service binding, and a configuration naming it with a default diagnostic (or
the reverse) is refused. The prepared default `osa1-060` is the same
verifier with the similarity probe at threshold 0.60 (scores in `[60, 100]`);
it is its own policy identity (diagnostic `"policy": "osa1-060"`, threshold
600,000 millionths) and shares `osa1`'s acceptance rule and behaviour digest.
The experimental variant `osa1-060-union` is `osa1-060` with the transposition
any-of issued for every query that has variants, accepted or not, and its hits
combined with the accepted similarity hits before ranking and the limit: an
entry found by both branches appears once, with the transposition evidence.

Results contain the checked seven source fields plus integer `score`. Order is
score descending, normalized exact identifier first, identifier in JavaScript
UTF-16 order, then entry ID in the same order. Normalized exact comparison is
only lowercase plus whitespace collapse.

The score is Kurn's rounded coverage of query grams that occur somewhere in
the current corpus. Unknown query grams are excluded from its denominator.
Consequently an unusual character may have no effect and an inexact identifier
may score 100. This is not edit distance, spelling correction, phonetic,
phrase, semantic or general fuzzy search; corpus mutations can change scores
and order.

The request accepts the same literal membership filters as
[`parts-catalog-select`](../partscatalogselect/README.md): scalar or bounded
arrays for `manufacturer`, `category` and `status`, ANDed across fields and
trusted tenant/optional partition, with OR within a field. Values are
case-sensitive and untrimmed. Kurn applies the complete predicate before the
candidate cut.

Source, request, evidence and result JSON are closed and bounded. Resolution
validates all producing evidence before applying caller `limit`, including
entry ID, canonical payload, tenant/partition/filter agreement, echoed raw
identifier key, unique IDs and policy-specific integer scores: `[65,100]`
for legacy, `[60,100]` for `osa1-060` and `osa1-060-union`, `[45,100]` for
`osa1` (exactly 100 for transposition hits). These are rounded score ranges;
the engine applies its threshold before rounding. The adapter does
not recompute an engine score without corpus statistics.

Use the [local TypeScript/Python example](../../examples/parts-catalog-suggest/README.md)
for legacy setup and visible score checks, or the
[prepared-version walkthrough](../../examples/parts-catalog-suggest-prepared/README.md)
for the new default and lifecycle. This is a private local recipe, not a
production deployment, relevance-quality or stable-scoring claim.
