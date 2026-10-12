# catalog-identifier example

This directory holds the neutral example used by the
[local user manual](../../docs/user-manual.md):

- `list-feed.ndjson` — one neutral record (`config-proof`, identifier
  `ABC-123`, tenant `tenant-proof`, partition `partition-proof`);
- `list-mapping.json` — the exact-match catalog-identifier mapping that
  `kurn build` consumes; and
- `search.mjs` — a small program that calls `kurn-searchd` through the checked
  generated TypeScript client and checks the exact expected hit.

The feed and mapping are byte-identical copies of the adapter's checked
`adapters/catalogid/testdata` inputs. An upstream development check keeps the
copies in step; that harness is not part of this distribution. The values are
fixture values, not customer data.

Use the manual's quick path and local setup helper to prepare the service
files automatically, or its full manual path to inspect every step. To use
the client from a compiled external application, continue with the
[TypeScript consumer](../typescript-consumer/README.md).

## Run

Complete the manual's steps so that `kurnd` and `kurn-searchd` are running,
then, with the same environment values exported:

```sh
cd "$KURN_SEARCH_REPO"
node examples/catalog-identifier/search.mjs
```

The program accepts only the documented loopback address from
`KURN_SEARCHD_ADDR`, sends the bearer `client-token`, which is the checked
local fixture-auth handle and not a credential, and exits 0 only when the
response is complete and its hit set is exactly:

```json
{
  "complete": true,
  "hits": [
    {
      "entry_id": "config-proof",
      "result": {
        "id": "config-proof",
        "identifier": "ABC-123",
        "partition_id": "partition-proof",
        "tenant_id": "tenant-proof"
      }
    }
  ]
}
```

It prints no timing or runtime detail. An upstream clean-root acceptance harness
runs the manual's command blocks and this program. That harness is not included
in this distribution; the command above checks the shipped example locally.
