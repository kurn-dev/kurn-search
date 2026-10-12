# Parts-catalog example: identifier search with field filters

For several selected values per field, see the separate
[multi-select example](../parts-catalog-select/README.md). This scalar recipe's
commands, data and existing client API stay unchanged.

This example shows how to load a small fictional parts catalog into Kurn, run
the local Search service in front of it, and query part identifiers from
TypeScript or Python while narrowing the answer by exact manufacturer,
category and lifecycle status. Everything runs on your machine, on loopback
addresses, with fixture authentication; nothing here is a deployment.

Two operations are combined and it helps to keep them apart:

- **Identifier matching** finds parts whose identifier equals, starts with,
  ends with or contains as a word what you typed. `AX-100` finds the exact
  parts first, then parts such as `AX-101` that share the word `AX`.
- **Field filtering** keeps only parts whose `manufacturer`, `category` or
  `status` equals the value you gave, exactly and case-sensitively, combined
  with AND. Filtering happens inside the search engine before any result
  limit, so a filtered answer is complete or the service says it is not.

The step-by-step recipe, including building the pinned Kurn binaries,
preparing the local configuration with `kurn-search-local prepare -recipe
parts-catalog`, starting both daemons and running the programs below, is the
parts-catalog section of [`docs/user-manual.md`](../../docs/user-manual.md).

## Files

| File | Purpose |
|---|---|
| `source.ndjson` | The 152 checked fictional records, one JSON object per line: `id`, `identifier`, `manufacturer`, `category`, `status` (`active` or `discontinued`), `tenant_id`, optional `partition_id`. Twelve readable core parts are followed by a labelled block `ZB-9000`…`ZB-9139` used to demonstrate completeness when a query matches more parts than the first result pool holds. |
| `list-feed.ndjson` | The same records with a `keys` array: the search keys the adapter derives from each identifier. This is the input to `kurn build`; it is generated, never hand-edited. |
| `list-mapping.json` | The public build recipe: exact mode, no analyzer steps, keys from the `keys` array, the seven record members as payload, and the five filterable names `category`, `manufacturer`, `partition_id`, `status`, `tenant_id`. |
| `search.py` | Python example: runs the documented request set against the live service and checks every answer. |
| `consumer/` | A separate TypeScript project that installs the source client package locally, compiles with the pinned compiler, and runs the same request set. `src/parts.ts` is a small typed wrapper that narrows `status` to its two values. `negative/wrong-request.ts` must fail to compile. |

## What the programs check

Both programs send the query `AX-100` for tenant-a with limit 25 and compare
the complete answer, identifiers in order and full payloads, with the checked
expectation:

| Request | Expected identifiers, in order |
|---|---|
| no filters | PC-001, PC-002, PC-003, PC-004, PC-010, PC-011, PC-006, PC-007, PC-008, PC-012 |
| `manufacturer: Northstar` | PC-001, PC-003, PC-004, PC-010, PC-006, PC-012 |
| `category: bearing` | PC-001, PC-002, PC-003, PC-010, PC-011, PC-006 |
| `status: active` | PC-001, PC-002, PC-004, PC-010, PC-011, PC-006, PC-008, PC-012 |
| all three (`Northstar`, `bearing`, `active`) | PC-001, PC-010, PC-006 |
| `manufacturer: Unknown` | none, with `complete: true` |
| `status: retired` | refused: HTTP 400 `invalid_request`, before any backend work |

Notice that `PC-011` (manufacturer `northstar`) and `PC-012` (category
`Bearing`) drop out of the filtered answers: filter values are exact. A value
with a leading space matches nothing.

Each program prints one line per request, `ok` or `MISMATCH`, followed by the
identifiers returned, and exits 0 only when every request matched. Exit 2
means the environment or a request failed; exit 1 means an answer differed.

Environment for both: `KURN_SEARCHD_ADDR` must be the documented loopback
address of the running service. The Python program also needs
`KURN_SEARCH_REPO` (the checkout) to import the checked client package
without installation. The bearer value `client-token-a` is a fixture handle
accepted only by the local fixture authenticator; it is not a credential.

## Checked data

`source.ndjson`, `list-feed.ndjson` and `list-mapping.json` are derived from
the adapter's embedded neutral fixture. Byte-equality and regeneration checks
run in the upstream development harness, which is not included in this source
distribution. There is no local regeneration command here. Review fixture
changes upstream before replacing these files; do not generate expected answers
from observed results. The public query programs check the shipped expectations
against the running service.

## Limits

Local developer ergonomics only. No full-text search, no ranges or wildcards,
no OR or NOT, no sorting options, no pagination, no browser UI, no hosted
service, and no production security, availability or capacity claim.
