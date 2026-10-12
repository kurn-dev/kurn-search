// Separate TypeScript consumer of the parts-catalog-suggest recipe.
//
// The program uses only the generated fixed-route method. It validates the
// ordered IDs, Kurn gram-coverage scores and complete canonical result objects.
import { KurnSearchClient } from "@kurn-dev/kurn-search-client-private";
import type { SearchSuccess } from "@kurn-dev/kurn-search-client-private";
import { searchPartsSuggest } from "./parts-suggest.js";
import type { PartsCatalogRecord, PartsCatalogSuggestFilters } from "./parts-suggest.js";

const FIXTURE_HANDLE = "client-token-a";
const LIMIT = 25;

const RECORDS: Readonly<Record<string, PartsCatalogRecord>> = {
  "PC-001": { id: "PC-001", identifier: "AX-100", manufacturer: "Northstar", category: "bearing", status: "active", tenant_id: "tenant-a", partition_id: "north" },
  "PC-002": { id: "PC-002", identifier: "AX-100", manufacturer: "Halden", category: "bearing", status: "active", tenant_id: "tenant-a", partition_id: "north" },
  "PC-003": { id: "PC-003", identifier: "AX-100", manufacturer: "Northstar", category: "bearing", status: "discontinued", tenant_id: "tenant-a", partition_id: "south" },
  "PC-004": { id: "PC-004", identifier: "AX-100", manufacturer: "Northstar", category: "filter", status: "active", tenant_id: "tenant-a", partition_id: "north" },
  "PC-006": { id: "PC-006", identifier: "AX-101", manufacturer: "Northstar", category: "bearing", status: "active", tenant_id: "tenant-a", partition_id: "north" },
  "PC-007": { id: "PC-007", identifier: "AX-102", manufacturer: "Vega", category: "gasket", status: "discontinued", tenant_id: "tenant-a", partition_id: "south" },
  "PC-008": { id: "PC-008", identifier: "BX-100", manufacturer: "Halden", category: "filter", status: "active", tenant_id: "tenant-a", partition_id: "south" },
  "PC-009": { id: "PC-009", identifier: "GX-200", manufacturer: "Vega", category: "gasket", status: "active", tenant_id: "tenant-a" },
  "PC-010": { id: "PC-010", identifier: "AX-100", manufacturer: "Northstar", category: "bearing", status: "active", tenant_id: "tenant-a" },
  "PC-011": { id: "PC-011", identifier: "AX-100", manufacturer: "northstar", category: "bearing", status: "active", tenant_id: "tenant-a", partition_id: "north" },
  "PC-012": { id: "PC-012", identifier: "AX-103", manufacturer: "Northstar", category: "Bearing", status: "active", tenant_id: "tenant-a", partition_id: "north" },
};

interface ExpectedHit { readonly id: string; readonly score: number }
interface ExampleCase {
  readonly name: string;
  readonly query: string;
  readonly filters?: PartsCatalogSuggestFilters;
  readonly expected: readonly ExpectedHit[];
}

const hits = (score: number, ...ids: string[]): readonly ExpectedHit[] => ids.map((id) => ({ id, score }));
const CASES: readonly ExampleCase[] = [
  { name: "exact", query: "GX-200", expected: hits(100, "PC-009") },
  { name: "similar", query: "GX-201", expected: hits(93, "PC-009") },
  { name: "unknown query gram is excluded", query: "AX-1O0", expected: hits(100, "PC-001", "PC-002", "PC-003", "PC-004", "PC-010", "PC-011", "PC-006", "PC-007", "PC-012") },
  { name: "literal scalar manufacturer", query: "AX-100", filters: { manufacturer: "Northstar" }, expected: [...hits(100, "PC-001", "PC-003", "PC-004", "PC-010"), ...hits(78, "PC-006", "PC-012")] },
  { name: "multi-select category", query: "AX-100", filters: { category: ["filter", "gasket"] }, expected: [{ id: "PC-004", score: 100 }, { id: "PC-007", score: 78 }, { id: "PC-008", score: 76 }] },
  { name: "case-sensitive manufacturer", query: "AX-100", filters: { manufacturer: "northstar" }, expected: hits(100, "PC-011") },
  { name: "complete empty result", query: "Z", expected: [] },
];

function fail(message: string, code: number): never {
  process.stderr.write(`parts-suggest-consumer: ${message}\n`);
  process.exit(code);
}

function canonical(value: unknown): string {
  if (Array.isArray(value)) return "[" + value.map(canonical).join(",") + "]";
  if (value !== null && typeof value === "object") {
    const record = value as Record<string, unknown>;
    return "{" + Object.keys(record).sort().map((key) => JSON.stringify(key) + ":" + canonical(record[key])).join(",") + "}";
  }
  return JSON.stringify(value);
}

const address = process.env["KURN_SEARCHD_ADDR"];
if (typeof address !== "string" || !/^127\.0\.0\.1:[0-9]{1,5}$/.test(address)) {
  fail("KURN_SEARCHD_ADDR must be the documented loopback address", 2);
}

const client = new KurnSearchClient({ endpoint: "http://" + address, getToken: () => FIXTURE_HANDLE });
let failures = 0;
for (const example of CASES) {
  let result: SearchSuccess;
  try {
    result = await searchPartsSuggest(client, { query: example.query, limit: LIMIT,
      ...(example.filters === undefined ? {} : { filters: example.filters }) }, { timeoutMs: 2_000 });
  } catch (error: unknown) {
    fail(`${example.name}: request failed: ${error instanceof Error ? error.name : "error"}`, 2);
  }
  const observed = result.hits.map((hit) => {
    const value = hit.result as { score?: unknown };
    return { id: hit.entry_id, score: value.score };
  });
  const valuesMatch = result.hits.every((hit) => {
    const record = RECORDS[hit.entry_id];
    const score = (hit.result as { score?: unknown }).score;
    return record !== undefined && Number.isInteger(score) && canonical(hit.result) === canonical({ ...record, score });
  });
  const ok = result.complete === true && canonical(observed) === canonical(example.expected) && valuesMatch;
  process.stdout.write(`${ok ? "ok" : "MISMATCH"}  ${example.name}: ${observed.map((item) => `${item.id}:${String(item.score)}`).join(", ")}\n`);
  if (!ok) failures++;
}
if (failures > 0) fail(`${failures} case(s) did not match the checked expectation`, 1);
