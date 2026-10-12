// Checked TypeScript journey for multi-field parts discovery.
import { ApiError, ClientError, KurnSearchClient } from "@kurn-dev/kurn-search-client-private";
import { searchPartsDiscover } from "./parts-discover.js";
import type { PartsCatalogDiscoverFilters, PartsCatalogRecord } from "./parts-discover.js";

const FIXTURE_HANDLE = "client-token-a";

interface ExampleCase {
  readonly label: string;
  readonly query: string;
  readonly partitionId?: string;
  readonly filters?: PartsCatalogDiscoverFilters;
  readonly expected: readonly string[];
}

const CASES: readonly ExampleCase[] = [
  { label: "exact identifier", query: "AX-100", expected: ["PC-001", "PC-002", "PC-003", "PC-010", "PC-011", "PC-004", "PC-008"] },
  { label: "words across the name", query: "ball bearing", expected: ["PC-001", "PC-002", "PC-003", "PC-010", "PC-011"] },
  { label: "name plus filter", query: "ball", filters: { manufacturer: "Northstar" }, expected: ["PC-001", "PC-003", "PC-010"] },
  { label: "partitioned name", query: "filter", partitionId: "south", expected: ["PC-008"] },
  { label: "one term cannot split across fields", query: "AX-bearing", expected: [] },
];

function fail(message: string, code: number): never {
  process.stderr.write(`parts-discover-consumer: ${message}\n`);
  process.exit(code);
}

const address = process.env["KURN_SEARCHD_ADDR"];
if (typeof address !== "string" || !/^127\.0\.0\.1:[0-9]{1,5}$/.test(address)) {
  fail("KURN_SEARCHD_ADDR must be the documented loopback address", 2);
}

const client = new KurnSearchClient({ endpoint: "http://" + address, getToken: () => FIXTURE_HANDLE });
let failures = 0;
for (const example of CASES) {
  let result;
  try {
    result = await searchPartsDiscover(client, {
      query: example.query,
      limit: 25,
      ...(example.partitionId === undefined ? {} : { partitionId: example.partitionId }),
      ...(example.filters === undefined ? {} : { filters: example.filters }),
    }, { timeoutMs: 2_000 });
  } catch (error: unknown) {
    fail(`${example.label}: request failed: ${error instanceof Error ? error.name : "error"}`, 2);
  }
  const ids = result.hits.map((hit) => hit.entry_id);
  const shaped = result.hits.every((hit) => {
    const record = hit.result as Partial<PartsCatalogRecord>;
    return record.id === hit.entry_id && typeof record.identifier === "string" && typeof record.name === "string";
  });
  const ok = result.complete === true && shaped && JSON.stringify(ids) === JSON.stringify(example.expected);
  process.stdout.write(`${ok ? "ok" : "MISMATCH"}  ${example.label}: ${ids.join(", ")}\n`);
  if (!ok) failures++;
}

try {
  await client.searchPartsCatalogDiscover({ query: "ball", limit: 25, filters: { category: [] } }, { timeoutMs: 2_000 });
  process.stdout.write("MISMATCH  category=[]: accepted\n");
  failures++;
} catch (error: unknown) {
  if (!(error instanceof ClientError)) fail("category=[]: unexpected failure", 2);
  process.stdout.write("ok  category=[]: refused before transport\n");
}

try {
  await client.searchPartsCatalogDiscover({ query: " ball", limit: 25 }, { timeoutMs: 2_000 });
  process.stdout.write("MISMATCH  untrimmed query: accepted\n");
  failures++;
} catch (error: unknown) {
  if (!(error instanceof ApiError) || error.value.status !== 400 || error.value.category !== "invalid_request") {
    fail("untrimmed query: unexpected failure", 2);
  }
  process.stdout.write("ok  untrimmed query: HTTP 400 invalid_request\n");
}

if (failures > 0) fail(`${failures} case(s) did not match`, 1);
