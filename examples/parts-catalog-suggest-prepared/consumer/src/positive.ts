import type { KurnSearchClient } from "@kurn-dev/kurn-search-client-private";
import { search, validate, type Expected, type Request, type Status } from "./parts-suggest.js";

declare const client: KurnSearchClient;
const statuses: readonly Status[] = ["active", "discontinued"];
const requests: readonly Request[] = [
  {query:"AX-100",limit:25,filters:{category:"bearing",status:"active"}},
  {query:"ÅB-880",limit:25,partitionId:"south",filters:{manufacturer:"M<&>  国",category:"联接<&>  件",status:statuses}},
  {query:"RY-900",limit:25,filters:{category:["relay"]}},
  {query:"AX-100Ω",limit:25,partitionId:"south",filters:{category:"bearing"}},
];
declare const expected: Expected;
for (const request of requests) void search(client, request, 30_000).then(result => validate(result, expected));
