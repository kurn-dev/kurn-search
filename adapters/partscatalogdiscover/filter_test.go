package partscatalogdiscover

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

var testScope = []byte(`{"tenant_id":"tenant-a"}`)

func requestJSON(t *testing.T, filters any) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"query": "AX-100", "filters": filters})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestMembershipCanonicalCases(t *testing.T) {
	cases := []struct{ input, want string }{
		{`{"query":"AX-100"}`, `{"tenant_id":"tenant-a"}`},
		{`{"query":"AX-100","filters":{}}`, `{"tenant_id":"tenant-a"}`},
		{`{"query":"AX-100","filters":{"manufacturer":"Northstar"}}`, `{"manufacturer":"Northstar","tenant_id":"tenant-a"}`},
		{`{"query":"AX-100","filters":{"manufacturer":["Northstar","Northstar"]}}`, `{"manufacturer":"Northstar","tenant_id":"tenant-a"}`},
		{`{"query":"AX-100","partition_id":"north","filters":{"manufacturer":["Northstar","Halden"],"status":["discontinued","active"]}}`, `{"manufacturer":{"in":["Halden","Northstar"]},"partition_id":"north","status":{"in":["active","discontinued"]},"tenant_id":"tenant-a"}`},
		{`{"query":"AX-100","filters":{"category":["\ud83d\ude00"," x "]}}`, "{\"category\":{\"in\":[\" x \",\"😀\"]},\"tenant_id\":\"tenant-a\"}"},
	}
	for i, c := range cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			plan, err := New().CompileMembershipFilter([]byte(c.input), testScope)
			if err != nil || string(plan.CanonicalJSON()) != c.want {
				t.Fatalf("canonical: %s %v", plan.CanonicalJSON(), err)
			}
			if _, err := New().Compile([]byte(c.input), testScope); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestMembershipRequestRefusals(t *testing.T) {
	invalid := []string{
		`{"category":[]}`, `{"category":null}`, `null`, `{"category":1}`, `{"category":false}`,
		`{"category":["x",null]}`, `{"category":["x",true]}`, `{"category":["x",2]}`,
		`{"category":[["x"]]}`, `{"category":{"in":["x"]}}`, `{"category":[{"eq":"x"}]}`,
		`{"category":""}`, `{"category":[""]}`, `{"category":["x","\ud800"]}`,
		`{"category":["\udc00","x"]}`, `{"category":"\ud800"}`,
		`{"category":"bearing","category":["bearing"]}`, `{"category":"bearing","\u0063ategory":["filter"]}`,
		`{"tenant_id":["tenant-b"]}`, `{"partition_id":["north"]}`, `{"other":"x"}`, `[]`,
		`{"status":"retired"}`, `{"status":["active","retired"]}`, `{"status":["active","active","active"]}`,
	}
	for _, field := range []string{"category", "manufacturer"} {
		raw, _ := json.Marshal(map[string]any{field: strings.Repeat("x", 513)})
		invalid = append(invalid, string(raw))
		raw, _ = json.Marshal(map[string]any{field: []string{strings.Repeat("😀", 513)}})
		invalid = append(invalid, string(raw))
		values := make([]string, 17)
		for i := range values {
			values[i] = "bearing"
		}
		raw, _ = json.Marshal(map[string]any{field: values})
		invalid = append(invalid, string(raw))
	}
	requests := [][]byte{}
	for _, value := range invalid {
		requests = append(requests, []byte(`{"query":"AX-100","filters":`+value+`}`))
	}
	for _, value := range []string{
		`{}`, `{"query":""}`, `{"query":" "}`, `{"query":"AX-100","limit":0}`,
		`{"query":"AX-100","limit":1001}`, `{"query":"AX-100","limit":1.5}`,
		`{"query":"AX-100","partition_id":["north"]}`, `{"query":"AX-100","unknown":1}`,
		`{"query":"AX-100","query":"AX-100"}`, `{"query":"AX-100","adapter_version":"v2"}`,
	} {
		requests = append(requests, []byte(value))
	}
	raw, _ := json.Marshal(map[string]any{"query": strings.Repeat("x", 257)})
	requests = append(requests, raw)
	requests = append(requests, []byte("{\"query\":\"AX-100\",\"filters\":{\"category\":\""+string([]byte{0xff})+"\"}}"))
	for i, raw := range requests {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			if _, err := New().CompileMembershipFilter(raw, testScope); err == nil {
				t.Fatal("filter accepted invalid request")
			}
			if _, err := New().Compile(raw, testScope); err == nil {
				t.Fatal("compile accepted invalid request")
			}
		})
	}
	valid := []byte(`{"query":"AX-100"}`)
	for _, scope := range [][]byte{nil, []byte(`{}`), []byte(`{"tenant_id":["tenant-a"]}`), []byte(`{"tenant_id":"\ud800"}`), append([]byte(strings.Repeat(" ", maxTrustedScopeBytes)), testScope...)} {
		if _, err := New().Compile(valid, scope); err == nil {
			t.Fatal("invalid scope accepted")
		}
	}
	if _, err := New().Compile(append([]byte(strings.Repeat(" ", maxRequestBytes)), valid...), testScope); err == nil {
		t.Fatal("envelope accepted")
	}
}

func TestMembershipScopedCanonicalCap(t *testing.T) {
	category, manufacturer := make([]string, 16), make([]string, 16)
	for i := range category {
		category[i] = fmt.Sprintf("c%02d", i) + strings.Repeat("x", 509)
		manufacturer[i] = fmt.Sprintf("m%02d", i) + strings.Repeat("x", 509)
	}
	manufacturer[15] = "m15" + strings.Repeat("x", 345)
	filters := map[string]any{"category": category, "manufacturer": manufacturer}
	raw := requestJSON(t, filters)
	plan, err := New().CompileMembershipFilter(raw, testScope)
	if err != nil || len(plan.CanonicalJSON()) != 16384 {
		t.Fatalf("at cap %d %v", len(plan.CanonicalJSON()), err)
	}
	if _, err := New().Compile(raw, testScope); err != nil {
		t.Fatal(err)
	}
	withPartition, _ := json.Marshal(map[string]any{"query": "AX-100", "filters": filters, "partition_id": "north"})
	if _, err := New().Compile(withPartition, testScope); err == nil {
		t.Fatal("partition not charged")
	}
	manufacturer[15] += "x"
	if _, err := New().Compile(requestJSON(t, filters), testScope); err == nil {
		t.Fatal("cap+1 accepted")
	}
	for _, value := range []string{strings.Repeat("x", 512), strings.Repeat("é", 512), strings.Repeat("😀", 512), strings.Repeat("\x00", 512)} {
		if _, err := New().Compile(requestJSON(t, map[string]any{"category": []string{value}}), testScope); err != nil {
			t.Fatal("rune boundary", err)
		}
	}
}
