# Run Kurn Search

Start with [prepared part-number suggestions](../examples/parts-catalog-suggest-prepared/README.md).
That example shows source records, preparation, verification, local serving,
client queries, restart and replacement. The ordinary bundle recipes below
are alternatives, not prerequisites for prepared serving.

## Prerequisites

Use Go 1.27, Node.js 25.6.1, Python 3.14.5 and curl. Use a compatible engine
checkout containing prepared artifacts and exact companions; the reviewed
candidate is `3d6c938ab3d59bbd87bc697e5e25610569fbac4d`. Until the coordinated
engine release is published, `@latest` must not be assumed to include it.
Set checkout paths to absolute physical paths (`pwd -P`). Keep generated
versions and credentials in fresh private directories outside both checkouts.

```sh
export KURN_REPO=/absolute/path/to/kurn
export KURN_SEARCH_REPO=/absolute/path/to/kurn-search
export KURN_SEARCH_RUN_ROOT="$(cd "$(mktemp -d)" && pwd -P)"
umask 077
chmod 700 "$KURN_SEARCH_RUN_ROOT"
mkdir "$KURN_SEARCH_RUN_ROOT/bin"
(cd "$KURN_REPO" && go build -o "$KURN_SEARCH_RUN_ROOT/bin/kurn" ./cmd/kurn)
(cd "$KURN_REPO" && go build -o "$KURN_SEARCH_RUN_ROOT/bin/kurnd" ./cmd/kurnd)
(cd "$KURN_SEARCH_REPO" && go build -o "$KURN_SEARCH_RUN_ROOT/bin/kurn-searchd" ./cmd/kurn-searchd)
(cd "$KURN_SEARCH_REPO" && go build -o "$KURN_SEARCH_RUN_ROOT/bin/kurn-search-local" ./cmd/kurn-search-local)
```

Reuse these values in every terminal; do not run `mktemp` separately in each.
The prepared example also supplies its own complete setup. Choose either
setup once, then follow that example's variable names consistently.

## Prepared Suggestions

Follow the [prepared walkthrough](../examples/parts-catalog-suggest-prepared/README.md).
Keep its readiness check, then try `AX-010` in alpha's south partition to
retrieve `AX-100`. Filter manufacturer `Northstar` and status `active` to
select A02/A03. `QQQQQQ` has no matches. Tenant scope comes from the handle;
do not send it as a query field. Exact transposition hits score 100 for
membership of the generated variant, not confidence in a suggested record.

The source fields are `id`, `identifier`, `manufacturer`, `category`, `status`,
`tenant_id` and optional `partition_id`. Follow the profile's validation rules.
The selected readiness scope must contain exactly one actual source record.
Use new build/version targets and new handles for new source versions.

## Quick Path: Prepare The Example, Then Use A TypeScript Application

The [one-record identifier example](../examples/catalog-identifier/README.md)
shows the smallest ordinary bundle path. The helper stages checked fixture
authentication, a list binding and a readiness probe. Start `kurnd` first,
then `kurn-searchd`; call only the configured adapter. These examples use
source-checkout clients, not npm or PyPI packages.

## Parts Catalog: Search With Field Filters

Use [the parts example](../examples/parts-catalog/README.md). Filters are
literal and case-sensitive; manufacturer, category and status are combined
with AND before Kurn's candidate cut. A list must declare the filter paths.

## Multi-Select Parts Catalog: Choose Several Values Per Field

Use [the multi-select example](../examples/parts-catalog-select/README.md).
Arrays mean OR within a field and AND across fields. Empty arrays refuse;
omission does not restrict that field.

## Part-Number Suggestions: Scored Similarity Over The Example Catalog

Use [the ordinary suggestion example](../examples/parts-catalog-suggest/README.md)
when deliberately selecting the legacy 0.65 empty-pool fallback policy.
Prepared versions default to `osa1-060`: 0.60 generation, distance-one
acceptance and transposition fallback when nothing is accepted. See the
[adapter reference](../adapters/partscatalogsuggest/README.md) for opt-in union
and experimental policies. Client requests do not select the service's policy.

## Multi-Field Parts-Catalog Discovery

Use [the discovery example](../examples/parts-catalog-discover/README.md).
It has its own exact list and preparation. A suggestion service does not
also serve discovery: the wrong adapter route returns `404 adapter_not_found`.

## Faceted Parts-Catalog Discovery

Use [the facets example](../examples/parts-catalog-discover-facets/README.md).
Counts cover the complete matched set before the caller limit. To compile
the TypeScript consumer, use the pinned TypeScript 6.0.3 development compiler;
source client installation and compiler acquisition are separate steps.

For a source-checkout TypeScript consumer, copy its `consumer/` directory to
a fresh application directory. In that directory, install the local SDK with
`npm install --offline --ignore-scripts --package-lock=false --no-audit
--no-fund "$KURN_SEARCH_REPO/clients/typescript"`. This overrides the copied
example's relative source dependency; it does not publish a registry package.
Use the pinned compiler to run the example's positive `tsconfig.json` and
require its negative configuration to fail:

```sh
export COMPILER_ROOT="$(mktemp -d)"
cp "$KURN_SEARCH_REPO/tools/typescript/package.json" \
   "$KURN_SEARCH_REPO/tools/typescript/package-lock.json" "$COMPILER_ROOT/"
(cd "$COMPILER_ROOT" && npm ci --ignore-scripts --no-audit --no-fund)
node "$COMPILER_ROOT/node_modules/typescript/bin/tsc" -p /absolute/path/to/application/tsconfig.json
```

## Interactive Facet Alternatives

Use [the alternatives example](../examples/parts-catalog-discover-facet-alternatives/README.md).
Hits apply every filter; each facet group's counts omit only its own selection.
The consumer's positive and negative type checks are separate from runtime
response validation.

## Troubleshooting

The ordinary helper's low-level [fixture controls](manual-fixtures.md) are
preserved and checked byte-for-byte separately from this beginner guide.

- `configuration refused`: check matching adapter/list/diagnostic identities,
  absolute paths, private modes and the selected engine revision. Do not edit
  bound controls or weaken verification to force a version to start.
- `readiness unavailable`: start the backend, require full verification,
  then wait for the Search status file and issue an authenticated query.
- `401 unauthenticated`: use the same version's endpoint and tenant handle.
- `503 service_queue_full`: apply a bounded retry policy within the caller's
  deadline; clients report the retry hint without retrying automatically.

## Stop, Restart And Replace

Stop Search first and wait, then stop the backend and wait. Require
`verify-version` to report `startable: true` before restarting with that
version's bundled executables. Never remove a live process's status file.

Prepare B at new paths and addresses, verify it, require full backend readiness
and an authenticated B query, then switch endpoint and handle together.
Drain A before stopping it. For rollback, verify and restart A, check readiness
and an authenticated A query, then restore A's pair and drain/stop B.

Loopback authentication is demonstration material. TLS, production identity,
supervision, fleet operations, capacity and workload relevance need separate
evaluation. See [release verification](release.md)
for the selected source and evidence boundaries.
