// Package localproof implements the deliberately narrow private/local
// kurn-searchd composition used by the Slice-8 process proof. It is not a
// production deployment or authentication package.
package localproof

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/kurn-dev/kurn-search/adapters/catalogid"
	"github.com/kurn-dev/kurn-search/adapters/partscatalog"
	"github.com/kurn-dev/kurn-search/adapters/partscatalogalternatives"
	"github.com/kurn-dev/kurn-search/adapters/partscatalogdiscover"
	"github.com/kurn-dev/kurn-search/adapters/partscatalogfacets"
	"github.com/kurn-dev/kurn-search/adapters/partscatalogselect"
	"github.com/kurn-dev/kurn-search/adapters/partscatalogsuggest"
	"github.com/kurn-dev/kurn-search/backend/kurnhttp"
	"github.com/kurn-dev/kurn-search/orchestrator"
	"github.com/kurn-dev/kurn-search/profile"
	searchservice "github.com/kurn-dev/kurn-search/service"
)

const (
	ConfigSchema        = "kurn-search-private-local-proof/v1"
	AuthenticatorSchema = "kurn-search-fixture-auth/v1"
	ReadinessSchema     = "kurn-search-readiness/v1"
	StatusSchema        = "kurn-search-private-local-status/v1"
	AuthenticatorKind   = "fixture_file_v1"
	// KurnSourceRevision and KurnCompatibilityLabel are the current engine
	// authority: the revision and label a new preparation binds. A bound
	// configuration names its revision, and is served with that revision's
	// label (see EngineAuthorities), so versions bound to a previously
	// admitted pair keep loading without reinterpretation.
	KurnSourceRevision     = "3d6c938ab3d59bbd87bc697e5e25610569fbac4d"
	KurnCompatibilityLabel = "v0.7.0"

	ExitFailure     = 1
	ExitUnavailable = 69
	ExitConfig      = 78

	maxPrivateFile = 1 << 20
)

// The prepared schema binds one immutable prepared artifact, served by a
// fully verifying engine, to the fixed parts-catalog-suggest deployment. It
// is a separate closed document; the legacy schema above is unchanged.
const (
	PreparedConfigSchema   = "kurn-search-prepared-local/v1"
	PreparedAdmission      = "prepared_full"
	PreparedDiagnosticFile = "profile-diagnostic.json"

	preparedStateDirectory   = "state"
	preparedStatusFile       = "search-status.json"
	preparedAdapterName      = "parts-catalog-suggest"
	preparedPhysicalListName = "parts-catalog-suggest-v1"
	preparedConfigSHA256     = "5c77cc033a580ee8f654fc6def1559713d527f2d3466006ea30735afc7eaecfc"
	preparedTimeoutMillis    = 30_000
	// preparedMaxInflightLimit bounds the prepared admission gate; the value a
	// version declares is bound byte for byte through its profile diagnostic.
	preparedMaxInflightLimit     = 64
	preparedWorstCaseChargeBytes = 1 << 30
	preparedMaxEntries           = 100_000
)

var (
	preparedConfigMembers = []string{
		"schema", "listen_address", "runtime_status_file", "kurn_endpoint", "backend_credential_file",
		"kurn_source_revision", "rate", "adapter", "fixture_authenticator_file", "readiness_file",
		"admission", "expected_base_sha256", "expected_entries",
	}
	preparedAdapterMembers = []string{
		"name", "physical_list_name", "config_sha256", "timeout_millis", "max_inflight", "worst_case_charge_bytes",
	}
	// preparedAdapterOptionalMembers may be present (non-null) beside the
	// required adapter members: the behaviour policy and the queue bound.
	preparedAdapterOptionalMembers = []string{"max_queue", "policy"}
	preparedRateMembers            = []string{"unlimited", "rate_per_sec"}
)

// EngineAuthority is one admitted engine pair: a public source revision and
// the compatibility label its executables report. The first entry is the
// current authority; the others stay admitted for versions bound to them.
type EngineAuthority struct{ Revision, Label string }

// EngineAuthorities lists the admitted engine pairs, current first. The
// historical deployunit, fleet and release constants are separate: they name
// delivered artifacts and never move with this table.
var EngineAuthorities = []EngineAuthority{
	{Revision: KurnSourceRevision, Label: KurnCompatibilityLabel},
	{Revision: "06c97f9fe695e29ce5adfc05f502edd68f5790e2", Label: "v0.7.0"},
	{Revision: "cf235b9b946ca0a60b5f6b205c376336fc5558ab", Label: "v0.6.0-3-gcf235b9"},
	{Revision: "e11dcf8873a37a948cddba71571fcf2cc49b4b1a", Label: "v0.6.0-2-ge11dcf8"},
	{Revision: "f7423cd8cbb8165caf8aa750f56586a0ee621249", Label: "v0.6.0-1-gf7423cd"},
}

// EngineLabelFor returns the compatibility label of an admitted revision.
func EngineLabelFor(revision string) (string, bool) {
	for _, authority := range EngineAuthorities {
		if authority.Revision == revision {
			return authority.Label, true
		}
	}
	return "", false
}

// EngineLabelAdmitted reports whether label belongs to an admitted pair.
func EngineLabelAdmitted(label string) bool {
	for _, authority := range EngineAuthorities {
		if authority.Label == label {
			return true
		}
	}
	return false
}

var (
	errConfig      = errors.New("localproof: configuration refused")
	errUnavailable = errors.New("localproof: readiness unavailable")
)

// Config is the fully resolved, immutable proof-mode configuration.
type Config struct {
	Root                  string
	ListenAddress         string
	RuntimeStatusFile     string
	KurnEndpoint          string
	BackendCredentialFile string
	BackendCredential     string
	KurnSourceRevision    string
	AttestedEngine        string
	Rate                  searchservice.RateAttestation
	AdapterName           string
	PhysicalListName      string
	ConfigSHA256          string
	Policy                string // the adapter's opt-in behaviour policy ("" default)
	MaxQueue              int    // waiting bound per list; 0 = the service default
	TimeoutMillis         uint64
	MaxInflight           int
	WorstCaseChargeBytes  uint64
	Authenticator         *searchservice.ConstantTimeBearerAuthenticator
	Goldens               []searchservice.Golden

	// Prepared-schema admission. A legacy config has the zero values:
	// AdmissionLegacy, no expectations and no diagnostic bytes.
	Admission          kurnhttp.AdmissionPolicy
	ExpectedBaseSHA256 string
	ExpectedEntries    uint64
	// Observer, when set by the daemon, receives the service's events; it is
	// not a configuration member.
	Observer searchservice.Observer
	// ProfileDiagnostic is a private copy of root/profile-diagnostic.json.
	// It is not a config member; Compose requires byte equality with the
	// rebuilt descriptor's DiagnosticJSON.
	ProfileDiagnostic []byte
}

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

// preparedConfigDocument is exactly the legacy member set plus the three
// required admission members. Presence and non-null are checked separately
// because encoding/json alone accepts missing and null members.
type preparedConfigDocument struct {
	configDocument
	Admission          string `json:"admission"`
	ExpectedBaseSHA256 string `json:"expected_base_sha256"`
	ExpectedEntries    uint64 `json:"expected_entries"`
}

type rateDocument struct {
	Unlimited     bool `json:"unlimited"`
	RatePerSecond int  `json:"rate_per_sec"`
}

type adapterDocument struct {
	Name                 string `json:"name"`
	PhysicalListName     string `json:"physical_list_name"`
	ConfigSHA256         string `json:"config_sha256"`
	TimeoutMillis        uint64 `json:"timeout_millis"`
	MaxInflight          int    `json:"max_inflight"`
	WorstCaseChargeBytes uint64 `json:"worst_case_charge_bytes"`
	Policy               string `json:"policy,omitempty"`
	// MaxQueue bounds the requests waiting for this adapter's admission
	// slots (1..searchservice.MaxServiceQueue); absent selects the
	// service default of DefaultQueueFactor × max_inflight.
	MaxQueue *int `json:"max_queue,omitempty"`
}

type authDocument struct {
	Schema      string           `json:"schema"`
	Credentials []authCredential `json:"credentials"`
}

type authCredential struct {
	Handle string `json:"handle"`
	Tenant string `json:"tenant"`
}

type readinessDocument struct {
	Schema  string                    `json:"schema"`
	Goldens []readinessGoldenDocument `json:"goldens"`
}

type readinessGoldenDocument struct {
	Name             string   `json:"name"`
	Route            string   `json:"route"`
	AuthHandle       string   `json:"auth_handle"`
	SentinelTenant   string   `json:"sentinel_tenant"`
	Query            string   `json:"query"`
	PartitionID      string   `json:"partition_id,omitempty"`
	Limit            int      `json:"limit"`
	ExpectedIDs      []string `json:"expected_ids"`
	ExpectedComplete bool     `json:"expected_complete"`
}

// Load reads the single closed proof config and every mode-checked input.
func Load(path string) (Config, error) {
	if path == "" || !filepath.IsAbs(path) {
		return Config{}, errConfig
	}
	root := filepath.Dir(filepath.Clean(path))
	if err := validateRoot(root); err != nil {
		return Config{}, errConfig
	}
	raw, err := readPrivateFile(root, path, maxPrivateFile)
	if err != nil {
		return Config{}, errConfig
	}
	var document configDocument
	var prepared preparedConfigDocument
	preparedMode := preparedSchema(raw)
	if preparedMode {
		if decodePrepared(raw, &prepared) != nil || validatePreparedState(root, prepared.RuntimeStatusFile) != nil {
			return Config{}, errConfig
		}
		document = prepared.configDocument
	} else if decodeClosed(raw, &document) != nil || document.Schema != ConfigSchema {
		return Config{}, errConfig
	}
	attestedEngine, engineAdmitted := EngineLabelFor(document.KurnSourceRevision)
	if !engineAdmitted || !validSHA256(document.Adapter.ConfigSHA256) ||
		(document.Adapter.Name != "catalog-identifier" && document.Adapter.Name != "parts-catalog" && document.Adapter.Name != "parts-catalog-discover" && document.Adapter.Name != "parts-catalog-discover-facets" && document.Adapter.Name != "parts-catalog-discover-facet-alternatives" && document.Adapter.Name != "parts-catalog-select" && document.Adapter.Name != "parts-catalog-suggest") || document.Adapter.PhysicalListName == "" ||
		document.Adapter.TimeoutMillis == 0 || document.Adapter.TimeoutMillis > 600_000 ||
		document.Adapter.MaxInflight < 1 || document.Adapter.WorstCaseChargeBytes == 0 ||
		!validPolicyFor(document.Adapter.Name, document.Adapter.Policy) ||
		(document.Adapter.MaxQueue != nil && (*document.Adapter.MaxQueue < 1 || *document.Adapter.MaxQueue > searchservice.MaxServiceQueue)) ||
		validateListen(document.ListenAddress) != nil || validateLoopbackEndpoint(document.KurnEndpoint) != nil ||
		validateRate(document.Rate) != nil {
		return Config{}, errConfig
	}
	statusPath, err := resolveFutureFile(root, document.RuntimeStatusFile)
	if err != nil {
		return Config{}, errConfig
	}
	credentialPath, err := resolveExistingFile(root, document.BackendCredentialFile)
	if err != nil {
		return Config{}, errConfig
	}
	credentialRaw, err := readPrivateFile(root, credentialPath, 64<<10)
	if err != nil || len(credentialRaw) == 0 || !bytes.Equal(credentialRaw, bytes.TrimSpace(credentialRaw)) ||
		bytes.ContainsAny(credentialRaw, "\r\n") || !utf8.Valid(credentialRaw) {
		return Config{}, errConfig
	}
	authPath, err := resolveExistingFile(root, document.FixtureAuthenticatorFile)
	if err != nil {
		return Config{}, errConfig
	}
	authRaw, err := readPrivateFile(root, authPath, 64<<10)
	if err != nil {
		return Config{}, errConfig
	}
	var auth authDocument
	if decodeClosed(authRaw, &auth) != nil || auth.Schema != AuthenticatorSchema || len(auth.Credentials) == 0 {
		return Config{}, errConfig
	}
	credentials := make(map[string]string, len(auth.Credentials))
	for _, entry := range auth.Credentials {
		if entry.Handle == "" || entry.Tenant == "" || !utf8.ValidString(entry.Handle) ||
			!utf8.ValidString(entry.Tenant) || len(entry.Handle) > 4096 || len(entry.Tenant) > 4096 {
			return Config{}, errConfig
		}
		if _, duplicate := credentials[entry.Handle]; duplicate {
			return Config{}, errConfig
		}
		credentials[entry.Handle] = entry.Tenant
	}
	authenticator, err := searchservice.NewConstantTimeBearerAuthenticator(credentials)
	if err != nil {
		return Config{}, errConfig
	}
	readinessPath, err := resolveExistingFile(root, document.ReadinessFile)
	if err != nil {
		return Config{}, errConfig
	}
	readinessRaw, err := readPrivateFile(root, readinessPath, maxPrivateFile)
	if err != nil {
		return Config{}, errConfig
	}
	var readiness readinessDocument
	if decodeClosed(readinessRaw, &readiness) != nil || readiness.Schema != ReadinessSchema || len(readiness.Goldens) == 0 {
		return Config{}, errConfig
	}
	goldens := make([]searchservice.Golden, len(readiness.Goldens))
	seenGoldens := make(map[string]struct{}, len(readiness.Goldens))
	for index, golden := range readiness.Goldens {
		mappedTenant, authenticated := credentials[golden.AuthHandle]
		if golden.Name == "" || golden.Route != document.Adapter.Name || golden.AuthHandle == "" ||
			golden.SentinelTenant == "" || !authenticated || mappedTenant != golden.SentinelTenant ||
			golden.Query == "" || !utf8.ValidString(golden.Query) ||
			utf8.RuneCountInString(golden.Query) > profile.HardMaxQueryRunes || golden.Limit < 1 ||
			golden.Limit > profile.HardMaxResultLimit || len(golden.ExpectedIDs) > golden.Limit ||
			!golden.ExpectedComplete || !utf8.ValidString(golden.PartitionID) {
			return Config{}, errConfig
		}
		if _, duplicate := seenGoldens[golden.Name]; duplicate {
			return Config{}, errConfig
		}
		seenGoldens[golden.Name] = struct{}{}
		seenIDs := make(map[string]struct{}, len(golden.ExpectedIDs))
		for _, id := range golden.ExpectedIDs {
			if id == "" || !utf8.ValidString(id) {
				return Config{}, errConfig
			}
			if _, duplicate := seenIDs[id]; duplicate {
				return Config{}, errConfig
			}
			seenIDs[id] = struct{}{}
		}
		goldens[index] = searchservice.Golden{
			Name: golden.Name, Route: golden.Route, AuthHandle: golden.AuthHandle,
			SentinelTenant: golden.SentinelTenant, Query: golden.Query, PartitionID: golden.PartitionID,
			Limit: golden.Limit, ExpectedIDs: append([]string(nil), golden.ExpectedIDs...),
			ExpectedComplete: true,
		}
	}
	config := Config{
		Root: root, ListenAddress: document.ListenAddress, RuntimeStatusFile: statusPath,
		KurnEndpoint: document.KurnEndpoint, BackendCredentialFile: credentialPath,
		BackendCredential: string(credentialRaw), KurnSourceRevision: document.KurnSourceRevision,
		AttestedEngine:   attestedEngine,
		Rate:             searchservice.RateAttestation{Unlimited: document.Rate.Unlimited, RatePerSecond: document.Rate.RatePerSecond},
		AdapterName:      document.Adapter.Name,
		PhysicalListName: document.Adapter.PhysicalListName, ConfigSHA256: document.Adapter.ConfigSHA256,
		Policy:        document.Adapter.Policy,
		TimeoutMillis: document.Adapter.TimeoutMillis, MaxInflight: document.Adapter.MaxInflight,
		MaxQueue:             queueBound(document.Adapter.MaxQueue),
		WorstCaseChargeBytes: document.Adapter.WorstCaseChargeBytes,
		Authenticator:        authenticator, Goldens: goldens,
	}
	if preparedMode {
		diagnostic, err := readPreparedDiagnostic(root)
		if err != nil {
			return Config{}, errConfig
		}
		config.Admission = kurnhttp.AdmissionPreparedFull
		config.ExpectedBaseSHA256 = prepared.ExpectedBaseSHA256
		config.ExpectedEntries = prepared.ExpectedEntries
		config.ProfileDiagnostic = diagnostic
		if !preparedBindingsHold(config) {
			return Config{}, errConfig
		}
	}
	return config, nil
}

// Compose binds the single resolved endpoint to both backend and service.
func Compose(config Config) (*searchservice.Service, error) {
	var descriptor profile.Descriptor
	var semantics orchestrator.RuntimeSemantics
	var err error
	switch config.AdapterName {
	case "catalog-identifier":
		descriptor, err = catalogid.NewDescriptor(catalogid.OperationalBinding{
			PhysicalListName: config.PhysicalListName, ConfigSHA256: config.ConfigSHA256,
			TestedEngines: []string{config.AttestedEngine}, TimeoutMillis: config.TimeoutMillis,
			MaxInflight: config.MaxInflight, WorstCaseChargeBytes: config.WorstCaseChargeBytes,
		})
		semantics = catalogid.New()
	case "parts-catalog":
		descriptor, err = partscatalog.NewDescriptor(partscatalog.OperationalBinding{
			PhysicalListName: config.PhysicalListName, ConfigSHA256: config.ConfigSHA256,
			TestedEngines: []string{config.AttestedEngine}, TimeoutMillis: config.TimeoutMillis,
			MaxInflight: config.MaxInflight, WorstCaseChargeBytes: config.WorstCaseChargeBytes,
		})
		semantics = partscatalog.New()
	case "parts-catalog-select":
		descriptor, err = partscatalogselect.NewDescriptor(partscatalogselect.OperationalBinding{
			PhysicalListName: config.PhysicalListName, ConfigSHA256: config.ConfigSHA256,
			TestedEngines: []string{config.AttestedEngine}, TimeoutMillis: config.TimeoutMillis,
			MaxInflight: config.MaxInflight, WorstCaseChargeBytes: config.WorstCaseChargeBytes,
		})
	case "parts-catalog-discover":
		descriptor, err = partscatalogdiscover.NewDescriptor(partscatalogdiscover.OperationalBinding{
			PhysicalListName: config.PhysicalListName, ConfigSHA256: config.ConfigSHA256,
			TestedEngines: []string{config.AttestedEngine}, TimeoutMillis: config.TimeoutMillis,
			MaxInflight: config.MaxInflight, WorstCaseChargeBytes: config.WorstCaseChargeBytes,
		})
	case "parts-catalog-discover-facets":
		descriptor, err = partscatalogfacets.NewDescriptor(partscatalogfacets.OperationalBinding{
			PhysicalListName: config.PhysicalListName, ConfigSHA256: config.ConfigSHA256,
			TestedEngines: []string{config.AttestedEngine}, TimeoutMillis: config.TimeoutMillis,
			MaxInflight: config.MaxInflight, WorstCaseChargeBytes: config.WorstCaseChargeBytes,
		})
	case "parts-catalog-discover-facet-alternatives":
		descriptor, err = partscatalogalternatives.NewDescriptor(partscatalogalternatives.OperationalBinding{
			PhysicalListName: config.PhysicalListName, ConfigSHA256: config.ConfigSHA256,
			TestedEngines: []string{config.AttestedEngine}, TimeoutMillis: config.TimeoutMillis,
			MaxInflight: config.MaxInflight, WorstCaseChargeBytes: config.WorstCaseChargeBytes,
		})
	case "parts-catalog-suggest":
		descriptor, err = partscatalogsuggest.NewDescriptor(partscatalogsuggest.OperationalBinding{
			PhysicalListName: config.PhysicalListName, ConfigSHA256: config.ConfigSHA256, Policy: config.Policy,
			TestedEngines: []string{config.AttestedEngine}, TimeoutMillis: config.TimeoutMillis,
			MaxInflight: config.MaxInflight, WorstCaseChargeBytes: config.WorstCaseChargeBytes,
		})
	default:
		return nil, errConfig
	}
	if err != nil {
		return nil, errConfig
	}
	if !admissionBindingHolds(config, descriptor) {
		return nil, errConfig
	}
	minimumRate := (descriptor.Spec().Bounds.MaxProbes + 1) / 2
	if !config.Rate.Unlimited && config.Rate.RatePerSecond < minimumRate {
		return nil, errConfig
	}
	backend, err := kurnhttp.NewClient(kurnhttp.ClientConfig{
		BaseURL: config.KurnEndpoint, BearerToken: config.BackendCredential,
		MaxResponseBytes: int64(descriptor.Spec().Bounds.MaxResponseBytes),
		MaxRetryAfter:    searchservice.MaxServiceRetryAfter,
		Admission:        config.Admission, ExpectedBaseSHA256: config.ExpectedBaseSHA256,
		ExpectedEntries: config.ExpectedEntries,
	})
	if err != nil {
		return nil, errConfig
	}
	var executor *orchestrator.Executor
	if config.AdapterName == "parts-catalog-discover-facets" {
		executor, err = orchestrator.NewWithFacets(descriptor, partscatalogfacets.New(), backend)
	} else if config.AdapterName == "parts-catalog-discover-facet-alternatives" {
		executor, err = orchestrator.NewWithAlternatives(descriptor, partscatalogalternatives.New(), backend)
	} else if config.AdapterName == "parts-catalog-discover" {
		executor, err = orchestrator.NewWithMembership(descriptor, partscatalogdiscover.New(), backend)
	} else if config.AdapterName == "parts-catalog-select" {
		executor, err = orchestrator.NewWithMembership(descriptor, partscatalogselect.New(), backend)
	} else if config.AdapterName == "parts-catalog-suggest" {
		suggest, serr := partscatalogsuggest.NewWithPolicy(config.Policy)
		if serr != nil {
			return nil, errConfig
		}
		executor, err = orchestrator.NewWithMembershipFallback(descriptor, suggest, backend)
	} else {
		executor, err = orchestrator.New(descriptor, semantics, backend)
	}
	if err != nil {
		return nil, errConfig
	}
	var binding searchservice.Binding
	if config.AdapterName == "parts-catalog-discover-facets" {
		facetRunner, ferr := searchservice.WrapFacetExecutor(executor)
		if ferr != nil {
			return nil, errConfig
		}
		binding, err = searchservice.NewFacetBinding(descriptor, facetRunner)
		if err != nil {
			return nil, errConfig
		}
	} else if config.AdapterName == "parts-catalog-discover-facet-alternatives" {
		alternativeRunner, aerr := searchservice.WrapAlternativeExecutor(executor)
		if aerr != nil {
			return nil, errConfig
		}
		binding, err = searchservice.NewAlternativeBinding(descriptor, alternativeRunner)
		if err != nil {
			return nil, errConfig
		}
	} else {
		runner, rerr := searchservice.WrapExecutor(executor)
		if rerr != nil {
			return nil, errConfig
		}
		binding = searchservice.Binding{Descriptor: descriptor, Runner: runner}
	}
	value, err := searchservice.Compose(&searchservice.Config{
		Bindings:      []searchservice.Binding{binding},
		Authenticator: config.Authenticator, AttestedEngine: config.AttestedEngine,
		Rate: config.Rate, AdmissionWait: searchservice.MaxAdmissionWait, MaxQueue: config.MaxQueue,
		CredentialFile: config.BackendCredentialFile, KurnEndpoint: config.KurnEndpoint,
		ReadinessGoldens: config.Goldens, Observer: config.Observer,
	})
	if err != nil {
		return nil, errUnavailable
	}
	return value, nil
}

// Run executes the fail-closed proof daemon and returns its closed exit code.
func Run(args []string, signals <-chan os.Signal, stderr io.Writer) int {
	if len(args) != 2 || args[0] != "-config" || args[1] == "" || stderr == nil {
		writeConstant(stderr, "kurn-searchd: configuration refused\n")
		return ExitConfig
	}
	config, err := Load(args[1])
	if err != nil {
		writeConstant(stderr, "kurn-searchd: configuration refused\n")
		return ExitConfig
	}
	counters := &fallbackCounters{}
	config.Observer = counters
	defer func() { writeConstant(stderr, counters.line()) }()
	service, err := Compose(config)
	if err != nil {
		if errors.Is(err, errUnavailable) {
			writeConstant(stderr, "kurn-searchd: readiness unavailable\n")
			return ExitUnavailable
		}
		writeConstant(stderr, "kurn-searchd: configuration refused\n")
		return ExitConfig
	}
	server, err := service.HTTPServer(config.ListenAddress)
	if err != nil {
		writeConstant(stderr, "kurn-searchd: configuration refused\n")
		return ExitConfig
	}
	listener, err := net.Listen("tcp", config.ListenAddress)
	if err != nil {
		writeConstant(stderr, "kurn-searchd: process failure\n")
		return ExitFailure
	}
	readyContext, cancelReady := context.WithTimeout(context.Background(), 30*time.Second)
	err = service.Ready(readyContext)
	cancelReady()
	if err != nil {
		_ = listener.Close()
		writeConstant(stderr, "kurn-searchd: readiness unavailable\n")
		return ExitUnavailable
	}
	if writeStatus(config.RuntimeStatusFile, listener.Addr().String()) != nil {
		_ = listener.Close()
		writeConstant(stderr, "kurn-searchd: process failure\n")
		return ExitFailure
	}
	defer os.Remove(config.RuntimeStatusFile)
	serveResult := make(chan error, 1)
	go func() {
		err := server.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		serveResult <- err
	}()
	select {
	case <-signals:
		shutdownContext, cancel := context.WithTimeout(context.Background(), searchservice.ShutdownTimeout)
		err := searchservice.Shutdown(shutdownContext, server)
		cancel()
		if err != nil {
			writeConstant(stderr, "kurn-searchd: process failure\n")
			return ExitFailure
		}
		if err := <-serveResult; err != nil {
			writeConstant(stderr, "kurn-searchd: process failure\n")
			return ExitFailure
		}
		return 0
	case err := <-serveResult:
		if err != nil {
			writeConstant(stderr, "kurn-searchd: process failure\n")
		}
		return ExitFailure
	}
}

func writeConstant(writer io.Writer, value string) {
	if writer != nil {
		_, _ = io.WriteString(writer, value)
	}
}

func writeStatus(path, address string) error {
	value := struct {
		Schema  string `json:"schema"`
		Ready   bool   `json:"ready"`
		Address string `json:"address"`
	}{Schema: StatusSchema, Ready: true, Address: address}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(raw)
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

func validateRate(rate rateDocument) error {
	if rate.Unlimited {
		if rate.RatePerSecond != 0 {
			return errConfig
		}
		return nil
	}
	if rate.RatePerSecond < 1 {
		return errConfig
	}
	return nil
}

func validateListen(value string) error {
	host, port, err := net.SplitHostPort(value)
	if err != nil || host != "127.0.0.1" || port == "" {
		return errConfig
	}
	parsed, err := net.LookupPort("tcp", port)
	if err != nil || parsed < 0 || parsed > 65535 {
		return errConfig
	}
	return nil
}

func validateLoopbackEndpoint(value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "http" || parsed.Hostname() != "127.0.0.1" || parsed.Port() == "" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != "" ||
		parsed.RawPath != "" || parsed.Opaque != "" {
		return errConfig
	}
	if _, err := net.LookupPort("tcp", parsed.Port()); err != nil {
		return errConfig
	}
	return nil
}

func validateRoot(root string) error {
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o700 {
		return errConfig
	}
	return nil
}

func resolveExistingFile(root, path string) (string, error) {
	resolved, err := resolveUnderRoot(root, path)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(resolved)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 {
		return "", errConfig
	}
	return resolved, nil
}

func resolveFutureFile(root, path string) (string, error) {
	resolved, err := resolveUnderRoot(root, path)
	if err != nil {
		return "", err
	}
	if _, err := os.Lstat(resolved); !errors.Is(err, os.ErrNotExist) {
		return "", errConfig
	}
	return resolved, nil
}

func resolveUnderRoot(root, path string) (string, error) {
	if path == "" || !filepath.IsAbs(path) {
		return "", errConfig
	}
	clean := filepath.Clean(path)
	relative, err := filepath.Rel(root, clean)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errConfig
	}
	current := root
	parts := strings.Split(relative, string(filepath.Separator))
	for _, part := range parts[:len(parts)-1] {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", errConfig
		}
	}
	return clean, nil
}

func readPrivateFile(root, path string, maximum int64) ([]byte, error) {
	resolved, err := resolveExistingFile(root, path)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(resolved)
	if err != nil || info.Size() < 1 || info.Size() > maximum {
		return nil, errConfig
	}
	return os.ReadFile(resolved)
}

func validSHA256(value string) bool {
	raw, err := hex.DecodeString(value)
	return err == nil && len(raw) == 32 && value == strings.ToLower(value)
}

// preparedSchema selects the prepared decoder only for a document that names
// the prepared schema. Everything else, including malformed input, takes the
// unchanged legacy path, which refuses anything but the legacy schema.
func preparedSchema(raw []byte) bool {
	var head struct {
		Schema string `json:"schema"`
	}
	return json.Unmarshal(raw, &head) == nil && head.Schema == PreparedConfigSchema
}

// decodePrepared applies the same closed decoding as the legacy schema, then
// requires the exact member sets, with no null member, at the top level and
// in the adapter and rate objects.
func decodePrepared(raw []byte, document *preparedConfigDocument) error {
	if decodeClosed(raw, document) != nil {
		return errConfig
	}
	members, err := exactMembers(raw, preparedConfigMembers)
	if err != nil {
		return errConfig
	}
	if _, err := exactMembersWithOptional(members["adapter"], preparedAdapterMembers, preparedAdapterOptionalMembers); err != nil {
		return errConfig
	}
	if _, err := exactMembers(members["rate"], preparedRateMembers); err != nil {
		return errConfig
	}
	if document.Schema != PreparedConfigSchema || document.Admission != PreparedAdmission {
		return errConfig
	}
	return nil
}

func exactMembers(raw []byte, names []string) (map[string]json.RawMessage, error) {
	var members map[string]json.RawMessage
	if json.Unmarshal(raw, &members) != nil || len(members) != len(names) {
		return nil, errConfig
	}
	for _, name := range names {
		value, ok := members[name]
		if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return nil, errConfig
		}
	}
	return members, nil
}

// exactMembersWithOptional is exactMembers whose object may also carry any
// of the optional names, each non-null when present.
func exactMembersWithOptional(raw []byte, names, optional []string) (map[string]json.RawMessage, error) {
	var members map[string]json.RawMessage
	if json.Unmarshal(raw, &members) != nil || len(members) < len(names) || len(members) > len(names)+len(optional) {
		return nil, errConfig
	}
	for _, name := range names {
		value, ok := members[name]
		if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return nil, errConfig
		}
	}
	extra := len(members) - len(names)
	for _, name := range optional {
		if value, ok := members[name]; ok {
			if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				return nil, errConfig
			}
			extra--
		}
	}
	if extra != 0 {
		return nil, errConfig
	}
	return members, nil
}

// validatePreparedState requires the status file at exactly
// root/state/search-status.json, inside a physical, current-user-owned
// directory of mode exactly 0700. The status file itself must still be
// absent; resolveFutureFile enforces that afterwards.
func validatePreparedState(root, statusFile string) error {
	state := filepath.Join(root, preparedStateDirectory)
	if statusFile != filepath.Join(state, preparedStatusFile) {
		return errConfig
	}
	info, err := os.Lstat(state)
	if err != nil || info.Mode() != os.ModeDir|0o700 || !ownedByCurrentUser(info) {
		return errConfig
	}
	return nil
}

// ownedByCurrentUser reads the platform owner uid without a build-tagged
// syscall dependency, so this package keeps compiling for every target. A
// platform without a numeric owner uid fails closed.
func ownedByCurrentUser(info os.FileInfo) bool {
	current := os.Getuid()
	value := reflect.ValueOf(info.Sys())
	if current < 0 || value.Kind() != reflect.Pointer || value.IsNil() || value.Elem().Kind() != reflect.Struct {
		return false
	}
	uid := value.Elem().FieldByName("Uid")
	switch uid.Kind() {
	case reflect.Uint, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return uid.Uint() == uint64(current)
	default:
		return false
	}
}

// readPreparedDiagnostic reads the fixed private diagnostic file: regular,
// not a symlink, no group/other permission, 1 byte to 1 MiB.
func readPreparedDiagnostic(root string) ([]byte, error) {
	raw, err := readPrivateFile(root, filepath.Join(root, PreparedDiagnosticFile), maxPrivateFile)
	if err != nil || len(raw) < 1 || len(raw) > maxPrivateFile {
		return nil, errConfig
	}
	return bytes.Clone(raw), nil
}

// preparedBindingsHold is the fixed prepared deployment binding. Load and
// Compose both apply it to the resolved Config.
func preparedBindingsHold(config Config) bool {
	return config.Admission == kurnhttp.AdmissionPreparedFull &&
		engineBindingHolds(config) &&
		config.Rate.Unlimited && config.Rate.RatePerSecond == 0 &&
		config.AdapterName == preparedAdapterName && config.PhysicalListName == preparedPhysicalListName &&
		config.ConfigSHA256 == preparedConfigSHA256 && config.TimeoutMillis == preparedTimeoutMillis &&
		config.MaxInflight >= 1 && config.MaxInflight <= preparedMaxInflightLimit && config.WorstCaseChargeBytes == preparedWorstCaseChargeBytes &&
		validSHA256(config.ExpectedBaseSHA256) &&
		config.ExpectedEntries >= 1 && config.ExpectedEntries <= preparedMaxEntries &&
		len(config.ProfileDiagnostic) >= 1 && len(config.ProfileDiagnostic) <= maxPrivateFile &&
		config.RuntimeStatusFile == filepath.Join(config.Root, preparedStateDirectory, preparedStatusFile)
}

// engineBindingHolds requires the bound revision to be admitted and the
// attested engine to be that revision's label: a current or previously
// admitted pair, never a mixed one.
func engineBindingHolds(config Config) bool {
	label, ok := EngineLabelFor(config.KurnSourceRevision)
	return ok && config.AttestedEngine == label
}

// admissionBindingHolds keeps legacy composition exactly as before (no
// admission evidence at all) and, for the prepared policy, rebinds the whole
// rebuilt descriptor to the loaded diagnostic bytes.
func admissionBindingHolds(config Config, descriptor profile.Descriptor) bool {
	switch config.Admission {
	case kurnhttp.AdmissionLegacy:
		return config.ExpectedBaseSHA256 == "" && config.ExpectedEntries == 0 && len(config.ProfileDiagnostic) == 0
	case kurnhttp.AdmissionPreparedFull:
		return preparedBindingsHold(config) && bytes.Equal(descriptor.DiagnosticJSON(), config.ProfileDiagnostic)
	default:
		return false
	}
}

func queueBound(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

// validPolicyFor accepts the default policy for every adapter and the
// suggest adapter's opt-in policies for it alone.
func validPolicyFor(adapterName, policy string) bool {
	if policy == "" {
		return true
	}
	return adapterName == "parts-catalog-suggest" && (policy == partscatalogsuggest.PolicyOSA1 || policy == partscatalogsuggest.PolicyOSA1G60 || policy == partscatalogsuggest.PolicyOSA1G60Union)
}

// fallbackCounters counts, per daemon lifetime, the successful requests,
// how many of them ran the adapter's fallback step in their producing rung
// (with and without hits), and every physical fallback batch sent, rungs
// and attempts that were escalated or restarted included. They are
// reported on stderr at shutdown, never in a response.
type fallbackCounters struct{ requests, fallbackAttempted, fallbackWithHits, fallbackCalls atomic.Int64 }

func (c *fallbackCounters) Observe(event searchservice.Event) {
	if event.Kind != "request" || event.Outcome != "success" {
		return
	}
	c.requests.Add(1)
	c.fallbackCalls.Add(int64(event.FallbackCalls))
	if event.FallbackTaken {
		c.fallbackAttempted.Add(1)
		if event.FallbackHits > 0 {
			c.fallbackWithHits.Add(1)
		}
	}
}

func (c *fallbackCounters) line() string {
	return fmt.Sprintf("kurn-searchd: counters {\"requests\":%d,\"fallback_attempted\":%d,\"fallback_with_hits\":%d,\"fallback_calls\":%d}\n", c.requests.Load(), c.fallbackAttempted.Load(), c.fallbackWithHits.Load(), c.fallbackCalls.Load())
}

func decodeClosed(raw []byte, destination any) error {
	if len(raw) == 0 || !utf8.Valid(raw) || !json.Valid(raw) || rejectDuplicateNames(raw) != nil || rejectUnpairedSurrogates(raw) != nil {
		return errConfig
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return errConfig
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errConfig
	}
	return nil
}

func rejectDuplicateNames(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if walkJSON(decoder) != nil {
		return errConfig
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errConfig
	}
	return nil
}

func walkJSON(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := map[string]struct{}{}
		for decoder.More() {
			nameToken, err := decoder.Token()
			if err != nil {
				return err
			}
			name, ok := nameToken.(string)
			if !ok {
				return errConfig
			}
			if _, duplicate := seen[name]; duplicate {
				return errConfig
			}
			seen[name] = struct{}{}
			if walkJSON(decoder) != nil {
				return errConfig
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return errConfig
		}
	case '[':
		for decoder.More() {
			if walkJSON(decoder) != nil {
				return errConfig
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return errConfig
		}
	default:
		return errConfig
	}
	return nil
}

// rejectUnpairedSurrogates rejects the replacement behavior of encoding/json
// for lone UTF-16 surrogate escapes while allowing valid pairs.
func rejectUnpairedSurrogates(raw []byte) error {
	for index := 0; index+5 < len(raw); index++ {
		if raw[index] != '\\' || raw[index+1] != 'u' {
			continue
		}
		unit, ok := decodeHex(raw[index+2 : index+6])
		if !ok {
			return errConfig
		}
		if unit >= 0xd800 && unit <= 0xdbff {
			if index+11 >= len(raw) || raw[index+6] != '\\' || raw[index+7] != 'u' {
				return errConfig
			}
			low, ok := decodeHex(raw[index+8 : index+12])
			if !ok || low < 0xdc00 || low > 0xdfff {
				return errConfig
			}
			index += 6
		} else if unit >= 0xdc00 && unit <= 0xdfff {
			return errConfig
		}
	}
	return nil
}

func decodeHex(raw []byte) (uint16, bool) {
	if len(raw) != 4 {
		return 0, false
	}
	var value uint16
	for _, digit := range raw {
		value <<= 4
		switch {
		case digit >= '0' && digit <= '9':
			value |= uint16(digit - '0')
		case digit >= 'a' && digit <= 'f':
			value |= uint16(digit-'a') + 10
		case digit >= 'A' && digit <= 'F':
			value |= uint16(digit-'A') + 10
		default:
			return 0, false
		}
	}
	return value, true
}
