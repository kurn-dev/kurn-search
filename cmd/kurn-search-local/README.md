# Prepare local search workspaces and immutable versions

`kurn-search-local` has three subcommands: `prepare` for checked bundle
examples, `prepare-artifact` for a source-to-artifact version, and
`verify-version` for inspecting a prepared version. None starts the daemons.

## Prepared-artifact path

Follow the [prepared suggestions walkthrough](../../examples/parts-catalog-suggest-prepared/README.md)
for all prerequisites, flags and the complete lifecycle. `prepare-artifact`
validates and projects the supplied source, invokes the supplied `kurn` to
build and semantically verify the artifact, and publishes separate absent-only
build/runtime directories. The runtime includes its own `kurnd` and
`kurn-searchd`, immutable controls and freshly generated local-only credentials.
Preserve its absolute path and bundled executable/configuration pairing.

New versions default to `osa1-060`; `-policy legacy` selects the ordinary
recipe's semantics. `-max-inflight` and `-max-queue` bind the version's service
admission limits. These flags are not options to `prepare`.

`kurn-search-local verify-version -out /absolute/private/version-a` checks
the manifest-bound files, hashes, ownership and modes. Inspect both `status`
and `startable`: `VERIFIED` with `startable: false` warns that the status file
is present (running or stale), not that another daemon may be started. This
file verification is not a fresh live-service readiness or semantic probe.

## Ordinary bundle path (`prepare`)

`prepare` prepares the catalog-identifier, scalar parts-catalog,
multi-select parts-catalog-select, multi-field parts-catalog-discover,
its full-match or interactive-alternative facet view, or scored parts-catalog-suggest
example without hand-written service configuration. It prepares files only.
You still build and run the public Kurn backend and private search service as
separate foreground processes.

For the full source-to-client walkthrough, see the
[local user manual](../../docs/user-manual.md). For the sample data and exact
expected results, see the [catalog example](../../examples/catalog-identifier/README.md)
or [filtered parts example](../../examples/parts-catalog/README.md).
For the search-box path, use the
[parts discovery example](../../examples/parts-catalog-discover/README.md).

### Inputs

`prepare` accepts this subcommand and four required flag/value pairs, in any
order. Optional `-recipe` selects `catalog-identifier` (the default) or
`parts-catalog`, `parts-catalog-select`, `parts-catalog-discover`,
`parts-catalog-discover-facets`, `parts-catalog-discover-facet-alternatives`
or `parts-catalog-suggest`; no other recipe is accepted:

```text
kurn-search-local prepare
  -recipe parts-catalog
  -bundle /absolute/physical/path/to/bundle
  -out /absolute/physical/path/to/new-workspace
  -kurnd-addr 127.0.0.1:8080
  -searchd-addr 127.0.0.1:8081
```

Build this repository's command at `./cmd/kurn-search-local` with the same Go
toolchain as the private service. There are no environment or stdin defaults,
positional arguments, repeated flags, equals-form flags, discovery or reset
options.

The bundle must match the selected checked example built by the pinned public
Kurn revision: the original one-record catalog, the 152-record exact parts
catalog, the independent 152-record exact identifier/name discovery catalog,
or the dedicated 152-record n-gram suggestion list. The exact scalar and
multi-select parts recipes use the same exact bundle. Both discovery-facet
recipes reuse the discovery bundle byte-for-byte. Multi-select changes the
configured adapter and readiness route, not the source data. Discovery has a
separate mapping and bundle because it adds names and domain-separated keys.
Unlike `prepare-artifact`, `prepare` does not project source data or build an index. First build the
checked feed and mapping with `kurn build`; then pass its bundle here.
Its manifest and all three data files are checked before any output is
created. Other indexes are refused, even if
their manifests look plausible.
Manifest mode and analyzer digest are checked per recipe: `exact` with the
empty analyzer for the six delivered exact recipes, or `ngram` with the
fixed lowercase-plus-trim analyzer for suggestions.

Use absolute physical paths (for example, obtained with `pwd -P`). The output
must not exist, must not overlap the bundle in either direction, and must not
be inside a Git checkout or worktree. Its existing parent must be mode 0700
and exclusively owned and quiescent during preparation. Symlinked paths,
hard-linked bundle files, unsafe modes and oversized inputs are refused.
Both addresses must use literal `127.0.0.1`, distinct ports from 1 through
65535, and canonical decimal port numbers without leading zeros.

### What is created

The newly owned output root contains seven mode-0600 files:

```text
new-workspace/
  backend-token
  fixture-auth.json
  readiness.json
  kurn-searchd.json
  data/
    parts-catalog-suggest-v1/
      config.json
      base.jsonl
      base.idx
```

The root and its two descendant directories are mode 0700. No status file is
created by preparation; `kurn-searchd` creates it after its readiness check.
For exact parts it is `parts-catalog-v1`; for suggestions it is
`parts-catalog-suggest-v1`; for the default catalog recipe it is
`catalog-identifiers-v1`; discovery uses `parts-catalog-discover-v1`.
Omitting `-recipe` and explicitly selecting `catalog-identifier` produce the
same original configuration and fixture bytes for the same paths and addresses.

The three bundle data files are copied byte-for-byte. The served configuration
digest comes from the validated manifest's `config_sha256`, not from hashing
the raw `config.json` file. The four private inputs use the selected recipe's
fixed fixtures, with paths and addresses filled in. Both staged and published
configuration are checked by the strict single-adapter service loader.

The catalog's fixed `client-token` is a local fixture handle. The parts recipe
uses `client-token-a` and `client-token-b`, bound to `tenant-a` and `tenant-b`;
its readiness query is `GX-200` for tenant-a, expecting only `PC-009`.
These values are deliberately non-secret test data.
`unused-local-backend-token` is a required placeholder for the local backend
configuration. None is a production credential. `prepare` creates no
real key or credential and must not be used to provision one.

### Result and refusal

On success the command exits 0 and prints one JSON line with schema
`kurn-search-local-prepare-result/v1`, status `PREPARED`, the physical list and
configuration identity, seven files and two descendant directories. The
directory count excludes the output root itself. The result contains no
paths, addresses or fixture handles.

Any input or preparation refusal exits 1, writes
`kurn-search-local: refused` to stderr, and has no success output. Check the
selected recipe, four required inputs, bundle identity, paths, permissions and
destination absence; do not delete an existing workspace to force the command
through.

Preparation uses the fixed sibling `.<output-name>.local-setup` as an exclusive
reservation. A pre-existing reservation is refused and preserved. Cooperating
invocations cannot both prepare the same destination. Failed preparation
cleans only its own reserved output, not pre-existing objects.

Ordinary directory rename is absent-only under the documented exclusively
owned, quiescent-parent requirement; it is not an OS-enforced no-replace
primitive against a noncooperating same-user writer. The command does not claim
adversarial filesystem protection or crash-durable recovery. If interrupted,
inspect any residual reservation before taking a separate cleanup action.

The helper never starts or signals a daemon, retries, follows a redirect,
downloads a package or edits a checkout. This is local configuration
ergonomics, not production deployment, authentication, TLS, network isolation,
supervision, provenance, availability, durability, capacity or customer
deployability.
