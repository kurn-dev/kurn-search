// Separate-project checked journey over the shared 152-row example list.
import { ApiError, ClientError, KurnSearchClient } from "@kurn-dev/kurn-search-client-private";
import type { PartsCatalogDiscoverFacetAlternativesRequest, SearchFacetAlternativesSuccess } from "@kurn-dev/kurn-search-client-private";

const address = process.env["KURN_SEARCHD_ADDR"];
if (typeof address !== "string" || !/^127\.0\.0\.1:[0-9]{1,5}$/.test(address)) {
  process.stderr.write("facet-alternatives: set KURN_SEARCHD_ADDR to the local loopback listener\n");
  process.exit(2);
}
const client = new KurnSearchClient({ endpoint: "http://" + address, getToken: () => "client-token-a" });
const cases: readonly {
  readonly label: string;
  readonly request: PartsCatalogDiscoverFacetAlternativesRequest;
  readonly ids: readonly string[];
  readonly total: number;
  readonly manufacturerBase: number;
  readonly manufacturer: readonly (readonly [number, string])[];
}[] = [
  { label: "selected Northstar", request: { query: "bearing", limit: 25, filters: { manufacturer: "Northstar" } },
    ids: ["PC-001", "PC-003", "PC-010", "PC-006", "PC-012"], total: 5, manufacturerBase: 7,
    manufacturer: [[5, "Northstar"], [1, "Halden"], [1, "northstar"]] },
  { label: "unavailable selected Vega", request: { query: "bearing", limit: 25, filters: { manufacturer: "Vega" } },
    ids: [], total: 0, manufacturerBase: 7,
    manufacturer: [[5, "Northstar"], [1, "Halden"], [1, "northstar"]] },
  { label: "status alternatives", request: { query: "gasket", limit: 25, filters: { status: "discontinued" } },
    ids: ["PC-007", "ZB-9139"], total: 2, manufacturerBase: 2,
    manufacturer: [[1, "Halden"], [1, "Vega"]] },
];

let failures = 0;
for (const row of cases) {
  let result: SearchFacetAlternativesSuccess;
  try {
    result = await client.searchPartsCatalogDiscoverFacetAlternatives(row.request, { timeoutMs: 2_000 });
  } catch (error: unknown) {
    process.stderr.write(`${row.label}: ${error instanceof Error ? error.name : "request failed"}\n`);
    process.exit(2);
  }
  const ids = result.hits.map(hit => hit.entry_id);
  const manufacturer = result.facets.manufacturer.buckets.map(bucket => [bucket.count, bucket.value] as const);
  const ok = result.complete && result.total_matches === row.total &&
    result.facets.manufacturer.basis_total === row.manufacturerBase &&
    JSON.stringify(ids) === JSON.stringify(row.ids) &&
    JSON.stringify(manufacturer) === JSON.stringify(row.manufacturer);
  process.stdout.write(`${ok ? "ok" : "MISMATCH"}  ${row.label}\n`);
  if (!ok) failures++;
}
try {
  await client.searchPartsCatalogDiscoverFacetAlternatives({ query: "bearing", limit: 25, filters: { category: [] } }, { timeoutMs: 2_000 });
  failures++;
} catch (error: unknown) {
  if (!(error instanceof ClientError)) failures++;
}
try {
  await client.searchPartsCatalogDiscoverFacetAlternatives({ query: " bearing", limit: 25 }, { timeoutMs: 2_000 });
  failures++;
} catch (error: unknown) {
  if (!(error instanceof ApiError) || error.value.status !== 400 || error.value.category !== "invalid_request") failures++;
}
if (failures) process.exit(1);
