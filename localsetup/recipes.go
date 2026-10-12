package localsetup

// recipe is closed local-example authority, not a caller-defined ingestion or
// deployment contract. The parts values were measured from two byte-identical
// public kurn builds of the checked 152-record feed and mapping (1952 keys).
// ConfigSHA256 is the engine's canonical configuration digest; it is not the
// SHA-256 of the newline-terminated config.json file.
type recipe struct {
	name             string
	physicalListName string
	configSHA256     string
	analyzerSHA256   string
	mode             string
	bundleHashes     map[string]string
	entries          int
	keys             int
}

func catalogRecipe() recipe {
	return recipe{
		name: "catalog-identifier", physicalListName: PhysicalListName,
		configSHA256: ConfigSHA256, analyzerSHA256: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		mode: "exact", bundleHashes: bundleHashes, entries: 1, keys: 1,
	}
}

func recipeForName(name string) (recipe, bool) {
	switch name {
	case "", "catalog-identifier":
		return catalogRecipe(), true
	case "parts-catalog", "parts-catalog-select":
		return recipe{
			name: name, physicalListName: "parts-catalog-v1",
			configSHA256:   "49d8551b347da49135c49b13eed03d84a322a6496d34bf53428ab47ca3ee221d",
			analyzerSHA256: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			mode:           "exact",
			bundleHashes: map[string]string{
				"config.json": "ae4ea431813ec4ba91f3485ae7f4d566b5ed05b8b75c9582fb3695977e866075",
				"base.jsonl":  "4303428c41e619f339b276e3a97375f4861a3f7eedc4f0fcc2f946551b452b84",
				"base.idx":    "29263436b1bf67807ccd9cd2cfb4ca88c1a9da318ff425fe1a43a7bbc59e32b0",
			},
			entries: 152, keys: 1952,
		}, true
	case "parts-catalog-suggest":
		return recipe{
			name: name, physicalListName: "parts-catalog-suggest-v1",
			configSHA256:   "5c77cc033a580ee8f654fc6def1559713d527f2d3466006ea30735afc7eaecfc",
			analyzerSHA256: "ba7fe7f6aaaef24d85e2db7ede33cc3c3009924863585953eb0fd3648f221bb1",
			mode:           "ngram",
			bundleHashes: map[string]string{
				"config.json": "545f5b64bdff8509061e16e100d63c1c6f543ca98f36ff5d939d66803dfd2e1d",
				"base.jsonl":  "2fa285ad8459c8072b9631adf9572603000ef1462c7fa4e9e22bcb0856c510b0",
				"base.idx":    "6676c1484bad2b4f6039eda5335c58bf1c7c57dd21fc2fb802a9036ac3226e40",
			},
			entries: 152, keys: 152,
		}, true
	case "parts-catalog-discover":
		return recipe{
			name: name, physicalListName: "parts-catalog-discover-v1",
			configSHA256:   "49d8551b347da49135c49b13eed03d84a322a6496d34bf53428ab47ca3ee221d",
			analyzerSHA256: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			mode:           "exact",
			bundleHashes: map[string]string{
				"config.json": "ae4ea431813ec4ba91f3485ae7f4d566b5ed05b8b75c9582fb3695977e866075",
				"base.jsonl":  "7b57c6f47a8b6d8012c740f814afd259401ccd9b0f95cd7f92b144262d9ba788",
				"base.idx":    "f32b8d60bb12e7cb2fbc46790833906f23d77d3c0b2c3c5eaef6af841aa68ebc",
			},
			entries: 152, keys: 3693,
		}, true
	case "parts-catalog-discover-facets", "parts-catalog-discover-facet-alternatives":
		// Both facet routes reuse the delivered parts-catalog-discover-v1
		// physical list byte-for-byte; only service resolution differs.
		return recipe{
			name: name, physicalListName: "parts-catalog-discover-v1",
			configSHA256:   "49d8551b347da49135c49b13eed03d84a322a6496d34bf53428ab47ca3ee221d",
			analyzerSHA256: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			mode:           "exact",
			bundleHashes: map[string]string{
				"config.json": "ae4ea431813ec4ba91f3485ae7f4d566b5ed05b8b75c9582fb3695977e866075",
				"base.jsonl":  "7b57c6f47a8b6d8012c740f814afd259401ccd9b0f95cd7f92b144262d9ba788",
				"base.idx":    "f32b8d60bb12e7cb2fbc46790833906f23d77d3c0b2c3c5eaef6af841aa68ebc",
			},
			entries: 152, keys: 3693,
		}, true
	default:
		return recipe{}, false
	}
}

func (r recipe) fixtureFiles() map[string][]byte {
	if r.name == "catalog-identifier" {
		return fixtureFiles()
	}
	return map[string][]byte{
		"backend-token":     []byte("unused-local-backend-token"),
		"fixture-auth.json": []byte("{\"schema\":\"kurn-search-fixture-auth/v1\",\"credentials\":[{\"handle\":\"client-token-a\",\"tenant\":\"tenant-a\"},{\"handle\":\"client-token-b\",\"tenant\":\"tenant-b\"}]}\n"),
		"readiness.json":    []byte("{\"schema\":\"kurn-search-readiness/v1\",\"goldens\":[{\"name\":\"example-ready\",\"route\":\"" + r.name + "\",\"auth_handle\":\"client-token-a\",\"sentinel_tenant\":\"tenant-a\",\"query\":\"GX-200\",\"limit\":25,\"expected_ids\":[\"PC-009\"],\"expected_complete\":true}]}\n"),
	}
}
