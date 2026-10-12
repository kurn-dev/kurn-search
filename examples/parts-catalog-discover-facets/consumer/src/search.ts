// Checked TypeScript journey for faceted parts-catalog discovery.
import { ApiError, ClientError, KurnSearchClient } from "@kurn-dev/kurn-search-client-private";
import { searchPartsDiscoverFacets } from "./parts-discover-facets.js";
import type { PartsCatalogDiscoverFacetsFilters, PartsCatalogRecord } from "./parts-discover-facets.js";

const FIXTURE_HANDLE = "client-token-a";

interface ExampleCase {
  readonly label: string;
  readonly query: string;
  readonly partitionId?: string;
  readonly filters?: PartsCatalogDiscoverFacetsFilters;
  readonly expectedIds: readonly string[];
  readonly expectedTotal: number;
  readonly expectedManufacturer: readonly (readonly [number, string])[];
}

const CASES: readonly ExampleCase[] = [
  {
    label: "exact identifier and facets",
    query: "AX-100",
    expectedIds: ["PC-001", "PC-002", "PC-003", "PC-010", "PC-011", "PC-004", "PC-008"],
    expectedTotal: 7,
    expectedManufacturer: [[4, "Northstar"], [2, "Halden"], [1, "northstar"]],
  },
  {
    label: "broad word counts are pre-limit",
    query: "gasket",
    expectedIds: ["PC-007", "PC-009", "ZB-9001", "ZB-9002", "ZB-9003", "ZB-9004", "ZB-9005", "ZB-9006", "ZB-9007", "ZB-9008", "ZB-9009", "ZB-9010", "ZB-9011", "ZB-9012", "ZB-9013", "ZB-9014", "ZB-9015", "ZB-9016", "ZB-9017", "ZB-9018", "ZB-9019", "ZB-9020", "ZB-9021", "ZB-9022", "ZB-9023"],
    expectedTotal: 141,
    expectedManufacturer: [[139, "Halden"], [2, "Vega"]],
  },
  {
    label: "counts describe the applied filter",
    query: "gasket",
    filters: { status: "discontinued" },
    expectedIds: ["PC-007", "ZB-9139"],
    expectedTotal: 2,
    expectedManufacturer: [[1, "Halden"], [1, "Vega"]],
  },
  {
    label: "zero hits stay complete",
    query: "sprocket",
    expectedIds: [],
    expectedTotal: 0,
    expectedManufacturer: [],
  },
];

function fail(message: string, code: number): never {
  process.stderr.write(`parts-discover-facets-consumer: ${message}\n`);
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
    result = await searchPartsDiscoverFacets(client, {
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
  const manufacturer = result.facets.manufacturer.map((bucket) => [bucket.count, bucket.value] as const);
  const ok = result.complete === true && shaped &&
    JSON.stringify(ids) === JSON.stringify(example.expectedIds) &&
    result.total_matches === example.expectedTotal &&
    JSON.stringify(manufacturer) === JSON.stringify(example.expectedManufacturer);
  process.stdout.write(`${ok ? "ok" : "MISMATCH"}  ${example.label}: total ${result.total_matches}, ${ids.length} hit(s)\n`);
  if (!ok) failures++;
}

try {
  await client.searchPartsCatalogDiscoverFacets({ query: "gasket", limit: 25, filters: { category: [] } }, { timeoutMs: 2_000 });
  process.stdout.write("MISMATCH  category=[]: accepted\n");
  failures++;
} catch (error: unknown) {
  if (!(error instanceof ClientError)) fail("category=[]: unexpected failure", 2);
  process.stdout.write("ok  category=[]: refused before transport\n");
}

try {
  await client.searchPartsCatalogDiscoverFacets({ query: " gasket", limit: 25 }, { timeoutMs: 2_000 });
  process.stdout.write("MISMATCH  untrimmed query: accepted\n");
  failures++;
} catch (error: unknown) {
  if (!(error instanceof ApiError) || error.value.status !== 400 || error.value.category !== "invalid_request") {
    fail("untrimmed query: unexpected failure", 2);
  }
  process.stdout.write("ok  untrimmed query: HTTP 400 invalid_request\n");
}

if (failures > 0) fail(`${failures} case(s) did not match`, 1);
