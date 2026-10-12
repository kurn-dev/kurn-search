// Separate TypeScript consumer of the parts-catalog-select recipe.
//
// Compiled from a copy placed outside the source tree after the documented
// offline local install of `@kurn-dev/kurn-search-client-private`. It accepts
// only the documented loopback address, authenticates with the checked local
// fixture handle for tenant-a (not a credential), runs the documented request
// set through the fixed-route method and exits 0 only when every answer is
// complete and equals the checked expectation: the same identifiers in the
// same order with the same payloads.
import { ClientError, KurnSearchClient } from "@kurn-dev/kurn-search-client-private";
import type { SearchSuccess } from "@kurn-dev/kurn-search-client-private";
import { searchPartsSelect } from "./parts-select.js";
import type { PartsCatalogRecord, PartsCatalogSelectFilters, PartsCatalogStatus } from "./parts-select.js";

const FIXTURE_HANDLE = "client-token-a";
const QUERY = "AX-100";
const LIMIT = 25;

// The checked core records reachable by the AX-100 request set (tenant-a).
const RECORDS: Readonly<Record<string, PartsCatalogRecord>> = {
  "PC-001": { id: "PC-001", identifier: "AX-100", manufacturer: "Northstar", category: "bearing", status: "active", tenant_id: "tenant-a", partition_id: "north" },
  "PC-002": { id: "PC-002", identifier: "AX-100", manufacturer: "Halden", category: "bearing", status: "active", tenant_id: "tenant-a", partition_id: "north" },
  "PC-003": { id: "PC-003", identifier: "AX-100", manufacturer: "Northstar", category: "bearing", status: "discontinued", tenant_id: "tenant-a", partition_id: "south" },
  "PC-004": { id: "PC-004", identifier: "AX-100", manufacturer: "Northstar", category: "filter", status: "active", tenant_id: "tenant-a", partition_id: "north" },
  "PC-006": { id: "PC-006", identifier: "AX-101", manufacturer: "Northstar", category: "bearing", status: "active", tenant_id: "tenant-a", partition_id: "north" },
  "PC-007": { id: "PC-007", identifier: "AX-102", manufacturer: "Vega", category: "gasket", status: "discontinued", tenant_id: "tenant-a", partition_id: "south" },
  "PC-008": { id: "PC-008", identifier: "BX-100", manufacturer: "Halden", category: "filter", status: "active", tenant_id: "tenant-a", partition_id: "south" },
  "PC-010": { id: "PC-010", identifier: "AX-100", manufacturer: "Northstar", category: "bearing", status: "active", tenant_id: "tenant-a" },
  "PC-011": { id: "PC-011", identifier: "AX-100", manufacturer: "northstar", category: "bearing", status: "active", tenant_id: "tenant-a", partition_id: "north" },
  "PC-012": { id: "PC-012", identifier: "AX-103", manufacturer: "Northstar", category: "Bearing", status: "active", tenant_id: "tenant-a", partition_id: "north" },
};

const UNFILTERED: readonly string[] = ["PC-001", "PC-002", "PC-003", "PC-004", "PC-010", "PC-011", "PC-006", "PC-007", "PC-008", "PC-012"];

interface ExampleCase {
  readonly name: string;
  readonly filters?: PartsCatalogSelectFilters;
  readonly expected: readonly string[];
}

// Arrays are sets: member order and duplicates do not change an answer, and a
// one-member array means the same as the scalar string.
const CASES: readonly ExampleCase[] = [
  { name: "no filters", expected: UNFILTERED },
  { name: "category=[bearing,filter] status=active", filters: { category: ["bearing", "filter"], status: "active" }, expected: ["PC-001", "PC-002", "PC-004", "PC-010", "PC-011", "PC-006", "PC-008"] },
  { name: "manufacturer=[Northstar,Halden] category=bearing", filters: { manufacturer: ["Northstar", "Halden"], category: "bearing" }, expected: ["PC-001", "PC-002", "PC-003", "PC-010", "PC-006"] },
  { name: "manufacturer=[Halden,Northstar] category=bearing (permuted)", filters: { manufacturer: ["Halden", "Northstar"], category: "bearing" }, expected: ["PC-001", "PC-002", "PC-003", "PC-010", "PC-006"] },
  { name: "manufacturer=[Northstar,Vega]", filters: { manufacturer: ["Northstar", "Vega"] }, expected: ["PC-001", "PC-003", "PC-004", "PC-010", "PC-006", "PC-007", "PC-012"] },
  { name: "category=[bearing,Bearing]", filters: { category: ["bearing", "Bearing"] }, expected: ["PC-001", "PC-002", "PC-003", "PC-010", "PC-011", "PC-006", "PC-012"] },
  { name: "manufacturer=[Northstar,Northstar] (duplicate = scalar)", filters: { manufacturer: ["Northstar", "Northstar"] }, expected: ["PC-001", "PC-003", "PC-004", "PC-010", "PC-006", "PC-012"] },
  { name: "status=[active,discontinued] (full set)", filters: { status: ["active", "discontinued"] }, expected: UNFILTERED },
  { name: "manufacturer=[Unknown,Other]", filters: { manufacturer: ["Unknown", "Other"] }, expected: [] },
];

function fail(message: string, code: number): never {
  process.stderr.write(`parts-select-consumer: ${message}\n`);
  process.exit(code);
}

function canonical(value: unknown): string {
  if (Array.isArray(value)) {
    return "[" + value.map(canonical).join(",") + "]";
  }
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

const client = new KurnSearchClient({
  endpoint: "http://" + address,
  getToken: () => FIXTURE_HANDLE,
});

let failures = 0;
for (const example of CASES) {
  let result: SearchSuccess;
  try {
    result = await searchPartsSelect(client, { query: QUERY, limit: LIMIT, ...(example.filters === undefined ? {} : { filters: example.filters }) }, { timeoutMs: 2_000 });
  } catch (error: unknown) {
    const name = error instanceof Error ? error.name : "error";
    fail(`${example.name}: request failed: ${name}`, 2);
  }
  const ids = result.hits.map((hit) => hit.entry_id);
  const idsMatch = result.complete === true && JSON.stringify(ids) === JSON.stringify(example.expected);
  const payloadsMatch = result.hits.every((hit) => {
    const record = RECORDS[hit.entry_id];
    return record !== undefined && canonical(hit.result) === canonical(record);
  });
  const status = idsMatch && payloadsMatch ? "ok" : "MISMATCH";
  process.stdout.write(`${status}  ${example.name}: ${ids.length} hit(s) [${ids.join(", ")}]\n`);
  if (status !== "ok") {
    failures++;
  }
}

// An empty array never means "no restriction". TypeScript cannot reject it
// (any readonly string[] is admitted), so the client refuses it at runtime
// before asking for a token or opening a connection.
try {
  await searchPartsSelect(client, { query: QUERY, limit: LIMIT, filters: { category: [] } }, { timeoutMs: 2_000 });
  process.stdout.write("MISMATCH  category=[]: accepted\n");
  failures++;
} catch (error: unknown) {
  if (error instanceof ClientError) {
    process.stdout.write("ok  category=[]: refused by the client before transport\n");
  } else {
    const name = error instanceof Error ? error.name : "error";
    fail(`category=[]: unexpected failure: ${name}`, 2);
  }
}

// The recipe's closed status vocabulary is enforced twice: the fixed-route
// client refuses a member outside it before asking for a token or opening a
// connection, and the service would refuse it with HTTP 400 invalid_request
// if it ever arrived (the manual's raw curl step shows that server refusal).
// The cast is deliberate; the wrapper type rejects it at compile time.
try {
  await searchPartsSelect(client, { query: QUERY, limit: LIMIT, filters: { status: ["active", "retired" as PartsCatalogStatus] } }, { timeoutMs: 2_000 });
  process.stdout.write("MISMATCH  status=[active,retired]: accepted\n");
  failures++;
} catch (error: unknown) {
  if (error instanceof ClientError) {
    process.stdout.write("ok  status=[active,retired]: refused by the client before transport\n");
  } else {
    const name = error instanceof Error ? error.name : "error";
    fail(`status=[active,retired]: unexpected failure: ${name}`, 2);
  }
}

if (failures > 0) {
  fail(`${failures} case(s) did not match the checked expectation`, 1);
}
