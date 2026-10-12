package preparedsetup

import (
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/kurn-dev/kurn-search/internal/localproof"
)

const manifestSchema = "kurn-search-prepared-version/v1"
const maxManifest = 1 << 20
const maxExactInteger = 1<<53 - 1

var immutableFileModes = map[string]string{
	"backend-token":              "0400",
	"bin/kurn-searchd":           "0500",
	"bin/kurnd":                  "0500",
	"client-credentials.json":    "0400",
	"data/" + listName + ".kurn": "0400",
	"fixture-auth.json":          "0400",
	"kurn-searchd.json":          "0400",
	"kurnd-api-keys":             "0400",
	"profile-diagnostic.json":    "0400",
	"readiness.json":             "0400",
}

type fileIdentity struct {
	Mode   string `json:"mode"`
	SHA256 string `json:"sha256"`
}
type byteIdentity struct {
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}
type buildAssertion struct {
	GoVersion   string `json:"go_version"`
	MainPath    string `json:"main_path"`
	Path        string `json:"path"`
	VCSModified string `json:"vcs_modified"`
	VCSRevision string `json:"vcs_revision"`
}
type profileManifest struct {
	BehaviorSHA256   string `json:"behavior_sha256"`
	CodecSHA256      string `json:"codec_sha256"`
	DiagnosticFile   string `json:"diagnostic_file"`
	DiagnosticSHA256 string `json:"diagnostic_sha256"`
	Name             string `json:"name"`
	Policy           string `json:"policy,omitempty"`
	SearchSHA256     string `json:"search_sha256"`
	Version          string `json:"version"`
}
type operationalManifest struct {
	Admission            string `json:"admission"`
	ConfigSHA256         string `json:"config_sha256"`
	ExpectedBaseSHA256   string `json:"expected_base_sha256"`
	ExpectedEntries      uint64 `json:"expected_entries"`
	MaxInflight          int    `json:"max_inflight"`
	MaxQueue             int    `json:"max_queue,omitempty"`
	PhysicalListName     string `json:"physical_list_name"`
	TestedEngine         string `json:"tested_engine"`
	TimeoutMillis        uint64 `json:"timeout_millis"`
	WorstCaseChargeBytes uint64 `json:"worst_case_charge_bytes"`
}
type sourceIdentity struct {
	Bytes   int64  `json:"bytes"`
	Records int64  `json:"records"`
	SHA256  string `json:"sha256"`
	Tenants int64  `json:"tenants"`
}
type projectedIdentity struct {
	Keys        int    `json:"keys"`
	LinesSHA256 string `json:"lines_sha256"`
}
type artifactIdentity struct {
	ContainerSHA256 string `json:"container_sha256"`
	Path            string `json:"path"`
}
type recordedManifest struct {
	Artifact       artifactIdentity        `json:"artifact"`
	Executables    map[string]byteIdentity `json:"executables"`
	Files          map[string]fileIdentity `json:"files"`
	MappingSHA256  string                  `json:"mapping_sha256"`
	Projected      projectedIdentity       `json:"projected"`
	SearchContract map[string]string       `json:"search_contract"`
	Source         sourceIdentity          `json:"source"`
}
type reportedManifest struct {
	AnalyzerDeclared   string `json:"analyzer_declared"`
	AnalyzerRecomputed string `json:"analyzer_recomputed"`
	BaseSHA256         string `json:"base_sha256"`
	Collapsed          uint64 `json:"collapsed"`
	ConfigDeclared     string `json:"config_declared"`
	ConfigRecomputed   string `json:"config_recomputed"`
	ContainerSHA256    string `json:"container_sha256"`
	DroppedKeys        uint64 `json:"dropped_keys"`
	Entries            uint64 `json:"entries"`
	Format             string `json:"format"`
	InputSHA256        string `json:"input_sha256"`
	KeylessEntries     uint64 `json:"keyless_entries"`
	Keys               uint64 `json:"keys"`
	List               string `json:"list"`
	Mode               string `json:"mode"`
	VerifyLevel        string `json:"verify_level"`
}
type assertedManifest struct {
	CoreInventorySHA256   string                    `json:"core_inventory_sha256"`
	Executables           map[string]buildAssertion `json:"executables"`
	SearchInventorySHA256 string                    `json:"search_inventory_sha256"`
}
type readinessManifest struct {
	ExpectedID string `json:"expected_id"`
	Partition  string `json:"partition"`
	Query      string `json:"query"`
	Tenant     string `json:"tenant"`
}
type manifest struct {
	Asserted    assertedManifest    `json:"asserted"`
	Operational operationalManifest `json:"operational"`
	Profile     profileManifest     `json:"profile"`
	Readiness   readinessManifest   `json:"readiness"`
	Recorded    recordedManifest    `json:"recorded"`
	Reported    reportedManifest    `json:"reported"`
	Schema      string              `json:"schema"`
}

func fileSHA(path string) (string, int64, error) {
	f, e := os.Open(path)
	if e != nil {
		return "", 0, errRefused
	}
	defer f.Close()
	h := sha256.New()
	n, e := io.Copy(h, f)
	if e != nil {
		return "", 0, errRefused
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}
func bytesSHA(raw []byte) string { s := sha256.Sum256(raw); return hex.EncodeToString(s[:]) }

func executableAssertion(path string) (buildAssertion, error) {
	i, e := buildinfo.ReadFile(path)
	if e != nil || i.GoVersion == "" || i.Path == "" || i.Main.Path == "" {
		return buildAssertion{}, errRefused
	}
	a := buildAssertion{GoVersion: i.GoVersion, Path: i.Path, MainPath: i.Main.Path, VCSRevision: "absent", VCSModified: "absent"}
	for _, s := range i.Settings {
		if s.Key == "vcs.revision" {
			a.VCSRevision = s.Value
		}
		if s.Key == "vcs.modified" {
			a.VCSModified = s.Value
		}
	}
	if a.VCSRevision != "absent" && (len(a.VCSRevision) != 40 || strings.Trim(a.VCSRevision, "0123456789abcdef") != "") {
		return buildAssertion{}, errRefused
	}
	if a.VCSModified != "absent" && a.VCSModified != "true" && a.VCSModified != "false" {
		return buildAssertion{}, errRefused
	}
	return a, nil
}

func inventoryDigest(path string, required bool) (string, error) {
	if path == "" {
		if required {
			return "", errRefused
		}
		return "not_supplied", nil
	}
	raw, e := os.ReadFile(path)
	if e != nil || len(raw) == 0 {
		return "", errRefused
	}
	if raw[len(raw)-1] != '\n' {
		return "", errRefused
	}
	previous := ""
	for _, line := range strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n") {
		parts := strings.Split(line, "\x00")
		if len(parts) != 3 || !safeRelative(parts[0]) || parts[0] <= previous || (parts[1] != "100644" && parts[1] != "100755") || !hex64RE.MatchString(parts[2]) {
			return "", errRefused
		}
		previous = parts[0]
	}
	return bytesSHA(raw), nil
}

func marshalManifest(m manifest) ([]byte, error) {
	raw, e := json.Marshal(m)
	if e != nil || len(raw)+1 > maxManifest {
		return nil, errRefused
	}
	return append(raw, '\n'), nil
}

func readManifest(path string) (manifest, []byte, error) {
	raw, e := os.ReadFile(path)
	if e != nil || len(raw) == 0 || len(raw) > maxManifest {
		return manifest{}, nil, errRefused
	}
	var m manifest
	if strictDecode(raw, &m) != nil || m.Schema != manifestSchema {
		return manifest{}, nil, errRefused
	}
	canon, e := marshalManifest(m)
	if e != nil || string(canon) != string(raw) {
		return manifest{}, nil, errRefused
	}
	return m, raw, nil
}

func validateManifest(m manifest) error {
	hexes := []string{m.Profile.CodecSHA256, m.Profile.SearchSHA256, m.Profile.BehaviorSHA256, m.Profile.DiagnosticSHA256, m.Operational.ConfigSHA256, m.Operational.ExpectedBaseSHA256, m.Recorded.Source.SHA256, m.Recorded.Projected.LinesSHA256, m.Recorded.MappingSHA256, m.Recorded.Artifact.ContainerSHA256, m.Reported.BaseSHA256, m.Reported.ContainerSHA256, m.Reported.ConfigDeclared, m.Reported.ConfigRecomputed, m.Reported.AnalyzerDeclared, m.Reported.AnalyzerRecomputed, m.Reported.InputSHA256}
	for _, v := range hexes {
		if !hex64RE.MatchString(v) {
			return errRefused
		}
	}
	if m.Schema != manifestSchema || m.Profile.Name != "parts-catalog-suggest" || m.Profile.Version != "v1" || m.Profile.DiagnosticFile != "profile-diagnostic.json" || m.Operational.PhysicalListName != listName || m.Operational.ConfigSHA256 != configSHA || !localproof.EngineLabelAdmitted(m.Operational.TestedEngine) || len(m.Operational.TestedEngine) > 128 || m.Operational.TimeoutMillis != 30000 || !validMaxInflight(m.Operational.MaxInflight) || (m.Operational.MaxQueue != 0 && !validMaxQueue(m.Operational.MaxQueue)) || m.Operational.WorstCaseChargeBytes != 1073741824 || m.Operational.Admission != "prepared_full" || m.Operational.ExpectedEntries < 1 || m.Operational.ExpectedEntries > maxRecords || m.Recorded.Source.Bytes < 1 || m.Recorded.Source.Bytes > maxSourceBytes || m.Recorded.Source.Records < 1 || m.Recorded.Source.Records > maxRecords || m.Recorded.Source.Tenants < 1 || m.Recorded.Source.Tenants > maxTenants || m.Recorded.Projected.Keys < 1 || m.Recorded.Projected.Keys > maxExactInteger || m.Recorded.MappingSHA256 != bytesSHA(mappingBytes) || m.Recorded.Artifact.Path != "data/"+listName+".kurn" || m.Reported.VerifyLevel != "full + semantic" || m.Reported.List != listName || m.Reported.Mode != "ngram" || m.Reported.Format != "KURNART1 1.1" || m.Reported.Entries < 1 || m.Reported.Entries > maxRecords || m.Reported.Keys > maxExactInteger || m.Reported.DroppedKeys != 0 || m.Reported.KeylessEntries != 0 || m.Reported.Collapsed != 0 || m.Reported.ContainerSHA256 != m.Recorded.Artifact.ContainerSHA256 || m.Reported.InputSHA256 != m.Recorded.Projected.LinesSHA256 || m.Reported.Entries != uint64(m.Recorded.Source.Records) || m.Reported.Keys != uint64(m.Recorded.Projected.Keys) || m.Reported.ConfigDeclared != configSHA || m.Reported.ConfigRecomputed != configSHA || m.Reported.AnalyzerDeclared != analyzerSHA || m.Reported.AnalyzerRecomputed != analyzerSHA || m.Operational.ExpectedBaseSHA256 != m.Reported.BaseSHA256 || m.Operational.ExpectedEntries != m.Reported.Entries || len(m.Recorded.Files) < 1 || len(m.Recorded.Files) > 32 || m.Readiness.Tenant == "" || m.Readiness.Partition == "" || m.Readiness.Query == "" || m.Readiness.ExpectedID == "" {
		return errRefused
	}
	if !validPolicy(m.Profile.Policy) {
		return errRefused
	}
	descriptor, err := descriptorFor(m.Operational.MaxInflight, m.Profile.Policy, m.Operational.TestedEngine)
	if err != nil {
		return errRefused
	}
	identities := descriptor.Identities()
	if m.Profile.CodecSHA256 != identities.Codec.Hex() || m.Profile.SearchSHA256 != identities.Search.Hex() || m.Profile.BehaviorSHA256 != identities.Behavior.Hex() || m.Profile.DiagnosticSHA256 != bytesSHA(descriptor.DiagnosticJSON()) {
		return errRefused
	}
	executableNames := []string{"kurn", "kurnd", "kurn-searchd", "kurn-search-local"}
	if len(m.Recorded.Executables) != len(executableNames) || len(m.Asserted.Executables) != len(executableNames) {
		return errRefused
	}
	for _, name := range executableNames {
		r, rok := m.Recorded.Executables[name]
		a, aok := m.Asserted.Executables[name]
		if !rok || !aok || !hex64RE.MatchString(r.SHA256) || r.Bytes < 1 || r.Bytes > maxExactInteger || a.GoVersion == "" || a.MainPath == "" || a.Path == "" || !validVCSRevision(a.VCSRevision) || (a.VCSModified != "true" && a.VCSModified != "false" && a.VCSModified != "absent") {
			return errRefused
		}
	}
	contractNames := []string{"openapi_manifest_sha256", "clients_manifest_sha256", "typescript_client_mjs_sha256", "typescript_client_dts_sha256"}
	if len(m.Recorded.SearchContract) != len(contractNames) {
		return errRefused
	}
	for _, name := range contractNames {
		if !hex64RE.MatchString(m.Recorded.SearchContract[name]) {
			return errRefused
		}
	}
	if len(m.Recorded.Files) != len(immutableFileModes) {
		return errRefused
	}
	for name, id := range m.Recorded.Files {
		expectedMode, ok := immutableFileModes[name]
		if !ok || !safeRelative(name) || !hex64RE.MatchString(id.SHA256) || id.Mode != expectedMode {
			return errRefused
		}
	}
	if !inventoryIdentity(m.Asserted.CoreInventorySHA256) || !inventoryIdentity(m.Asserted.SearchInventorySHA256) {
		return errRefused
	}
	return nil
}

func validVCSRevision(value string) bool {
	return value == "absent" || (len(value) == 40 && strings.Trim(value, "0123456789abcdef") == "")
}
func inventoryIdentity(value string) bool {
	return value == "not_supplied" || hex64RE.MatchString(value)
}

func safeRelative(name string) bool {
	return name != "" && !filepath.IsAbs(name) && filepath.Clean(name) == name && name != "." && !strings.HasPrefix(name, ".."+string(filepath.Separator))
}
