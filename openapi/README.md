# Search HTTP Contract

`kurn-search.openapi.json` is the canonical Search HTTP description.
`fixtures.json` contains admitted requests, responses and refusals used by the
contract tests. This API is distinct from the engine's `kurnd` API.

Run `go test ./internal/openapi ./service` to check the contract and actual
handler behavior. Generated TypeScript and Python clients call fixed adapter
routes and validate responses; the service runs one configured adapter.

`current-manifest.json` summarizes upstream proof facts and the public API and
fixture hashes, with a digest of the complete upstream receipt. Private source
inventories and generator commands are omitted. The summary is not an inventory
of this selected public source tree. No private generator or Git history is needed to build and test
this distribution. See [release verification](../docs/release.md) for the
public source inventory and executed checks.
