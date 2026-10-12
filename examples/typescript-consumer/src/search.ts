// Separate TypeScript consumer of the private kurn-search client package.
//
// This file is compiled from a copy placed outside the source tree, after the
// documented offline local install of `@kurn-dev/kurn-search-client-private`
// from the checkout. It behaves exactly like examples/catalog-identifier/
// search.mjs: it accepts only the documented loopback address, sends the
// checked local fixture-auth handle (not a credential), and exits 0 only when
// the response is complete and its hit set is exactly the checked example.
import { KurnSearchClient } from "@kurn-dev/kurn-search-client-private";
import type { SearchRequest, SearchSuccess } from "@kurn-dev/kurn-search-client-private";

const FIXTURE_HANDLE = "client-token";
const ADAPTER = "catalog-identifier";
const REQUEST: SearchRequest = { query: "ABC-123", partitionId: "partition-proof", limit: 10 };

interface ExampleHit {
  readonly entry_id: string;
  readonly result: unknown;
}

const EXPECTED_HITS: readonly ExampleHit[] = [
  {
    entry_id: "config-proof",
    result: {
      id: "config-proof",
      identifier: "ABC-123",
      partition_id: "partition-proof",
      tenant_id: "tenant-proof",
    },
  },
];

function fail(message: string, code: number): never {
  process.stderr.write(`consumer: ${message}\n`);
  process.exit(code);
}

const address = process.env["KURN_SEARCHD_ADDR"];
if (typeof address !== "string" || !/^127\.0\.0\.1:[0-9]{1,5}$/.test(address)) {
  fail("KURN_SEARCHD_ADDR must be the documented loopback address", 2);
}

const client = new KurnSearchClient({
  endpoint: "http://" + address,
  getToken: () => FIXTURE_HANDLE,
});

let result: SearchSuccess;
try {
  result = await client.search(ADAPTER, REQUEST, { timeoutMs: 2_000 });
} catch (error: unknown) {
  const name = error instanceof Error ? error.name : "error";
  fail(`request failed: ${name}`, 2);
}

const hits: ExampleHit[] = result.hits.map((hit) => ({ entry_id: hit.entry_id, result: hit.result }));
const projection = JSON.stringify({ complete: result.complete === true, hits }, null, 2) + "\n";
if (result.complete !== true || JSON.stringify(hits) !== JSON.stringify(EXPECTED_HITS)) {
  process.stdout.write(projection);
  fail("hit set does not match the checked example", 1);
}
process.stdout.write(projection);
