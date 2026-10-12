import type { KurnSearchClient, PartsCatalogSuggestRequest, SearchSuccess } from "@kurn-dev/kurn-search-client-private";

export type Status = "active" | "discontinued";
export interface Filters { readonly manufacturer?: string | readonly string[]; readonly category?: string | readonly string[]; readonly status?: Status | readonly Status[] }
export interface Request { readonly query: string; readonly limit: number; readonly partitionId?: string; readonly filters?: Filters }
export interface Part { readonly category: string; readonly id: string; readonly identifier: string; readonly manufacturer: string; readonly partition_id?: string; readonly score: number; readonly status: Status; readonly tenant_id: string }
export interface Expected { readonly ids: readonly string[]; readonly parts: readonly Part[]; readonly lists: readonly { readonly role: string; readonly name: string; readonly stamp: string }[] }

export function toRequest(value: Request): PartsCatalogSuggestRequest { return value; }
export async function search(client: KurnSearchClient, value: Request, timeoutMs: number): Promise<SearchSuccess> { return client.searchPartsCatalogSuggest(toRequest(value), {timeoutMs}); }
export function narrow(value: unknown): Part {
  if (typeof value !== "object" || value === null) throw new TypeError("invalid part");
  const p = value as Record<string, unknown>; const allowed = new Set(["category","id","identifier","manufacturer","partition_id","score","status","tenant_id"]);
  if (Object.keys(p).some(k => !allowed.has(k)) || !Number.isInteger(p.score) || (p.status !== "active" && p.status !== "discontinued")) throw new TypeError("invalid part");
  for (const k of ["category","id","identifier","manufacturer","tenant_id"] as const) if (typeof p[k] !== "string") throw new TypeError("invalid part");
  if (p.partition_id !== undefined && typeof p.partition_id !== "string") throw new TypeError("invalid part");
  return p as unknown as Part;
}
export function validate(result: SearchSuccess, expected: Expected): void {
  if (result.hits.length !== expected.ids.length || result.hits.length !== expected.parts.length || result.lists.length !== expected.lists.length) throw new Error("result length mismatch");
  for (let i = 0; i < result.hits.length; i++) {
    const actual = narrow(result.hits[i]!.result), want = expected.parts[i]!;
    if (result.hits[i]!.entry_id !== expected.ids[i] || actual.id !== result.hits[i]!.entry_id || actual.category !== want.category || actual.id !== want.id || actual.identifier !== want.identifier || actual.manufacturer !== want.manufacturer || actual.partition_id !== want.partition_id || actual.score !== want.score || actual.status !== want.status || actual.tenant_id !== want.tenant_id) throw new Error("result payload mismatch");
  }
  for (let i = 0; i < result.lists.length; i++) {
    const actual = result.lists[i]!, want = expected.lists[i]!;
    if (actual.role !== want.role || actual.name !== want.name || actual.stamp !== want.stamp) throw new Error("producing list mismatch");
  }
}
