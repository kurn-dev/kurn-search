package service

import (
	"bytes"
	"encoding/hex"
	"testing"

	"github.com/kurn-dev/kurn-search/adapter"
	"github.com/kurn-dev/kurn-search/adapters/catalogid"
	"github.com/kurn-dev/kurn-search/adapters/partscatalog"
	"github.com/kurn-dev/kurn-search/adapters/partscatalogdiscover"
	"github.com/kurn-dev/kurn-search/adapters/partscatalogfacets"
	"github.com/kurn-dev/kurn-search/adapters/partscatalogselect"
	"github.com/kurn-dev/kurn-search/adapters/partscatalogsuggest"
)

// Old-route response-byte preservation: the golden bodies below were captured
// from the delivered base commit's encoder over these exact representative
// executions. The facet envelope branch must never fire on the five delivered
// routes, so their success bytes are byte-identical to the delivered encoder.
var oldRouteGoldenBodies = map[string]string{
	"catalog-identifier":     "7b2261646170746572223a7b226e616d65223a22636174616c6f672d6964656e746966696572222c2276657273696f6e223a227631222c226265686176696f725f736861323536223a2238373166343136313638646532353861653730383362646233323537333036633931363739303738386138346332653433393862616432636537323464633265227d2c22636f6d706c657465223a747275652c2268697473223a5b7b22656e7472795f6964223a226e65757472616c2d31222c22726573756c74223a7b226964223a226e65757472616c2d31222c226964656e746966696572223a224142432d313233227d7d5d2c226c69737473223a5b7b22726f6c65223a22636174616c6f67222c226e616d65223a226c6973742d7631222c227374616d70223a226f70617175652d7374616d70227d5d2c2276657273696f6e5f617474656d7074223a312c22706f6f6c5f73746570223a312c22706f6f6c223a3132382c2274696d696e675f7573223a7b226261636b656e64223a372c2273657276696365223a392c22726573696475616c223a327d7d0a",
	"parts-catalog":          "7b2261646170746572223a7b226e616d65223a2270617274732d636174616c6f67222c2276657273696f6e223a227631222c226265686176696f725f736861323536223a2232396231653066646635656261613631343538353362646666343730646234323030663930343739343335363263636435313739336533303037306239356164227d2c22636f6d706c657465223a747275652c2268697473223a5b7b22656e7472795f6964223a226e65757472616c2d31222c22726573756c74223a7b2263617465676f7279223a2262656172696e67222c226964223a2250432d303031222c226964656e746966696572223a2241582d313030222c226d616e756661637475726572223a224e6f72746873746172222c22737461747573223a22616374697665222c2274656e616e745f6964223a2274656e616e742d61227d7d5d2c226c69737473223a5b7b22726f6c65223a22636174616c6f67222c226e616d65223a226c6973742d7631222c227374616d70223a226f70617175652d7374616d70227d5d2c2276657273696f6e5f617474656d7074223a312c22706f6f6c5f73746570223a312c22706f6f6c223a3132382c2274696d696e675f7573223a7b226261636b656e64223a372c2273657276696365223a392c22726573696475616c223a327d7d0a",
	"parts-catalog-select":   "7b2261646170746572223a7b226e616d65223a2270617274732d636174616c6f672d73656c656374222c2276657273696f6e223a227631222c226265686176696f725f736861323536223a2265643637633932303533366637653065313638623830356331353735643734316237316437376439336166343162333364313736333831383138366561303063227d2c22636f6d706c657465223a747275652c2268697473223a5b7b22656e7472795f6964223a226e65757472616c2d31222c22726573756c74223a7b2263617465676f7279223a2262656172696e67222c226964223a2250432d303031222c226964656e746966696572223a2241582d313030222c226d616e756661637475726572223a224e6f72746873746172222c22737461747573223a22616374697665222c2274656e616e745f6964223a2274656e616e742d61227d7d5d2c226c69737473223a5b7b22726f6c65223a22636174616c6f67222c226e616d65223a226c6973742d7631222c227374616d70223a226f70617175652d7374616d70227d5d2c2276657273696f6e5f617474656d7074223a312c22706f6f6c5f73746570223a312c22706f6f6c223a3132382c2274696d696e675f7573223a7b226261636b656e64223a372c2273657276696365223a392c22726573696475616c223a327d7d0a",
	"parts-catalog-suggest":  "7b2261646170746572223a7b226e616d65223a2270617274732d636174616c6f672d73756767657374222c2276657273696f6e223a227631222c226265686176696f725f736861323536223a2265663463313066313537656364616365323663306134363864343031646537626135383161613032666331356464343637616365323338336133353465653764227d2c22636f6d706c657465223a747275652c2268697473223a5b7b22656e7472795f6964223a226e65757472616c2d31222c22726573756c74223a7b2263617465676f7279223a226761736b6574222c226964223a2250432d303039222c226964656e746966696572223a2247582d323030222c226d616e756661637475726572223a2256656761222c22737461747573223a22616374697665222c2274656e616e745f6964223a2274656e616e742d61227d7d5d2c226c69737473223a5b7b22726f6c65223a22636174616c6f67222c226e616d65223a226c6973742d7631222c227374616d70223a226f70617175652d7374616d70227d5d2c2276657273696f6e5f617474656d7074223a312c22706f6f6c5f73746570223a312c22706f6f6c223a3132382c2274696d696e675f7573223a7b226261636b656e64223a372c2273657276696365223a392c22726573696475616c223a327d7d0a",
	"parts-catalog-discover": "7b2261646170746572223a7b226e616d65223a2270617274732d636174616c6f672d646973636f766572222c2276657273696f6e223a227631222c226265686176696f725f736861323536223a2230393436323533643438383136653363346334653639316665633232636464343462633137623231346661616666643036326532323734633465613963386435227d2c22636f6d706c657465223a747275652c2268697473223a5b7b22656e7472795f6964223a226e65757472616c2d31222c22726573756c74223a7b2263617465676f7279223a2262656172696e67222c226964223a2250432d303031222c226964656e746966696572223a2241582d313030222c226d616e756661637475726572223a224e6f72746873746172222c226e616d65223a22446565702067726f6f76652062616c6c2062656172696e672036323034222c22737461747573223a22616374697665222c2274656e616e745f6964223a2274656e616e742d61227d7d5d2c226c69737473223a5b7b22726f6c65223a22636174616c6f67222c226e616d65223a226c6973742d7631222c227374616d70223a226f70617175652d7374616d70227d5d2c2276657273696f6e5f617474656d7074223a312c22706f6f6c5f73746570223a312c22706f6f6c223a3132382c2274696d696e675f7573223a7b226261636b656e64223a372c2273657276696365223a392c22726573696475616c223a327d7d0a",
}

func TestDeliveredFacetRouteSuccessBytesPreserved(t *testing.T) {
	hit, err := adapter.NewResolvedHit(adapter.ResolvedHitSpec{EntryID: "neutral-1", Result: []byte(`{"category":"bearing","id":"PC-001","identifier":"AX-100","manufacturer":"Northstar","name":"Deep groove ball bearing 6204","status":"active","tenant_id":"tenant-a"}`)})
	if err != nil {
		t.Fatal(err)
	}
	resolution, err := adapter.NewResolution(adapter.ResolutionSpec{Complete: true, Hits: []adapter.ResolvedHit{hit}})
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err := partscatalogfacets.NewDescriptor(partscatalogfacets.OperationalBinding{PhysicalListName: "parts-catalog-discover-v1", ConfigSHA256: testConfig, TestedEngines: []string{"v0.5.3"}, TimeoutMillis: 1000, MaxInflight: 1, WorstCaseChargeBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	execution, err := NewExecution(ExecutionSpec{Resolution: resolution, Lists: []ProducedListSpec{{Role: "catalog", Name: "list-v1", Stamp: "opaque-stamp"}}, AdapterName: "parts-catalog-discover-facets", AdapterVersion: "v1", BehaviorSHA256: descriptor.Identities().Behavior.Hex(), VersionAttempt: 1, PoolStep: 1, Pool: 128, BackendTookUS: 7})
	if err != nil {
		t.Fatal(err)
	}
	execution.summary = validTestSummary(t, 1)
	body, err := encodeSuccess(execution, 9, 64<<20)
	if err != nil {
		t.Fatal(err)
	}
	// Captured from the delivered W7-S1 encoder over this exact execution.
	want := []byte(`{"adapter":{"name":"parts-catalog-discover-facets","version":"v1","behavior_sha256":"32fec7efb3461dc15af8c48c3bf3c560494f4d2d820d748435b6dab35f692f7f"},"complete":true,"total_matches":1,"hits":[{"entry_id":"neutral-1","result":{"category":"bearing","id":"PC-001","identifier":"AX-100","manufacturer":"Northstar","name":"Deep groove ball bearing 6204","status":"active","tenant_id":"tenant-a"}}],"facets":{"category":[{"count":1,"value":"gasket"}],"manufacturer":[{"count":1,"value":"Halden"}],"status":[{"count":1,"value":"active"}]},"lists":[{"role":"catalog","name":"list-v1","stamp":"opaque-stamp"}],"version_attempt":1,"pool_step":1,"pool":128,"timing_us":{"backend":7,"service":9,"residual":2}}
`)
	if !bytes.Equal(body, want) {
		t.Fatal("delivered facet route bytes differ")
	}
}

func TestOldRouteSuccessBytesPreserved(t *testing.T) {
	payloads := map[string][]byte{
		"catalog-identifier":     []byte(`{"id":"neutral-1","identifier":"ABC-123"}`),
		"parts-catalog":          []byte(`{"category":"bearing","id":"PC-001","identifier":"AX-100","manufacturer":"Northstar","status":"active","tenant_id":"tenant-a"}`),
		"parts-catalog-select":   []byte(`{"category":"bearing","id":"PC-001","identifier":"AX-100","manufacturer":"Northstar","status":"active","tenant_id":"tenant-a"}`),
		"parts-catalog-suggest":  []byte(`{"category":"gasket","id":"PC-009","identifier":"GX-200","manufacturer":"Vega","status":"active","tenant_id":"tenant-a"}`),
		"parts-catalog-discover": []byte(`{"category":"bearing","id":"PC-001","identifier":"AX-100","manufacturer":"Northstar","name":"Deep groove ball bearing 6204","status":"active","tenant_id":"tenant-a"}`),
	}
	for _, route := range []string{"catalog-identifier", "parts-catalog", "parts-catalog-select", "parts-catalog-suggest", "parts-catalog-discover"} {
		hit, err := adapter.NewResolvedHit(adapter.ResolvedHitSpec{EntryID: "neutral-1", Result: payloads[route]})
		if err != nil {
			t.Fatal(err)
		}
		resolution, err := adapter.NewResolution(adapter.ResolutionSpec{Complete: true, Hits: []adapter.ResolvedHit{hit}})
		if err != nil {
			t.Fatal(err)
		}
		var behavior string
		switch route {
		case "catalog-identifier":
			d, _ := catalogid.NewDescriptor(catalogid.OperationalBinding{PhysicalListName: "catalog-v1", ConfigSHA256: testConfig, TestedEngines: []string{"v0.5.3"}, TimeoutMillis: 1000, MaxInflight: 1, WorstCaseChargeBytes: 1})
			behavior = d.Identities().Behavior.Hex()
		case "parts-catalog":
			d, _ := partscatalog.NewDescriptor(partscatalog.OperationalBinding{PhysicalListName: "parts-catalog-v1", ConfigSHA256: testConfig, TestedEngines: []string{"v0.5.3"}, TimeoutMillis: 1000, MaxInflight: 1, WorstCaseChargeBytes: 1})
			behavior = d.Identities().Behavior.Hex()
		case "parts-catalog-select":
			d, _ := partscatalogselect.NewDescriptor(partscatalogselect.OperationalBinding{PhysicalListName: "parts-catalog-v1", ConfigSHA256: testConfig, TestedEngines: []string{"v0.5.3"}, TimeoutMillis: 1000, MaxInflight: 1, WorstCaseChargeBytes: 1})
			behavior = d.Identities().Behavior.Hex()
		case "parts-catalog-suggest":
			d, _ := partscatalogsuggest.NewDescriptor(partscatalogsuggest.OperationalBinding{PhysicalListName: "parts-catalog-suggest-v1", ConfigSHA256: testConfig, TestedEngines: []string{"v0.5.3"}, TimeoutMillis: 1000, MaxInflight: 1, WorstCaseChargeBytes: 1})
			behavior = d.Identities().Behavior.Hex()
		case "parts-catalog-discover":
			d, _ := partscatalogdiscover.NewDescriptor(partscatalogdiscover.OperationalBinding{PhysicalListName: "parts-catalog-discover-v1", ConfigSHA256: testConfig, TestedEngines: []string{"v0.5.3"}, TimeoutMillis: 1000, MaxInflight: 1, WorstCaseChargeBytes: 1})
			behavior = d.Identities().Behavior.Hex()
		}
		execution, err := NewExecution(ExecutionSpec{
			Resolution:  resolution,
			Lists:       []ProducedListSpec{{Role: "catalog", Name: "list-v1", Stamp: "opaque-stamp"}},
			AdapterName: route, AdapterVersion: "v1", BehaviorSHA256: behavior,
			VersionAttempt: 1, PoolStep: 1, Pool: 128, BackendTookUS: 7,
		})
		if err != nil {
			t.Fatal(err)
		}
		body, err := encodeSuccess(execution, 9, 64<<20)
		if err != nil {
			t.Fatal(err)
		}
		want, err := hex.DecodeString(oldRouteGoldenBodies[route])
		if err != nil || !bytes.Equal(body, want) {
			t.Fatalf("%s: success body differs from the delivered encoder", route)
		}
	}
}
