// Package localsetup prepares only the checked neutral local example. It does
// not start processes or provide production configuration or authentication.
package localsetup

import (
	"encoding/json"
	"errors"
	"path/filepath"

	"github.com/kurn-dev/kurn-search/fleet"
	"github.com/kurn-dev/kurn-search/internal/localproof"
)

const (
	ResultSchema      = "kurn-search-local-prepare-result/v1"
	ConfigSHA256      = "ffd99df8acdeb32535e52dd60bfcf02f8bfa1f0968b2895ba7a48a4c1fb887f6"
	PhysicalListName  = fleet.PhysicalListName
	maxManifestBytes  = 64 << 10
	maxDataBytes      = 64 << 20
	reservationSuffix = ".local-setup"
)

var errRefused = errors.New("kurn-search-local: refused")

type Options struct {
	Bundle         string
	Out            string
	KurndAddress   string
	SearchdAddress string
	Recipe         string
}

// Result has lexical field order and contains no private path or listener.
// Directories counts descendants, excluding the prepared root.
type Result struct {
	ConfigSHA256     string `json:"config_sha256"`
	Directories      int    `json:"directories"`
	Files            int    `json:"files"`
	PhysicalListName string `json:"physical_list_name"`
	Schema           string `json:"schema"`
	Status           string `json:"status"`
}

type rateDocument struct {
	Unlimited     bool `json:"unlimited"`
	RatePerSecond int  `json:"rate_per_sec"`
}

type adapterDocument struct {
	Name                 string `json:"name"`
	PhysicalListName     string `json:"physical_list_name"`
	ConfigSHA256         string `json:"config_sha256"`
	TimeoutMillis        int    `json:"timeout_millis"`
	MaxInflight          int    `json:"max_inflight"`
	WorstCaseChargeBytes int64  `json:"worst_case_charge_bytes"`
}

// Field order intentionally matches the unchanged full manual's JSON block.
type configDocument struct {
	Schema                   string          `json:"schema"`
	ListenAddress            string          `json:"listen_address"`
	RuntimeStatusFile        string          `json:"runtime_status_file"`
	KurnEndpoint             string          `json:"kurn_endpoint"`
	BackendCredentialFile    string          `json:"backend_credential_file"`
	KurnSourceRevision       string          `json:"kurn_source_revision"`
	Rate                     rateDocument    `json:"rate"`
	Adapter                  adapterDocument `json:"adapter"`
	FixtureAuthenticatorFile string          `json:"fixture_authenticator_file"`
	ReadinessFile            string          `json:"readiness_file"`
}

func configuration(root string, options Options, recipe recipe) configDocument {
	return configDocument{
		Schema: localproof.ConfigSchema, ListenAddress: options.SearchdAddress,
		RuntimeStatusFile:     filepath.Join(root, "search-status.json"),
		KurnEndpoint:          "http://" + options.KurndAddress,
		BackendCredentialFile: filepath.Join(root, "backend-token"),
		KurnSourceRevision:    localproof.KurnSourceRevision,
		Rate:                  rateDocument{Unlimited: true},
		Adapter: adapterDocument{
			Name: recipe.name, PhysicalListName: recipe.physicalListName,
			ConfigSHA256: recipe.configSHA256, TimeoutMillis: 30000,
			MaxInflight: 1, WorstCaseChargeBytes: 1073741824,
		},
		FixtureAuthenticatorFile: filepath.Join(root, "fixture-auth.json"),
		ReadinessFile:            filepath.Join(root, "readiness.json"),
	}
}

func sameNonPathFields(staged, final configDocument) bool {
	final.RuntimeStatusFile = staged.RuntimeStatusFile
	final.BackendCredentialFile = staged.BackendCredentialFile
	final.FixtureAuthenticatorFile = staged.FixtureAuthenticatorFile
	final.ReadinessFile = staged.ReadinessFile
	return final == staged
}

func configurationBytes(value configDocument) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, errRefused
	}
	return append(raw, '\n'), nil
}

// These are the existing manual's fixed, deliberately non-secret fixtures.
func fixtureFiles() map[string][]byte {
	return map[string][]byte{
		"backend-token":     []byte("unused-local-backend-token"),
		"fixture-auth.json": []byte("{\"schema\":\"kurn-search-fixture-auth/v1\",\"credentials\":[{\"handle\":\"client-token\",\"tenant\":\"tenant-proof\"}]}\n"),
		"readiness.json":    []byte("{\"schema\":\"kurn-search-readiness/v1\",\"goldens\":[{\"name\":\"example-ready\",\"route\":\"catalog-identifier\",\"auth_handle\":\"client-token\",\"sentinel_tenant\":\"tenant-proof\",\"query\":\"ABC-123\",\"partition_id\":\"partition-proof\",\"limit\":25,\"expected_ids\":[\"config-proof\"],\"expected_complete\":true}]}\n"),
	}
}
