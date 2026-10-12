# kurn search

**Build search around your own data.**

kurn search provides example adapters for adding fuzzy and exact lookup to
your application, powered by [kurn](https://github.com/kurn-dev/kurn). Prepare a
reusable search version, run it alongside your application, and query it with
TypeScript or Python. Adapt the examples to define which data is searchable,
which queries you accept and what results your application returns.

## Try A Fuzzy Lookup

Start with [prepared part-number suggestions](examples/parts-catalog-suggest-prepared/README.md).
The walkthrough builds the tools, prepares 24 fictional records, verifies the
version, starts both services and queries them from a client. It shows a
mistyped identifier, exact filters, no-match behavior, restart and coordinated
replacement. Your source stays authoritative; the serving version needs
neither the original source nor its mapping.

Use Go 1.27, Node.js 25.6.1, Python 3.14.5 and a compatible Kurn engine checkout.
Use Kurn v0.7.0 at `06c97f9fe695e29ce5adfc05f502edd68f5790e2`.
It contains prepared artifacts and exact companions. Use that exact source
revision, or the v0.7.0 tag when available; do not assume an older `@latest`
engine tag contains these features.

```sh
go build ./cmd/kurn-searchd ./cmd/kurn-search-local
go test ./...
node --test clients/typescript/test/client.test.mjs
python3 clients/python/test_client.py
```

The clients can be used directly from this source checkout. No npm or PyPI
registry release is claimed. The legacy npm package name is retained for
source-example compatibility; its `private` flag prevents registry publication,
not use under the MIT license.

## Choose An Example

| Example | Query behavior |
| --- | --- |
| [Identifier lookup](examples/catalog-identifier/README.md) | Smallest exact identifier lookup. |
| [Parts with filters](examples/parts-catalog/README.md) | Identifier lookup with scalar manufacturer, category and status filters. |
| [Multi-select](examples/parts-catalog-select/README.md) | OR within selected fields, AND across fields and tenant scope. |
| [Discovery](examples/parts-catalog-discover/README.md) | Exact and partial identifiers, or words in part names. |
| [Facets](examples/parts-catalog-discover-facets/README.md) | Counts over the complete matched set before the caller limit. |
| [Facet alternatives](examples/parts-catalog-discover-facet-alternatives/README.md) | Counts that expose alternatives after a selection. |
| [Suggestions](examples/parts-catalog-suggest/README.md) | Scored typo-tolerant part-number suggestions. |

Each local service loads one adapter and its corresponding list. Run the
matching preparation before calling that adapter's client method. The
[architecture guide](docs/architecture.md) explains projection, query plans,
evidence validation and complete-result handling. The canonical HTTP contract
is [OpenAPI](openapi/kurn-search.openapi.json).

Similarity scores measure rounded corpus-known n-gram coverage. Exact
transposition hits instead receive 100 for whole-key membership of a generated
variant. Neither is confidence or probability; evaluate your own labelled
queries, false matches and capacity before deployment.

## Operating Boundaries

The examples bind to loopback and use demonstration authentication. Production
authentication, TLS, supervision and fleet operation need a separate deployment
design. Prepared versions are immutable and use fresh endpoint/handle pairs;
coordinate replacement and retain the old version for rollback. Collections
must fit one node's memory. Admission limits are not a total process-memory cap.

Swarm is experimental and is not included as a supported product or Search
backend in this release. The helper's required implementation libraries are
not a supported distributed control plane. Private performance commands,
deployment tooling and historical evidence generators are not part of this
source distribution.

## License And Provenance

Search service, adapters, Go tooling and examples are AGPL-3.0-only. The thin
TypeScript and Python clients and their template sources are MIT. See
[licensing boundaries](LICENSING.md) and the license in each client directory.

The public source inventory and executed release checks are recorded separately
from filtered upstream provenance summaries. Private generator-layout metadata
is not included. See [release verification](docs/release.md).
