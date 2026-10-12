package profile

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"unicode/utf8"

	"github.com/kurn-dev/kurn-search/adapter"
)

const diagnosticSchema = "kurn-search-profile/v1"

type descriptorWire struct {
	Schema         string              `json:"schema"`
	Name           string              `json:"name"`
	Version        string              `json:"version"`
	Policy         string              `json:"policy,omitempty"`
	SourceSchema   string              `json:"source_schema"`
	RequestSchema  string              `json:"request_schema"`
	ResultSchema   string              `json:"result_schema"`
	EvidenceKinds  []string            `json:"evidence_kinds"`
	Identities     identitiesWire      `json:"identities"`
	Analyzer       AnalyzerAuthorities `json:"analyzer"`
	GoToolchain    string              `json:"go_toolchain"`
	Modules        []ModuleAuthority   `json:"modules"`
	TestedEngines  []string            `json:"tested_engine_attestations"`
	Lists          []physicalListWire  `json:"lists"`
	TrustedFilters []string            `json:"trusted_filters"`
	CallerFilters  []string            `json:"caller_filters"`
	Bounds         boundsWire          `json:"bounds"`
	Completeness   string              `json:"completeness"`
}

type identitiesWire struct {
	Codec    string `json:"codec_sha256"`
	Search   string `json:"search_sha256"`
	Behavior string `json:"behavior_sha256"`
}

type physicalListWire struct {
	Role                string  `json:"role"`
	Name                string  `json:"name"`
	ConfigSHA256        string  `json:"config_sha256"`
	ThresholdMillionths *uint32 `json:"threshold_millionths,omitempty"`
}

type boundsWire struct {
	MaxSourceBytes           int    `json:"max_source_bytes"`
	MaxRequestBytes          int    `json:"max_request_bytes"`
	MaxTrustedScopeBytes     int    `json:"max_trusted_scope_bytes"`
	MaxProjectedEntries      int    `json:"max_projected_entries"`
	MaxKeysPerEntry          int    `json:"max_keys_per_entry"`
	MaxPayloadBytes          int    `json:"max_payload_bytes"`
	MaxQueryRunes            int    `json:"max_query_runes"`
	MaxProbes                int    `json:"max_probes"`
	MaxPhysicalKeyRunes      int    `json:"max_physical_key_runes"`
	PoolLadder               []int  `json:"pool_ladder"`
	DefaultResultLimit       int    `json:"default_result_limit"`
	MaxResultLimit           int    `json:"max_result_limit"`
	MaxCandidatesPerProbe    int    `json:"max_candidates_per_probe"`
	MaxEvidenceCandidates    int    `json:"max_evidence_candidates"`
	MaxResultBytes           int    `json:"max_result_bytes"`
	MaxResponseBytes         int    `json:"max_response_bytes"`
	MaxIdentityArtifactBytes int    `json:"max_identity_artifact_bytes"`
	VersionAttempts          int    `json:"version_attempts"`
	TimeoutMillis            uint64 `json:"timeout_millis"`
	MaxInflight              int    `json:"max_inflight"`
	WorstCaseChargeBytes     uint64 `json:"worst_case_charge_bytes"`
}

func encodeDiagnostic(spec Spec, identities Identities) ([]byte, error) {
	wire := wireFromSpec(spec, identities)
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(wire); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

// ParseDiagnostic accepts only the exact deterministic diagnostic form and
// recomputes every behavior identity from the separately supplied artifacts.
func ParseDiagnostic(raw []byte, artifacts BehaviorArtifacts) (Descriptor, error) {
	if len(raw) == 0 || len(raw) > HardMaxResponseBytes || !utf8.Valid(raw) || !json.Valid(raw) || rejectDuplicateNames(raw) != nil {
		return Descriptor{}, invalidProfile()
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var wire descriptorWire
	if err := decoder.Decode(&wire); err != nil {
		return Descriptor{}, invalidProfile()
	}
	if err := requireEOF(decoder); err != nil {
		return Descriptor{}, invalidProfile()
	}
	spec, claimed, ok := specFromWire(wire)
	if !ok {
		return Descriptor{}, invalidProfile()
	}
	descriptor, err := NewDescriptor(spec, artifacts)
	if err != nil {
		return Descriptor{}, err
	}
	identities := descriptor.Identities()
	if !identities.Codec.Equal(claimed.Codec) || !identities.Search.Equal(claimed.Search) ||
		!identities.Behavior.Equal(claimed.Behavior) {
		return Descriptor{}, identityMismatch()
	}
	if !bytes.Equal(raw, descriptor.DiagnosticJSON()) {
		return Descriptor{}, invalidProfile()
	}
	return descriptor, nil
}

func wireFromSpec(spec Spec, identities Identities) descriptorWire {
	kinds := make([]string, len(spec.EvidenceKinds))
	for index, kind := range spec.EvidenceKinds {
		kinds[index] = string(kind)
	}
	lists := make([]physicalListWire, len(spec.Lists))
	for index, list := range spec.Lists {
		lists[index] = physicalListWire{
			Role: string(list.Role), Name: list.Name, ConfigSHA256: list.ConfigSHA256,
			ThresholdMillionths: list.ThresholdMillionths,
		}
	}
	return descriptorWire{
		Schema: diagnosticSchema, Name: spec.Name, Version: spec.Version, Policy: spec.Policy,
		SourceSchema: spec.SourceSchema, RequestSchema: spec.RequestSchema,
		ResultSchema: spec.ResultSchema, EvidenceKinds: kinds,
		Identities: identitiesWire{
			Codec: identities.Codec.Hex(), Search: identities.Search.Hex(), Behavior: identities.Behavior.Hex(),
		},
		Analyzer: spec.Analyzer, GoToolchain: spec.GoToolchain,
		Modules: spec.Modules, TestedEngines: spec.TestedEngines, Lists: lists,
		TrustedFilters: spec.TrustedFilters, CallerFilters: spec.CallerFilters,
		Bounds: boundsToWire(spec.Bounds), Completeness: "fail_closed",
	}
}

func specFromWire(wire descriptorWire) (Spec, Identities, bool) {
	if wire.Schema != diagnosticSchema || wire.Completeness != "fail_closed" {
		return Spec{}, Identities{}, false
	}
	codec, err := adapter.ParseDigestHex(wire.Identities.Codec)
	if err != nil {
		return Spec{}, Identities{}, false
	}
	search, err := adapter.ParseDigestHex(wire.Identities.Search)
	if err != nil {
		return Spec{}, Identities{}, false
	}
	behavior, err := adapter.ParseDigestHex(wire.Identities.Behavior)
	if err != nil {
		return Spec{}, Identities{}, false
	}
	kinds := make([]adapter.EvidenceKind, len(wire.EvidenceKinds))
	for index, kind := range wire.EvidenceKinds {
		kinds[index] = adapter.EvidenceKind(kind)
	}
	lists := make([]PhysicalListSpec, len(wire.Lists))
	for index, list := range wire.Lists {
		lists[index] = PhysicalListSpec{
			Role: adapter.ListRole(list.Role), Name: list.Name,
			ConfigSHA256: list.ConfigSHA256, ThresholdMillionths: list.ThresholdMillionths,
		}
	}
	return Spec{
		Name: wire.Name, Version: wire.Version, Policy: wire.Policy, SourceSchema: wire.SourceSchema,
		RequestSchema: wire.RequestSchema, ResultSchema: wire.ResultSchema,
		EvidenceKinds: kinds, Analyzer: wire.Analyzer, GoToolchain: wire.GoToolchain,
		Modules: wire.Modules, TestedEngines: wire.TestedEngines, Lists: lists,
		TrustedFilters: wire.TrustedFilters, CallerFilters: wire.CallerFilters,
		Bounds: boundsFromWire(wire.Bounds),
	}, Identities{Codec: codec, Search: search, Behavior: behavior}, true
}

func boundsToWire(bounds Bounds) boundsWire {
	return boundsWire{
		MaxSourceBytes: bounds.MaxSourceBytes, MaxRequestBytes: bounds.MaxRequestBytes,
		MaxTrustedScopeBytes: bounds.MaxTrustedScopeBytes, MaxProjectedEntries: bounds.MaxProjectedEntries,
		MaxKeysPerEntry: bounds.MaxKeysPerEntry, MaxPayloadBytes: bounds.MaxPayloadBytes,
		MaxQueryRunes: bounds.MaxQueryRunes, MaxProbes: bounds.MaxProbes,
		MaxPhysicalKeyRunes: bounds.MaxPhysicalKeyRunes, PoolLadder: bounds.PoolLadder,
		DefaultResultLimit: bounds.DefaultResultLimit, MaxResultLimit: bounds.MaxResultLimit,
		MaxCandidatesPerProbe: bounds.MaxCandidatesPerProbe,
		MaxEvidenceCandidates: bounds.MaxEvidenceCandidates, MaxResultBytes: bounds.MaxResultBytes,
		MaxResponseBytes:         bounds.MaxResponseBytes,
		MaxIdentityArtifactBytes: bounds.MaxIdentityArtifactBytes,
		VersionAttempts:          bounds.VersionAttempts, TimeoutMillis: bounds.TimeoutMillis,
		MaxInflight: bounds.MaxInflight, WorstCaseChargeBytes: bounds.WorstCaseChargeBytes,
	}
}

func boundsFromWire(wire boundsWire) Bounds {
	return Bounds{
		MaxSourceBytes: wire.MaxSourceBytes, MaxRequestBytes: wire.MaxRequestBytes,
		MaxTrustedScopeBytes: wire.MaxTrustedScopeBytes, MaxProjectedEntries: wire.MaxProjectedEntries,
		MaxKeysPerEntry: wire.MaxKeysPerEntry, MaxPayloadBytes: wire.MaxPayloadBytes,
		MaxQueryRunes: wire.MaxQueryRunes, MaxProbes: wire.MaxProbes,
		MaxPhysicalKeyRunes: wire.MaxPhysicalKeyRunes,
		PoolLadder:          append([]int(nil), wire.PoolLadder...),
		DefaultResultLimit:  wire.DefaultResultLimit, MaxResultLimit: wire.MaxResultLimit,
		MaxCandidatesPerProbe: wire.MaxCandidatesPerProbe,
		MaxEvidenceCandidates: wire.MaxEvidenceCandidates, MaxResultBytes: wire.MaxResultBytes,
		MaxResponseBytes:         wire.MaxResponseBytes,
		MaxIdentityArtifactBytes: wire.MaxIdentityArtifactBytes,
		VersionAttempts:          wire.VersionAttempts, TimeoutMillis: wire.TimeoutMillis,
		MaxInflight: wire.MaxInflight, WorstCaseChargeBytes: wire.WorstCaseChargeBytes,
	}
}

func rejectDuplicateNames(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := walkValue(decoder); err != nil {
		return err
	}
	return requireEOF(decoder)
}

func walkValue(decoder *json.Decoder) error {
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
				return errors.New("object member is not a string")
			}
			if _, exists := seen[name]; exists {
				return errors.New("duplicate object member")
			}
			seen[name] = struct{}{}
			if err := walkValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return errors.New("object is not closed")
		}
	case '[':
		for decoder.More() {
			if err := walkValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return errors.New("array is not closed")
		}
	default:
		return errors.New("unknown delimiter")
	}
	return nil
}

func requireEOF(decoder *json.Decoder) error {
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("trailing JSON")
		}
		return err
	}
	return nil
}
