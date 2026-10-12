# Parts-catalog faceted discovery example

This example answers "use Kurn as the search backend for my parts screen,
with counts." One search box returns the same ordered hits as the
[parts-discovery example](../parts-catalog-discover/README.md), plus exact
manufacturer/category/status counts over the complete matched set before the
result limit.

The facet route `parts-catalog-discover-facets` reuses the checked 152-record
`parts-catalog-discover-v1` bundle byte-for-byte; there is no separate source
or index to build. The mapping, bundle and backend are exactly the discovery
example's.

## Build and run

Build `kurn`, `kurnd`, `kurn-search-local` and `kurn-searchd` as shown in the
[user manual](../../docs/user-manual.md#faceted-parts-catalog-discovery).
Then build and prepare this example alongside the parity discovery
configuration if you want both routes:

```sh
kurn build \
  -in /absolute/path/to/kurn-search/examples/parts-catalog-discover/source.ndjson \
  -mapping /absolute/path/to/kurn-search/examples/parts-catalog-discover/list-mapping.json \
  -out /absolute/private/path/to/bundle

kurn-search-local prepare \
  -recipe parts-catalog-discover-facets \
  -bundle /absolute/private/path/to/bundle \
  -out /absolute/private/path/to/run-facets \
  -kurnd-addr 127.0.0.1:8080 \
  -searchd-addr 127.0.0.1:8081
```

Start `kurnd` with the prepared `run-facets/data` directory, then start:

```sh
kurn-searchd -config /absolute/private/path/to/run-facets/kurn-searchd.json
```

Set `KURN_SEARCHD_ADDR=127.0.0.1:8081`. The TypeScript project under
`consumer/` uses the repository's private generated client as an offline
local dependency. The Python journey is `search.py` and additionally needs
`KURN_SEARCH_REPO` set to this checkout.

## Expected searches

| Search | Extra constraint | Total | Hits | Manufacturer buckets |
|---|---|---|---|---|
| `AX-100` | none | 7 | 7 | Northstar 4, Halden 2, northstar 1 |
| `gasket` | none, limit 25 | 141 | 25 | Halden 139, Vega 2 |
| `gasket` | status `discontinued` | 2 | 2 | Halden 1, Vega 1 |
| `sprocket` | none | 0 | 0 | none |

Counts are exact and describe the complete, currently filtered matched set
before the limit; `hits` is that ordered set's prefix. `total_matches: 0`
with three empty groups is a successful zero-match, not an error. The
`gasket` collection escalates its pool from 128 to 256 to stay complete
(141 rows).

Both checked client journeys also prove that `category: []` is refused before
transport and that an untrimmed query is classified by the service as HTTP
400 `invalid_request`.

If the complete matched set ever reached 1,000 rows, the route would refuse
HTTP 422 `incomplete_at_pool_ceiling` with no partial count; on this checked
152-row example that cannot happen.

This example is a fictional local journey. It is not a production,
full-text, relevance, aggregation-capability or customer-readiness claim.
