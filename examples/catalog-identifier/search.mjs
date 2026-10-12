// Example: search the checked catalog-identifier list through kurn-searchd
// with the generated TypeScript client.
//
// Run it after following docs/user-manual.md: both daemons must be up and
// KURN_SEARCHD_ADDR must hold the documented loopback listener address.
//
// The bearer value below is the checked local fixture-auth handle accepted by
// the walkthrough's fixture authenticator. It is not a credential and must not
// be reused as one.
import { KurnSearchClient } from "../../clients/typescript/generated/client.mjs";

const FIXTURE_HANDLE = "client-token";
const ADAPTER = "catalog-identifier";
const REQUEST = { query: "ABC-123", partitionId: "partition-proof", limit: 10 };
const EXPECTED_HITS = [
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

function fail(message) {
  process.stderr.write(`search.mjs: ${message}\n`);
  process.exit(2);
}

const address = process.env.KURN_SEARCHD_ADDR;
if (typeof address !== "string" || !/^127\.0\.0\.1:[0-9]{1,5}$/.test(address)) {
  fail("KURN_SEARCHD_ADDR must be the documented loopback address");
}

const client = new KurnSearchClient({
  endpoint: "http://" + address,
  getToken: () => FIXTURE_HANDLE,
});

let result;
try {
  result = await client.search(ADAPTER, REQUEST, { timeoutMs: 2_000 });
} catch (error) {
  fail(`request failed: ${error?.name ?? "error"} ${error?.message ?? ""}`.trim());
}

const hits = Array.isArray(result?.hits) ? result.hits.map((hit) => ({ entry_id: hit.entry_id, result: hit.result })) : [];
const observed = JSON.stringify(hits);
const expected = JSON.stringify(EXPECTED_HITS);
if (result?.complete !== true || observed !== expected) {
  process.stdout.write(JSON.stringify({ complete: result?.complete === true, hits }, null, 2) + "\n");
  fail("hit set does not match the checked example");
}
process.stdout.write(JSON.stringify({ complete: true, hits }, null, 2) + "\n");
