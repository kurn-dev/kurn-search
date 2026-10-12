# Prepared parts-catalog suggestions

Use this path to turn application-owned source rows into a version that can
serve without the original source or mapping. `prepare-artifact` validates and
projects every source row, calls a supplied `kurn`, and publishes separate
immutable build and runtime directories. It never overwrites either target.
This is a local, loopback-only composition with demonstration authentication,
not a production deployment or availability claim. The
[release verification](../../docs/release.md) records the checks run against
this selected source tree. Those bounded fixtures are not workload sizing
or customer qualification.

## 1. Build and prepare a version

Use the engine revision and Go toolchain in the
[manual prerequisites](../../docs/user-manual.md#prerequisites), plus Python
3.14.5 for the dependency-free query below and curl for backend readiness.
Use absolute physical checkout paths (`pwd -P`), unused loopback ports and
a fresh private run root outside both checkouts. Do not reuse a prior version
directory. The engine revision is a checked local candidate, not a new
published release tag.

```sh
export KURN_REPO=/absolute/path/to/kurn
export KURN_SEARCH_REPO=/absolute/path/to/kurn-search
export PREPARED_RUN_ROOT="$(cd "$(mktemp -d)" && pwd -P)"
export PREPARED_VERSION="$PREPARED_RUN_ROOT/version-a"
export KURND_ADDR=127.0.0.1:18080
export KURN_SEARCHD_ADDR=127.0.0.1:18081
umask 077
chmod 700 "$PREPARED_RUN_ROOT"
mkdir "$PREPARED_RUN_ROOT/bin"
(cd "$KURN_REPO" && go build -o "$PREPARED_RUN_ROOT/bin/kurn" ./cmd/kurn)
(cd "$KURN_REPO" && go build -o "$PREPARED_RUN_ROOT/bin/kurnd" ./cmd/kurnd)
(cd "$KURN_SEARCH_REPO" && go build -o "$PREPARED_RUN_ROOT/bin/kurn-searchd" ./cmd/kurn-searchd)
(cd "$KURN_SEARCH_REPO" && go build -o "$PREPARED_RUN_ROOT/bin/kurn-search-local" ./cmd/kurn-search-local)
```

Keep these same environment values in every terminal below; do not run
`mktemp` again there. The supplied [source A](source-a.ndjson) has 24 fictional
records and a unique readiness record in tenant `alpha`, partition `north`.
For your own source, the selected readiness tenant/partition must contain
exactly one record; invalid source or an ambiguous readiness scope is refused.

```sh
"$PREPARED_RUN_ROOT/bin/kurn-search-local" prepare-artifact \
  -profile parts-catalog-suggest \
  -source "$KURN_SEARCH_REPO/examples/parts-catalog-suggest-prepared/source-a.ndjson" \
  -kurn "$PREPARED_RUN_ROOT/bin/kurn" \
  -kurnd "$PREPARED_RUN_ROOT/bin/kurnd" \
  -searchd "$PREPARED_RUN_ROOT/bin/kurn-searchd" \
  -search-checkout "$KURN_SEARCH_REPO" \
  -build-dir "$PREPARED_RUN_ROOT/build-a" -out "$PREPARED_VERSION" \
  -readiness-tenant alpha -readiness-partition north \
  -kurnd-addr "$KURND_ADDR" -searchd-addr "$KURN_SEARCHD_ADDR"
"$PREPARED_RUN_ROOT/bin/kurn-search-local" verify-version -out "$PREPARED_VERSION"
```

Require successful exit from both commands. Verification should report
`status: "VERIFIED"` and `startable: true` before the first start. It checks
manifest-bound files, ownership and modes; it is not a live-service probe.
Do not start a partial, uncertain or refused publication. Keep logs and any
other files outside the runtime directory (only its `state/` is mutable).
Paths in the configuration are absolute: keep the version at its prepared
path; do not relocate it or rewrite controls to make it load elsewhere.

## 2. Start, query and stop

In the first terminal, run the version's bundled backend in the foreground:

```sh
"$PREPARED_VERSION/bin/kurnd" -addr "$KURND_ADDR" \
  -artifact "$PREPARED_VERSION/data/parts-catalog-suggest-v1.kurn" \
  -api-keys-file "$PREPARED_VERSION/kurnd-api-keys"
```

In the second terminal, check backend readiness before starting searchd:

```sh
curl --fail --silent --show-error --include "http://$KURND_ADDR/readyz"
"$PREPARED_VERSION/bin/kurn-searchd" -config "$PREPARED_VERSION/kurn-searchd.json"
```

The backend must return HTTP 200 with `Kurn-Artifact-Verification: full`
(header names are case-insensitive). If it is not ready, do not continue to
searchd. Full verification is the default; do not add
`-artifact-verify trusted`, which this search composition refuses at readiness.

In a third terminal, wait for searchd to publish
`$PREPARED_VERSION/state/search-status.json` while its foreground process
remains running. Then run this authenticated sentinel query. It reads the
tenant handle inside Python instead of exposing it in command arguments or
printing it. No package installation is required:

```sh
PYTHONPATH="$KURN_SEARCH_REPO/clients/python" python3 - <<'PY'
import json
import os
from pathlib import Path
from kurn_search_client import KurnSearchClient, PartsCatalogSuggestRequest

version = Path(os.environ["PREPARED_VERSION"])
credentials = json.loads((version / "client-credentials.json").read_text())
handle = next(c["handle"] for c in credentials["credentials"] if c["tenant"] == "alpha")
client = KurnSearchClient("http://" + os.environ["KURN_SEARCHD_ADDR"], lambda: handle)
result = client.search_parts_catalog_suggest(
    PartsCatalogSuggestRequest("ZS-901", 1, partition_id="north"),
    timeout_seconds=5,
)
assert result["complete"] is True
assert [hit["entry_id"] for hit in result["hits"]] == ["A01"]
assert result["hits"][0]["result"]["score"] == 100
print("prepared sentinel: PASS (A01, score 100)")
PY
```

An `alpha` handle supplies tenant scope; do not send `tenant_id` as a request
field. For application integration, use the same endpoint/handle pair with
the [Python](../../clients/python/README.md) or
[TypeScript](../../clients/typescript/README.md) client. Do not substitute the
ordinary bundle example's fixed fixture handles.

Stop searchd first with Ctrl-C in its owning terminal and wait for exit, then
stop kurnd in its terminal and wait. Clean searchd shutdown removes the status
file. Run `verify-version` again and require `startable: true`, then repeat
these start/query steps to restart the unchanged version. Verification does
not prove that all processes have stopped; check the owning terminals too.
Do not kill processes by port or delete a status file while a process may live.
The build and runtime directories remain available for inspection and rollback.

## Try a typo, filter and no-match

While both services are running, use the same alpha handle in a third terminal.
`AX-010` transposes adjacent digits in `AX-100`. This example prints useful
source fields instead of only a readiness assertion:

```sh
PYTHONPATH="$KURN_SEARCH_REPO/clients/python" python3 - <<'PY'
import json
import os
from pathlib import Path
from kurn_search_client import KurnSearchClient, PartsCatalogSuggestRequest

credentials = json.loads((Path(os.environ["PREPARED_VERSION"]) / "client-credentials.json").read_text())
handle = next(c["handle"] for c in credentials["credentials"] if c["tenant"] == "alpha")
client = KurnSearchClient("http://" + os.environ["KURN_SEARCHD_ADDR"], lambda: handle)
for query, filters in [
    ("AX-010", None),
    ("AX-010", {"manufacturer": "Northstar", "status": "active"}),
    ("QQQQQQ", None),
]:
    result = client.search_parts_catalog_suggest(
        PartsCatalogSuggestRequest(query, 10, partition_id="south", filters=filters),
        timeout_seconds=5,
    )
    assert result["complete"] is True
    print(query, [hit["entry_id"] for hit in result["hits"]])
    for hit in result["hits"]:
        row = hit["result"]
        print(row["identifier"], row["manufacturer"], row["status"])
PY
```

The first query returns A02/A03/A04, all with identifier `AX-100`. The filter
returns A02/A03, active Northstar records. The final query returns no hits.
Separate source IDs remain separate even when identifiers repeat; beta's
records are not returned to alpha. Manufacturer filters are case-sensitive.

Similarity scores are corpus-known coverage. Exact transposition hits instead
score 100 for membership of a generated variant. Neither score is confidence
or probability. A TypeScript application uses `searchPartsCatalogSuggest`
with `partitionId: "south"` on this same service; discovery needs its own
prepared service. Both clients and their templates are MIT-licensed.

## Admission and policy options

`-max-inflight N` (1-64, default 4) sets how many requests the version's
`kurn-searchd` admits concurrently; it is bound into the version's manifest,
config and profile diagnostic.

`-max-queue N` (1-4096, default 4 × `-max-inflight`) bounds how many
requests may wait for those admission slots; it is written explicitly into
the version's config (`adapter.max_queue`) and manifest. A request that finds
the queue full is refused at once with `503 service_queue_full` and a
`Retry-After` header, before adapter preparation (HTTP parsing and
authentication precede admission). This bounds waiting admission slots, not
all HTTP connections, request parsing, process memory or end-to-end latency.
A request admitted to the queue still waits
at most 5 s for a slot (`503 service_admission_unavailable`, unchanged).
The limit is shared across the service and does not promise fairness between
tenants. Both generated clients expose these 503 categories and the validated
retry hint without automatically retrying. An existing configuration
without the member keeps the service default.

Two different routes, two different defaults. The ordinary bundle recipe
(`kurn build` plus `kurn-search-local prepare -recipe parts-catalog-suggest`,
the manual's quickstart) binds the adapter's empty policy: threshold 0.65
with the exact transposition step only on an empty pool. The prepared
customer path above (`prepare-artifact`) binds `osa1-060` unless told
otherwise. They are not the same behaviour.

`-policy NAME` selects the suggest adapter's behaviour policy for the new
version. Omitted, it selects `osa1-060` (generation threshold 0.60, a
similarity candidate accepted only within optimal-string-alignment distance
one of the query, the exact transposition step taken when nothing is
accepted). `legacy` prepares the pre-0.60 behaviour (threshold 0.65, the
transposition step only on an empty pool); `osa1-060-union` always takes
the transposition step and combines both branches; `osa1` is the 0.45
experiment. Non-legacy policies are persisted explicitly in the version's
config (`adapter.policy`), manifest (`profile.policy`) and profile diagnostic,
each with its own identity. `-policy legacy` deliberately omits those members
to preserve the legacy identities. A configuration naming one policy with
another's diagnostic is refused.

New default versus legacy loading: a *new* preparation without `-policy`
binds `osa1-060` and writes the policy member. An *existing* bound
configuration without a policy member is, and stays, the legacy behaviour:
`kurn-searchd` reads the member as written and never reinterprets an
absent one, so versions prepared before this default keep serving exactly
as their diagnostic says. To prepare that behaviour anew, pass
`-policy legacy`.

## Package and replacement boundaries

Both target parents must already be physical, private `0700` directories
outside either checkout. Acceptance runs additionally supply
`-core-source-inventory` and `-search-source-inventory` streams; the simple
walkthrough above omits them and does not establish those inventory assertions.
The runtime package contains the artifact, two serving executables,
controls and generated local-only credentials, but not the source, mapping or
projected input. Credentials are synthetic demonstration material, not a
production authentication design.

Version B must use different absent build/runtime targets. Reusing A's ports
requires an explicit stop/start with an outage. Distinct ports allow a
caller-coordinated overlap as below, but there is no supervisor, hot replacement,
remote router or availability guarantee here.

## Coordinated credential replacement (fresh credentials per version)

Every `prepare-artifact` run generates its own client handles
(`client-credentials.json`), backend token and kurnd API keys. A client
configuration is therefore one coherent **pair**: a version's endpoint plus
that version's tenant handle. Replacement is a pair switch, never an endpoint
switch alone:

1. Prepare B into fresh build/runtime targets and run `verify-version` on it.
   Keep A untouched and serving.
2. Start B's `kurnd` and `kurn-searchd` beside A on their own addresses (the
   addresses bound at prepare time). Wait for B's readiness file, then issue
   one authenticated query with the **B** pair.
3. Switch each application or client instance to the B pair atomically for
   its new requests: endpoint and handle change together. Requests already
   issued against A finish against A (drain), then stop A. A mixed rollout is
   some clients on A/A and others on B/B, never A endpoint with B handle or
   the reverse; a mismatched pair is refused with `401 unauthenticated`
   (that refusal is the binding working, not an outage).
4. Rollback restores the **A pair** on every client, not only A's processes:
   start A again (its runtime directory is unchanged), switch the pairs
   back, drain B, stop B.
5. Keep A and its private runtime directory only for the agreed rollback
   window, then retire it through your normal owner-controlled removal.

Preserving a version means keeping its bound configuration **and** the
executables bundled in its runtime directory (`bin/`): a version is started
with its own `kurnd` and `kurn-searchd`, and a newer `kurn-searchd` refuses a
diagnostic identity it does not reproduce (for example an experimental
policy whose identity later changed). To move such a version onto a newer
build, prepare it anew under that build; never rewrite a prepared
version's diagnostic or configuration to force acceptance.

A blue/green stack where each application instance is deployed together with
its own search pair is the natural realization. A shared endpoint or DNS flip
alone is **not** an atomic endpoint-and-credential switch: during the flip
some requests carry the old handle to the new version and are refused with
401, so it cannot promise zero authentication failures. Clients that cannot
switch the pair need an explicitly coordinated maintenance window, or a
separately decided credential-compatibility mechanism; this product does not
carry credentials across versions, accept two versions' handles at once, or
run a credential service. A status-file
warning means a process may be running or a stale file remains. Confirm no
process is running before manually removing it. Do not start bytes that failed
final validation or were reported partial, uncertain, or refused after
publication.
