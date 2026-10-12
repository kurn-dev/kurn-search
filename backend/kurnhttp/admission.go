package kurnhttp

import (
	"encoding/json"
	"net/http"
	"strconv"
)

// AdmissionPolicy selects the evidence every successful Batch must carry
// before its results are admitted. The zero value is the legacy behavior.
type AdmissionPolicy uint8

const (
	// AdmissionLegacy admits exactly what the client admitted before
	// admission policies existed: verification evidence is ignored.
	AdmissionLegacy AdmissionPolicy = 0
	// AdmissionPreparedFull admits a successful Batch only when the backend
	// serves one immutable prepared artifact under full verification, with
	// the expected base identity and entry count and no journal, on every
	// result of every Batch (including empty results).
	AdmissionPreparedFull AdmissionPolicy = 1
)

const (
	artifactVerificationHeader = "Kurn-Artifact-Verification"
	artifactVerificationFull   = "full"
	versionBasesMember         = "version_bases"
	verifiedBaseBasis          = "verified-base-id"
	maxPreparedEntries         = 100_000
)

// admission is the validated, immutable policy a Client applies. Its zero
// value is AdmissionLegacy with no expectations.
type admission struct {
	policy     AdmissionPolicy
	baseSHA256 string
	entries    uint64
}

// newAdmission validates the closed policy enum and its expectations. Legacy
// carries no expectation at all; PreparedFull requires a lowercase hex64 base
// and a bounded, nonzero entry count.
func newAdmission(policy AdmissionPolicy, baseSHA256 string, entries uint64) (admission, bool) {
	switch policy {
	case AdmissionLegacy:
		if baseSHA256 != "" || entries != 0 {
			return admission{}, false
		}
		return admission{}, true
	case AdmissionPreparedFull:
		if !hex64RE.MatchString(baseSHA256) || entries < 1 || entries > maxPreparedEntries {
			return admission{}, false
		}
		return admission{policy: AdmissionPreparedFull, baseSHA256: baseSHA256, entries: entries}, true
	default:
		return admission{}, false
	}
}

func (a admission) preparedFull() bool { return a.policy == AdmissionPreparedFull }

func artifactVerificationMismatch() *Error {
	return classified(CategoryArtifactVerificationMismatch, http.StatusOK, nil)
}

// admitHeader runs on a 200 response before its body is decoded. Exactly one
// header value, exactly "full", is required: absent, duplicated, conflicting,
// "trusted" or any other value refuses.
func (a admission) admitHeader(header http.Header) error {
	if !a.preparedFull() {
		return nil
	}
	values := header.Values(artifactVerificationHeader)
	if len(values) != 1 || values[0] != artifactVerificationFull {
		return artifactVerificationMismatch()
	}
	return nil
}

// admitStamp runs after the existing versions/stamp/config checks. The served
// version must be the expected immutable base with the expected entry count
// and an empty journal, spelled canonically (no leading-zero entry count).
func (a admission) admitStamp(stamp Stamp) error {
	if !a.preparedFull() {
		return nil
	}
	canonical := a.baseSHA256 + "@" + strconv.FormatUint(a.entries, 10) + "+j0+c" + stamp.ConfigSHA256
	if stamp.BaseSHA256 != a.baseSHA256 || stamp.Entries != a.entries ||
		stamp.JournalBytes != 0 || stamp.JournalSHA256 != "" || stamp.Raw != canonical {
		return artifactVerificationMismatch()
	}
	return nil
}

// admitVersionBases runs after admitStamp. version_bases is required: an
// object with exactly one member, named by the probe list, whose value is
// exactly the verified-base basis. Missing, null, malformed, extra,
// duplicate, wrong-list, declared or unknown evidence refuses.
func (a admission) admitVersionBases(result map[string]json.RawMessage, list string) error {
	if !a.preparedFull() {
		return nil
	}
	raw, ok := result[versionBasesMember]
	if !ok {
		return artifactVerificationMismatch()
	}
	bases, err := object(raw)
	if err != nil || len(bases) != 1 {
		return artifactVerificationMismatch()
	}
	var basis string
	if err := required(bases, list, &basis); err != nil || basis != verifiedBaseBasis {
		return artifactVerificationMismatch()
	}
	return nil
}
