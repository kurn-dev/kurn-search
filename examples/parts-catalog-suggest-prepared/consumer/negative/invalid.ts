import type { KurnSearchClient } from "@kurn-dev/kurn-search-client-private";
import { search } from "../src/parts-suggest.js";
declare const client: KurnSearchClient;
void search(client,{query:"AX-100",limit:25,filters:{color:"blue"}},30000);
void search(client,{query:"AX-100",limit:25,filters:{category:7}},30000);
void search(client,{query:"AX-100",limit:25,filters:{status:"retired"}},30000);
