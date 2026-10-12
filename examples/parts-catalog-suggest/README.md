# Part-number similarity suggestion example

This example loads the checked 152-record fictional parts catalog into a
dedicated Kurn n-gram list, runs `kurn-searchd` with the
`parts-catalog-suggest` adapter, and checks scored results from TypeScript or
Python. Everything stays local on loopback with fixture authentication.

## Build and prepare

Build the pinned public `kurn` and `kurnd`, plus this repository's
`kurn-search-local` and `kurn-searchd`, as described in the
[user manual](../../docs/user-manual.md). Build this example's bundle with
`source.ndjson` and `list-mapping.json`, then prepare it with:

```sh
kurn-search-local prepare \
  -recipe parts-catalog-suggest \
  -bundle /absolute/path/to/bundle \
  -out /absolute/private/path/to/new-run-root \
  -kurnd-addr 127.0.0.1:8080 \
  -searchd-addr 127.0.0.1:8081
```

The mapping is fixed: one raw identifier key, lowercase-plus-trim analyzer,
character grams `[2,3]`, threshold `0.65`, an exact companion index for the
transposition fallback, and the five checked filterable fields. The helper verifies the resulting bundle's mode, analyzer, exact
hashes, 152 entries and 152 keys before creating the run root.

Start `kurnd` with the prepared `data` directory and then start:

```sh
kurn-searchd -config /absolute/private/path/to/new-run-root/kurn-searchd.json
```

Run the clients with `KURN_SEARCHD_ADDR=127.0.0.1:8081`; Python additionally
needs `KURN_SEARCH_REPO` set to this checkout. The TypeScript consumer is a
separate ESM project under `consumer/` and uses the private client as an
offline local-path dependency, following the manual's existing install steps.

## Checked behavior

The programs use the fixed-route methods `searchPartsCatalogSuggest` and
`search_parts_catalog_suggest`. They verify:

| Request | Ordered result (`entry_id:score`) |
|---|---|
| `GX-200` | `PC-009:100` |
| `GX-201` | `PC-009:93` |
| `AX-1O0` | nine checked AX records, all `100` |
| `AX-100`, manufacturer `Northstar` | four exact records at `100`, then `PC-006` and `PC-012` at `78` |
| `AX-100`, category `[filter,gasket]` | `PC-004:100`, `PC-007:78`, `PC-008:76` |
| `AX-100`, manufacturer `northstar` | `PC-011:100` |
| `Z` | complete empty result |

Filters are literal and case-sensitive. Arrays mean OR within a field; fields,
trusted tenant and optional partition combine with AND. Kurn applies the
predicate before its candidate cut.

## Reading the score

Similarity hits report Kurn's rounded coverage of corpus-known character 2-grams and
3-grams, not probability, confidence, edit distance or a character-match
percentage. Query grams absent from the corpus are excluded from the
denominator. That is why the inexact `AX-1O0` example can yield 100-score
results. A different corpus can change similarity scores and ordering.

Exact companion hits instead receive 100 from whole-key membership of a
generated transposition variant and carry `transposition: "yes"` evidence.
Under the union policy, transposition evidence wins for an entry found by both
branches. Neither score is probability, confidence or edit distance.

The service uses pool rungs `128/256/512/1000` and applies caller `limit` only
after the qualifying set is known complete. Saturation at the final rung is an
HTTP 422 refusal, never a silently truncated success.

This example is local developer evidence only. It makes no production
authentication, TLS, isolation, deployment, availability, capacity or
relevance-quality claim.
