# A separate TypeScript application

This example consumes the source client by its legacy package name,
`@kurn-dev/kurn-search-client-private`, rather than importing a source-tree
file directly. It is copied to a fresh project outside the checkout for the
[manual's quick path](../../docs/user-manual.md).

## Run it

Follow the manual's quick path through service readiness, then its consumer
install, compiler, compile and run steps. The example expects the same
`KURN_SEARCHD_ADDR` environment value as the service, with a loopback
`127.0.0.1` listener. Use the unused port selected for the setup helper.

The prerequisites are the pinned Kurn source, this Search checkout,
Go 1.27, Node.js 25.6.1 and npm 11.9.0. The manual materializes TypeScript
6.0.3 using the existing checked compiler lockfile. The real npm local-path
client install is offline and runs no install scripts; compiler acquisition
may need a registry when its package is not cached and also runs no install
scripts. Neither step publishes a client package.

The checked `package.json` has no dependency entry or scripts. npm adds the
local file dependency to its external copy during the documented install.
That path is machine-specific; it is not a portable published package lock
or a file to copy back into this checkout.

## Files and behavior

- `src/search.ts` imports the generated client and its types and issues the
  bounded catalog-identifier request.
- `tsconfig.json` uses strict NodeNext ESM compilation, with output under
  `dist/`. No external Node type package is required.
- The negative compiler fixture is acceptance-test material, not part of the
  normal application run.

The request uses `ABC-123`, `partition-proof`, limit 10 and a two-second
timeout. `client-token` is the checked local fixture handle, not a production
credential. Success requires `complete: true` and exactly the ordered
`config-proof` hit with identifier `ABC-123`, partition `partition-proof`
and tenant `tenant-proof`. The program prints that projection and exits 0;
a mismatched hit set exits 1, while invalid environment or a failed request
exits 2. No timing detail is printed.

For your own local TypeScript project, use the manual's same offline
local-path install against `clients/typescript`, keep your project ESM, and
import the client by package name. The source of the client remains this
Search checkout; it is not installed from a registry.

The example establishes local developer consumption only. It does not
establish production authentication, TLS, tenant network isolation,
supervision, availability, durability, capacity or customer deployability.
