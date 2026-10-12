import * as api from "../generated/client.mjs";

const expected: string[] = [
  "ApiError", "ClientError", "KurnSearchClient", "PROTOCOL_ERROR_REASONS",
  "ProtocolError", "SEMANTIC_CHECKS", "verify",
];
const observed: string[] = Object.keys(api).sort();
if (JSON.stringify(observed) !== JSON.stringify(expected)) {
  throw new Error("generated public export inventory mismatch");
}
for (const name of expected) {
  if (api[name as keyof typeof api] === undefined) throw new Error("missing generated export");
}
