import test from "node:test";
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import {
  ApiError, ClientError, KurnSearchClient, ProtocolError, verify,
} from "../generated/client.mjs";

const success = {
  adapter: { name: "catalog-identifier", version: "v1", behavior_sha256: "a".repeat(64) },
  complete: true,
  hits: [],
  lists: [{ role: "catalog", name: "catalog-v1", stamp: "opaque" }],
  version_attempt: 1,
  pool_step: 1,
  pool: 25,
  timing_us: { backend: 1, service: 2, residual: 1 },
};

test("canonical request, auth, and additive success", async () => {
  let call;
  const fetch = async (url, options) => {
    call = { url, options };
    return new Response(JSON.stringify({ ...success, future: { value: true } }), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    });
  };
  const client = new KurnSearchClient({ endpoint: "http://unit.invalid/prefix", getToken: () => "fixture-token", fetch });
  const result = await client.search("catalog-identifier", {
    adapterVersion: "v1", limit: 25, partitionId: "p-é", query: "AX-104\n",
  }, { timeoutMs: 1000 });
  assert.equal(call.url, "http://unit.invalid/prefix/v1/search/catalog-identifier");
  assert.equal(call.options.redirect, "manual");
  assert.equal(call.options.headers.Authorization, "Bearer fixture-token");
  assert.equal(call.options.body, "{\"adapter_version\":\"v1\",\"limit\":25,\"partition_id\":\"p-é\",\"query\":\"AX-104\\n\"}");
  assert.deepEqual(result.hits, []);
});

test("known API error and bounded hint", async () => {
  const client = new KurnSearchClient({
    endpoint: "http://unit.invalid", getToken: () => "fixture-token",
    fetch: async () => new Response('{"category":"backend_rate_limited","retry_after_seconds":2}', {
      status: 429, headers: { "Content-Type": "application/json", "Retry-After": "2" },
    }),
  });
  await assert.rejects(client.search("catalog-identifier", { query: "AX-104", limit: 1 }, { timeoutMs: 1000 }),
    (error) => error instanceof ApiError && error.value.status === 429 && error.value.retry_after_seconds === 2);
});

test("protocol reasons, token refusal, and pre-cancel are stable", async () => {
  let authCalls = 0;
  let fetchCalls = 0;
  const controller = new AbortController();
  controller.abort();
  const client = new KurnSearchClient({
    endpoint: "http://unit.invalid", getToken: () => { authCalls += 1; return "fixture-token"; },
    fetch: async () => { fetchCalls += 1; return new Response("{}", { status: 200 }); },
  });
  await assert.rejects(client.search("catalog-identifier", { query: "AX-104", limit: 1 },
    { timeoutMs: 1000, signal: controller.signal }),
    (error) => error instanceof ClientError && error.reason === "cancelled");
  assert.equal(authCalls, 0);
  assert.equal(fetchCalls, 0);

  const nonJson = new KurnSearchClient({ endpoint: "http://unit.invalid", getToken: () => "fixture-token",
    fetch: async () => new Response("opaque", { status: 500, headers: { "Content-Type": "text/plain" } }) });
  await assert.rejects(nonJson.search("catalog-identifier", { query: "AX-104", limit: 1 }, { timeoutMs: 1000 }),
    (error) => error instanceof ProtocolError && error.reason === "non_json_media" && error.status === 500);
});

test("active cancellation, auth failure, and auth deadline are bounded and redacted", async () => {
  let fetchCalls = 0;
  const controller = new AbortController();
  const active = new KurnSearchClient({
    endpoint: "http://unit.invalid", getToken: () => "fixture-token",
    fetch: async (_url, options) => {
      fetchCalls += 1;
      return new Promise((_resolve, reject) => options.signal.addEventListener("abort", reject, { once: true }));
    },
  });
  const pending = active.search("catalog-identifier", { query: "AX-104", limit: 1 },
    { timeoutMs: 1000, signal: controller.signal });
  await new Promise((resolve) => setImmediate(resolve));
  controller.abort("private-reason");
  await assert.rejects(pending,
    (error) => error instanceof ClientError && error.reason === "cancelled" && !error.message.includes("private-reason"));
  assert.equal(fetchCalls, 1);

  const readController = new AbortController();
  const duringRead = new KurnSearchClient({
    endpoint: "http://unit.invalid", getToken: () => "fixture-token",
    fetch: async () => ({
      status: 200,
      headers: new Headers({ "Content-Type": "application/json" }),
      text: () => new Promise((_resolve, reject) =>
        readController.signal.addEventListener("abort", reject, { once: true })),
    }),
  });
  const reading = duringRead.search("catalog-identifier", { query: "AX-104", limit: 1 },
    { timeoutMs: 1000, signal: readController.signal });
  await new Promise((resolve) => setImmediate(resolve));
  readController.abort();
  await assert.rejects(reading,
    (error) => error instanceof ClientError && error.reason === "cancelled");

  const authFailure = new KurnSearchClient({
    endpoint: "http://unit.invalid", getToken: () => { throw new Error("private-auth-detail"); },
    fetch: async () => { fetchCalls += 1; return new Response("{}"); },
  });
  await assert.rejects(authFailure.search("catalog-identifier", { query: "AX-104", limit: 1 }, { timeoutMs: 100 }),
    (error) => error instanceof ClientError && error.reason === "authentication_failure" &&
      !error.message.includes("private-auth-detail"));
  assert.equal(fetchCalls, 1);

  const authDeadline = new KurnSearchClient({
    endpoint: "http://unit.invalid", getToken: () => new Promise(() => {}),
    fetch: async () => { fetchCalls += 1; return new Response("{}"); },
  });
  const started = performance.now();
  await assert.rejects(authDeadline.search("catalog-identifier", { query: "AX-104", limit: 1 }, { timeoutMs: 20 }),
    (error) => error instanceof ClientError && error.reason === "deadline");
  assert.ok(performance.now() - started < 500);
  assert.equal(fetchCalls, 1);
});

test("semantic verification is opt-in and ordered", () => {
  const candidate = structuredClone(success);
  candidate.hits = [{ entry_id: "same", result: {} }, { entry_id: "same", result: {} }];
  candidate.lists = [{ role: "z", name: "z", stamp: "1" }, { role: "a", name: "a", stamp: "2" }];
  assert.deepEqual(verify(candidate), ["canonical_list_order", "duplicate_entry_id"]);
  assert.deepEqual(verify({ status: 429, category: "backend_rate_limited", retry_after_seconds: 2 },
    { retryAfter: "3" }), ["retry_hint_eligibility"]);
  assert.deepEqual(verify({ status: 400, category: "unauthenticated" }),
    ["status_category_header_coupling"]);
});

test("filter bytes preserve omission, presence, literal values and canonical order", async () => {
  const bodies = [];
  const client = new KurnSearchClient({
    endpoint: "http://unit.invalid", getToken: () => "fixture-token",
    fetch: async (_url, options) => {
      bodies.push(options.body);
      return new Response(JSON.stringify(success), { headers: { "Content-Type": "application/json" } });
    },
  });
  const request = { query: "AX-100", limit: 25, adapterVersion: "v1", partitionId: "north" };
  const baseline = '{"adapter_version":"v1","limit":25,"partition_id":"north","query":"AX-100"}';
  for (const candidate of [request, { ...request, filters: undefined }]) {
    await client.search("parts-catalog", candidate, { timeoutMs: 1000 });
    assert.equal(bodies.at(-1), baseline);
  }
  for (const filters of [{}, { category: undefined, manufacturer: undefined, status: undefined }]) {
    await client.search("parts-catalog", { ...request, filters }, { timeoutMs: 1000 });
    assert.equal(bodies.at(-1), '{"adapter_version":"v1","filters":{},"limit":25,"partition_id":"north","query":"AX-100"}');
  }
  const filters = Object.freeze({ status: "active", manufacturer: "Northstar", category: "bearing" });
  await client.search("parts-catalog", { ...request, filters }, { timeoutMs: 1000 });
  assert.equal(bodies.at(-1), '{"adapter_version":"v1","filters":{"category":"bearing","manufacturer":"Northstar","status":"active"},"limit":25,"partition_id":"north","query":"AX-100"}');
  for (const name of ["category", "manufacturer", "status"]) {
    for (const value of ["unknown", " Northstar ", "😀".repeat(512), "é\n\"\\\u0001"]) {
      await client.search("parts-catalog", { query: "AX-100", limit: 25, filters: { [name]: value } }, { timeoutMs: 1000 });
      const quoted = JSON.stringify(value).replace(/\\u([0-9a-f]{4})/g, (_all, hex) => "\\u" + hex.toUpperCase());
      assert.equal(bodies.at(-1), '{"filters":{' + JSON.stringify(name) + ':' + quoted + '},"limit":25,"query":"AX-100"}');
    }
  }
  assert.equal(bodies.length, 17);
});

test("closed filter grammar refuses before authentication and transport", async () => {
  let authCalls = 0;
  let fetchCalls = 0;
  const client = new KurnSearchClient({
    endpoint: "http://unit.invalid", getToken: () => { authCalls += 1; return "fixture-token"; },
    fetch: async () => { fetchCalls += 1; throw new Error("must not run"); },
  });
  const invalid = [null, true, 1, "active", [], new Date(0),
    { tenant_id: "tenant-a" }, { partition_id: "north" }, { unknown: undefined }];
  for (const name of ["category", "manufacturer", "status"]) {
    for (const value of [null, false, 1, [], {}, "", "😀".repeat(513), "\uD800", "\uDC00"])
      invalid.push({ [name]: value });
  }
  for (const filters of invalid) {
    await assert.rejects(client.search("parts-catalog", { query: "AX-100", limit: 25, filters }, { timeoutMs: 1000 }),
      (error) => error instanceof ClientError && error.reason === "invalid_request");
  }
  assert.equal(invalid.length, 36);
  assert.equal(authCalls, 0);
  assert.equal(fetchCalls, 0);
});

test("fixed multi-select method preserves arrays and checks every supplied member before I/O", async () => {
  let authCalls = 0;
  const calls = [];
  const client = new KurnSearchClient({ endpoint: "http://unit.invalid/prefix",
    getToken: () => { authCalls += 1; return "fixture-token"; },
    fetch: async (url, options) => {
      calls.push({ url, options });
      return new Response(JSON.stringify(success), { headers: { "Content-Type": "application/json" } });
    },
  });
  const filters = Object.freeze({ status: Object.freeze(["discontinued", "active"]),
    manufacturer: Object.freeze(["Northstar", "Halden", "Northstar"]), category: "bearing" });
  await client.searchPartsCatalogSelect({ query: "AX-100", limit: 25, partitionId: "north", adapterVersion: "v1", filters }, { timeoutMs: 1000 });
  assert.equal(calls[0].url, "http://unit.invalid/prefix/v1/search/parts-catalog-select");
  assert.equal(calls[0].options.body, '{"adapter_version":"v1","filters":{"category":"bearing","manufacturer":["Northstar","Halden","Northstar"],"status":["discontinued","active"]},"limit":25,"partition_id":"north","query":"AX-100"}');
  assert.equal(calls[0].options.redirect, "manual");
  for (const filters of [{ category: ["bearing"] }, { status: ["active", "active"] },
    { category: Array(16).fill("😀".repeat(512)), manufacturer: Array(16).fill("x"), status: ["active", "discontinued"] }]) {
    await client.searchPartsCatalogSelect({ query: "AX-100", limit: 25, filters }, { timeoutMs: 1000 });
    assert.deepEqual(JSON.parse(calls.at(-1).options.body).filters, filters);
  }
  const invalid = [null, [], true, "bearing", new Map(), new Date(0),
    { tenant_id: "tenant-a" }, { partition_id: "north" }, { unknown: undefined },
    { status: "retired" }, { status: ["active", "retired"] }, { status: ["active", "active", "active"] },
    { category: Array(17).fill("bearing") }, { manufacturer: Array(17).fill("Northstar") }];
  for (const name of ["category", "manufacturer", "status"]) {
    for (const value of [[], [null], [1], [false], [["bearing"]], [{}], [""], ["😀".repeat(513)], ["\uD800"], ["\uDC00"], Array(1), new Set(["active"]), new String("active")]) {
      invalid.push({ [name]: value });
    }
  }
  for (const filters of invalid) {
    await assert.rejects(client.searchPartsCatalogSelect({ query: "AX-100", limit: 25, filters }, { timeoutMs: 1000 }),
      error => error instanceof ClientError && error.reason === "invalid_request");
  }
  assert.equal(invalid.length, 53);
  assert.equal(authCalls, 4);
  assert.equal(calls.length, 4);
});

test("fixed suggestion method uses its own route and the bounded membership grammar", async () => {
  let authCalls = 0;
  const calls = [];
  const response = { ...success, adapter: { ...success.adapter, name: "parts-catalog-suggest" },
    hits: [{ entry_id: "PC-009", result: { id: "PC-009", identifier: "GX-200", score: 93 } }] };
  const client = new KurnSearchClient({ endpoint: "http://unit.invalid/prefix",
    getToken: () => { authCalls += 1; return "fixture-token"; },
    fetch: async (url, options) => {
      calls.push({ url, options });
      return new Response(JSON.stringify(response), { headers: { "Content-Type": "application/json" } });
    },
  });
  const result = await client.searchPartsCatalogSuggest({ query: "GX-201", limit: 25,
    filters: { category: ["gasket", "filter"], status: "active" } }, { timeoutMs: 1000 });
  assert.equal(calls[0].url, "http://unit.invalid/prefix/v1/search/parts-catalog-suggest");
  assert.equal(calls[0].options.body,
    '{"filters":{"category":["gasket","filter"],"status":"active"},"limit":25,"query":"GX-201"}');
  assert.equal(calls[0].options.redirect, "manual");
  assert.equal(result.hits[0].result.score, 93);
  for (const filters of [{ category: [] }, { status: ["active", "retired"] }]) {
    await assert.rejects(client.searchPartsCatalogSuggest({ query: "GX-201", limit: 25, filters }, { timeoutMs: 1000 }),
      error => error instanceof ClientError && error.reason === "invalid_request");
  }
  assert.equal(authCalls, 1);
  assert.equal(calls.length, 1);
});

test("fixed discovery method uses its own route and the bounded membership grammar", async () => {
  let authCalls = 0;
  const calls = [];
  const response = { ...success, adapter: { ...success.adapter, name: "parts-catalog-discover" },
    hits: [{ entry_id: "PC-001", result: { id: "PC-001", identifier: "AX-100", name: "Deep groove ball bearing 6204" } }] };
  const client = new KurnSearchClient({ endpoint: "http://unit.invalid/prefix",
    getToken: () => { authCalls += 1; return "fixture-token"; },
    fetch: async (url, options) => {
      calls.push({ url, options });
      return new Response(JSON.stringify(response), { headers: { "Content-Type": "application/json" } });
    },
  });
  const result = await client.searchPartsCatalogDiscover({ query: "ball bearing", limit: 25,
    filters: { manufacturer: ["Northstar", "Halden"], status: "active" } }, { timeoutMs: 1000 });
  assert.equal(calls[0].url, "http://unit.invalid/prefix/v1/search/parts-catalog-discover");
  assert.equal(calls[0].options.body,
    '{"filters":{"manufacturer":["Northstar","Halden"],"status":"active"},"limit":25,"query":"ball bearing"}');
  assert.equal(result.hits[0].result.name, "Deep groove ball bearing 6204");
  await assert.rejects(client.searchPartsCatalogDiscover({ query: "ball", limit: 25,
    filters: { category: [] } }, { timeoutMs: 1000 }),
  error => error instanceof ClientError && error.reason === "invalid_request");
  assert.equal(authCalls, 1);
  assert.equal(calls.length, 1);
});

test("fixed facet method returns the strict summary shape and refuses drift", async () => {
  const calls = [];
  const response = { ...success, adapter: { ...success.adapter, name: "parts-catalog-discover-facets" },
    hits: [{ entry_id: "PC-007", result: { id: "PC-007", identifier: "AX-102", name: "Flange gasket 80 mm" } }],
    total_matches: 2,
    facets: {
      category: [{ count: 2, value: "gasket" }],
      manufacturer: [{ count: 1, value: "Halden" }, { count: 1, value: "Vega" }],
      status: [{ count: 1, value: "active" }, { count: 1, value: "discontinued" }],
    } };
  const client = new KurnSearchClient({ endpoint: "http://unit.invalid/prefix",
    getToken: () => "fixture-token",
    fetch: async (url, options) => {
      calls.push({ url, options });
      return new Response(JSON.stringify(response), { headers: { "Content-Type": "application/json" } });
    },
  });
  const result = await client.searchPartsCatalogDiscoverFacets({ query: "gasket", limit: 25,
    filters: { status: ["active", "discontinued"] } }, { timeoutMs: 1000 });
  assert.equal(calls[0].url, "http://unit.invalid/prefix/v1/search/parts-catalog-discover-facets");
  assert.equal(result.total_matches, 2);
  assert.deepEqual(result.facets.manufacturer.map((bucket) => [bucket.count, bucket.value]),
    [[1, "Halden"], [1, "Vega"]]);
  const drifted = { ...response, facets: { ...response.facets, status: undefined } };
  const drifting = new KurnSearchClient({ endpoint: "http://unit.invalid",
    getToken: () => "fixture-token",
    fetch: async () => new Response(JSON.stringify(drifted), { headers: { "Content-Type": "application/json" } }),
  });
  await assert.rejects(drifting.searchPartsCatalogDiscoverFacets({ query: "gasket", limit: 25 }, { timeoutMs: 1000 }),
    error => error instanceof ProtocolError && error.reason === "shape_mismatch");
  await assert.rejects(client.searchPartsCatalogDiscoverFacets({ query: "gasket", limit: 25,
    filters: { category: [] } }, { timeoutMs: 1000 }),
    error => error instanceof ClientError && error.reason === "invalid_request");
});

test("facet ceiling refusal classifies as the existing 422 category", async () => {
  const client = new KurnSearchClient({ endpoint: "http://unit.invalid",
    getToken: () => "fixture-token",
    fetch: async () => new Response(JSON.stringify({ category: "incomplete_at_pool_ceiling" }),
      { status: 422, headers: { "Content-Type": "application/json" } }),
  });
  await assert.rejects(client.searchPartsCatalogDiscoverFacets({ query: "gasket", limit: 25 }, { timeoutMs: 1000 }),
    error => error instanceof ApiError && error.value.status === 422 && error.value.category === "incomplete_at_pool_ceiling");
});

test("interactive facet alternatives preserve request bytes, bases and strict shape", async () => {
  const calls = [];
  const response = { ...success,
    adapter: { ...success.adapter, name: "parts-catalog-discover-facet-alternatives" },
    hits: [{ entry_id: "PC-001", result: { manufacturer: "Northstar" } }],
    total_matches: 1,
    facets: {
      category: { basis_total: 1, buckets: [{ count: 1, value: "bearing" }] },
      manufacturer: { basis_total: 2, buckets: [{ count: 1, value: "Halden" }, { count: 1, value: "Northstar" }] },
      status: { basis_total: 1, buckets: [{ count: 1, value: "active" }] },
    },
  };
  const client = new KurnSearchClient({ endpoint: "http://unit.invalid/prefix",
    getToken: () => "fixture-token",
    fetch: async (url, options) => {
      calls.push({ url, options });
      return new Response(JSON.stringify(response), { headers: { "Content-Type": "application/json" } });
    },
  });
  const result = await client.searchPartsCatalogDiscoverFacetAlternatives(
    { query: "bearing", limit: 25, filters: { manufacturer: "Northstar" } }, { timeoutMs: 1000 });
  assert.equal(calls[0].url, "http://unit.invalid/prefix/v1/search/parts-catalog-discover-facet-alternatives");
  assert.equal(calls[0].options.body, '{"filters":{"manufacturer":"Northstar"},"limit":25,"query":"bearing"}');
  assert.equal(result.total_matches, 1);
  assert.equal(result.facets.manufacturer.basis_total, 2);
  const bad = { ...response, facets: { ...response.facets,
    manufacturer: { basis_total: 2, buckets: [{ count: 1, value: "Northstar" }, { count: 1, value: "Halden" }] } } };
  const drifting = new KurnSearchClient({ endpoint: "http://unit.invalid", getToken: () => "fixture-token",
    fetch: async () => new Response(JSON.stringify(bad), { headers: { "Content-Type": "application/json" } }),
  });
  await assert.rejects(drifting.searchPartsCatalogDiscoverFacetAlternatives(
    { query: "bearing", limit: 25 }, { timeoutMs: 1000 }),
  error => error instanceof ProtocolError && error.reason === "shape_mismatch");
  const surrogate = { ...response, facets: { ...response.facets,
    category: { basis_total: 1, buckets: [{ count: 1, value: "\uD800" }] } } };
  const malformedScalar = new KurnSearchClient({ endpoint: "http://unit.invalid", getToken: () => "fixture-token",
    fetch: async () => new Response(JSON.stringify(surrogate), { headers: { "Content-Type": "application/json" } }),
  });
  await assert.rejects(malformedScalar.searchPartsCatalogDiscoverFacetAlternatives(
    { query: "bearing", limit: 25 }, { timeoutMs: 1000 }),
  error => error instanceof ProtocolError && error.reason === "shape_mismatch");
  await assert.rejects(client.searchPartsCatalogDiscoverFacetAlternatives(
    { query: "bearing", limit: 25, filters: { category: [] } }, { timeoutMs: 1000 }),
  error => error instanceof ClientError && error.reason === "invalid_request");
  assert.equal(calls.length, 1);
});

test("legacy serialization and early refusals match the exact delivered S2 client", async () => {
  const oldSource = readFileSync(new URL("./fixtures/s2-client.mjs", import.meta.url));
  assert.equal(createHash("sha256").update(oldSource).digest("hex"),
    "fab508a7185ade56402cbc69c91b408b5975dd69dbd1e4b002f2a9be3da50b70");
  const old = await import("data:text/javascript;base64," + oldSource.toString("base64"));
  const oldDeclaration = readFileSync(new URL("./fixtures/s2-client.d.ts", import.meta.url), "utf8");
  assert.equal(createHash("sha256").update(oldDeclaration).digest("hex"),
    "b1b2f2cb0cf192f9f8dcb1fd8e0da0f5fc508df09cd0ec20207c557891178312");
  const currentDeclaration = readFileSync(new URL("../generated/client.d.ts", import.meta.url), "utf8");
  const requestReader = source => source.match(/export interface SearchRequest \{[\s\S]*?\n\}/)[0];
  assert.equal(requestReader(currentDeclaration), requestReader(oldDeclaration));
  const shapes = [undefined, {}, { category: "bearing" }, { status: "retired" },
    { manufacturer: "\u000b<é😀\u2028/\\\"", category: " bearing " }, null, [],
    { category: ["bearing"] }, { status: ["active", "discontinued"] }, { category: "\uD800" },
    { category: "x".repeat(513) }, { tenant_id: "tenant-a" }];
  let comparisons = 0;
  for (const route of ["parts-catalog", "catalog-identifier", "parts-catalog-select"]) {
    for (const filters of shapes) {
      const request = { query: "AX-100", limit: 25, partitionId: "north", adapterVersion: "v1", filters };
      const observe = async Client => {
        let auth = 0; const wire = [];
        const client = new Client({ endpoint: "http://unit.invalid", getToken: () => { auth++; return "fixture-token"; },
          fetch: async (url, options) => { wire.push([url, options.body]); return new Response(JSON.stringify(success), { headers: { "Content-Type": "application/json" } }); } });
        let reason;
        try { await client.search(route, request, { timeoutMs: 1000 }); } catch (error) { reason = [error.name, error.reason]; }
        return { auth, wire, reason };
      };
      assert.deepEqual(await observe(KurnSearchClient), await observe(old.KurnSearchClient));
      comparisons++;
    }
  }
  assert.equal(comparisons, 36);
});

test("all methods retain transport classifications, no retry, and opt-in verification", async () => {
  for (const method of ["legacy", "select", "suggest"]) {
    const invoke = (client, options = { timeoutMs: 1000 }) => method === "legacy"
      ? client.search("parts-catalog", { query: "AX-100", limit: 25 }, options)
      : method === "select"
        ? client.searchPartsCatalogSelect({ query: "AX-100", limit: 25 }, options)
        : client.searchPartsCatalogSuggest({ query: "AX-100", limit: 25 }, options);
    for (const [status, body, headers, expected, reason] of [
      [307, "{}", {}, ProtocolError, "redirect"],
      [503, '{"category":"backend_overloaded"}', { "Content-Type": "application/json" }, ApiError, "backend_overloaded"],
      [429, '{"category":"backend_rate_limited","retry_after_seconds":2}', { "Content-Type": "application/json", "Retry-After": "2" }, ApiError, "backend_rate_limited"],
      [200, "{", { "Content-Type": "application/json" }, ProtocolError, "malformed_json"],
    ]) {
      let calls = 0;
      const client = new KurnSearchClient({ endpoint: "http://unit.invalid", getToken: () => "fixture-token",
        fetch: async () => { calls++; return new Response(body, { status, headers }); } });
      await assert.rejects(invoke(client), e => e instanceof expected && (e.reason ?? e.value?.category) === reason);
      assert.equal(calls, 1);
    }
    let authCalls = 0; let calls = 0;
    const client = new KurnSearchClient({ endpoint: "http://unit.invalid", getToken: () => { authCalls++; return "fixture-token"; },
      fetch: async () => { calls++; return new Response(JSON.stringify({ ...success, hits: [{ entry_id: "same", result: {} }, { entry_id: "same", result: {} }] }), { headers: { "Content-Type": "application/json" } }); } });
    const result = await invoke(client);
    assert.deepEqual(verify(result), ["duplicate_entry_id"]);
    const controller = new AbortController(); controller.abort();
    await assert.rejects(invoke(client, { timeoutMs: 1000, signal: controller.signal }), e => e instanceof ClientError && e.reason === "cancelled");
    await assert.rejects(invoke(client, { timeoutMs: 0 }), e => e instanceof ClientError && e.reason === "invalid_timeout");
    assert.equal(authCalls, 1); assert.equal(calls, 1);
    const deadline = new KurnSearchClient({ endpoint: "http://unit.invalid", getToken: () => new Promise(() => {}), fetch: async () => { throw new Error("unexpected I/O"); } });
    await assert.rejects(invoke(deadline, { timeoutMs: 10 }), e => e instanceof ClientError && e.reason === "deadline");
  }
});
