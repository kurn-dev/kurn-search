# Parts-catalog discovery example

This is the shortest end-to-end answer to “use Kurn as the search backend for
my parts screen.” One search box finds either an exact/partial part identifier
or words in the part name; the same request can constrain manufacturer,
category, status and partition.

The checked `source.ndjson` has 152 fictional records and their generated
domain-separated keys. `list-mapping.json` builds the dedicated exact list
`parts-catalog-discover-v1`; it is not interchangeable with the older parts
or suggestion bundles.

## Build and run

Build `kurn`, `kurnd`, `kurn-search-local` and `kurn-searchd` as shown in the
[user manual](../../docs/user-manual.md#multi-field-parts-catalog-discovery).
Then build and prepare this example:

```sh
kurn build \
  -in /absolute/path/to/kurn-search/examples/parts-catalog-discover/source.ndjson \
  -mapping /absolute/path/to/kurn-search/examples/parts-catalog-discover/list-mapping.json \
  -out /absolute/private/path/to/bundle

kurn-search-local prepare \
  -recipe parts-catalog-discover \
  -bundle /absolute/private/path/to/bundle \
  -out /absolute/private/path/to/run \
  -kurnd-addr 127.0.0.1:8080 \
  -searchd-addr 127.0.0.1:8081
```

Start `kurnd` with the prepared `run/data` directory, then start:

```sh
kurn-searchd -config /absolute/private/path/to/run/kurn-searchd.json
```

Set `KURN_SEARCHD_ADDR=127.0.0.1:8081`. The TypeScript project under
`consumer/` uses the repository's private generated client as an offline
local dependency. The Python journey is `search.py` and additionally needs
`KURN_SEARCH_REPO` set to this checkout.

## Expected searches

| Search | Extra constraint | Ordered IDs |
|---|---|---|
| `AX-100` | none | `PC-001, PC-002, PC-003, PC-010, PC-011, PC-004, PC-008` |
| `ball bearing` | none | `PC-001, PC-002, PC-003, PC-010, PC-011` |
| `ball` | manufacturer `Northstar` | `PC-001, PC-003, PC-010` |
| `filter` | partition `south` | `PC-008` |
| `AX-bearing` | none | empty, complete success |

The last case is intentional: word pieces of one term cannot be taken from
different fields. Exact identifier matches rank first; then whole-word
matches rank before prefix-only matches. `limit` is applied only after the
qualifying set is known complete.

Both checked client journeys also prove that `category: []` is refused before
transport and that an untrimmed query is classified by the service as HTTP 400
`invalid_request`.

This is loopback fixture-authenticated developer evidence. It makes no claim
about production authentication, TLS, tenant isolation, availability,
durability, capacity, customer fit or relevance quality.
