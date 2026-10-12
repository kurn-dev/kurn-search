import {
  ApiError, ClientError, KurnSearchClient, ProtocolError, verify,
} from "../generated/client.mjs";

const input = JSON.parse(process.env.KURN_S7_INPUT);
const request = { query: input.request.query, limit: input.request.limit };
if (input.flags?.unpaired_query) request.query = "\uD800";
if (input.request.partition_id !== undefined) request.partitionId = input.request.partition_id;
if (input.request.adapter_version !== undefined) request.adapterVersion = input.request.adapter_version;
if (input.request.filters !== undefined) request.filters = input.request.filters;
const client = new KurnSearchClient({
  endpoint: process.env.KURN_S7_ENDPOINT,
  getToken: () => {
    if (input.flags?.auth_failure) throw new Error("private-auth-detail");
    return process.env.KURN_S7_TOKEN;
  },
});
const controller = new AbortController();
if (input.flags?.pre_cancel) controller.abort("private-cancel-detail");
let output;
try {
  const value = await client.search(input.adapter, request, {
    timeoutMs: input.timeout_ms, signal: controller.signal,
  });
  output = { kind: "success", value, violations: [...verify(value)] };
} catch (error) {
  if (error instanceof ApiError) output = { kind: "api_error", ...error.value };
  else if (error instanceof ProtocolError) output = { kind: "protocol_error", status: error.status, reason: error.reason };
  else if (error instanceof ClientError) output = { kind: "client_error", reason: error.reason };
  else output = { kind: "client_error", reason: "unexpected" };
}
process.stdout.write(JSON.stringify(output));
