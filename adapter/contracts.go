// Package adapter defines immutable pure semantic contracts for paired Kurn
// search adapters. It performs no transport, orchestration, authentication,
// ranking, or domain-specific interpretation.
package adapter

import (
	"bytes"
	"encoding/json"
	"math"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

// Common hard ceilings. Profiles may select smaller nonzero bounds.
const (
	MaxEntryIDBytes          = 512
	MaxPhysicalKeys          = 256
	MaxLogicalTermRunes      = 512
	MaxPhysicalKeyRunes      = 512
	MaxOpaqueJSONBytes       = 1 << 20
	MaxProbes                = 100
	MaxAnyOfKeys             = 64
	MaxPool                  = 1000
	MaxCandidatesPerProbe    = 1000
	MaxEvidenceCandidates    = MaxProbes * MaxCandidatesPerProbe
	MaxHits                  = 1000
	MaxEvidenceKinds         = 16
	MaxPhysicalListRoles     = 16
	MaxIdentityArtifactBytes = 1 << 20
)

var contractTokenRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// EvidenceKind is an adapter-declared closed evidence vocabulary member.
type EvidenceKind string

// ListRole is an adapter-declared physical-list role, never a caller-provided
// Kurn list name.
type ListRole string

func validToken(value string) bool { return contractTokenRE.MatchString(value) }

type ProjectedEntrySpec struct {
	EntryID string
	Keys    []string
	Payload []byte
}

// ProjectedEntry is one immutable source-projection output.
type ProjectedEntry struct {
	entryID string
	keys    []string
	payload []byte
	valid   bool
}

func NewProjectedEntry(spec ProjectedEntrySpec) (ProjectedEntry, error) {
	if !validEntryID(spec.EntryID) || len(spec.Keys) < 1 || len(spec.Keys) > MaxPhysicalKeys ||
		!validOpaqueJSON(spec.Payload, true) {
		return ProjectedEntry{}, invalidContract()
	}
	keys := append([]string(nil), spec.Keys...)
	seen := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		if !validBoundedString(key, MaxPhysicalKeyRunes) {
			return ProjectedEntry{}, invalidContract()
		}
		if _, exists := seen[key]; exists {
			return ProjectedEntry{}, invalidContract()
		}
		seen[key] = struct{}{}
	}
	return ProjectedEntry{
		entryID: spec.EntryID,
		keys:    keys,
		payload: bytes.Clone(spec.Payload),
		valid:   true,
	}, nil
}

func (e ProjectedEntry) EntryID() string { return e.entryID }
func (e ProjectedEntry) Keys() []string  { return append([]string(nil), e.keys...) }
func (e ProjectedEntry) Payload() []byte { return bytes.Clone(e.payload) }
func (e ProjectedEntry) Valid() bool     { return e.valid }

type ProbeSpec struct {
	Kind        EvidenceKind
	LogicalTerm string
	PhysicalKey string
	// PhysicalKeys makes the probe an any-of lookup: 1–MaxAnyOfKeys
	// distinct keys, each looked up by exact equality, in place of one
	// scored PhysicalKey. Exactly one of PhysicalKey and PhysicalKeys is
	// set.
	PhysicalKeys []string
	ListRole     ListRole
}

// Probe is one immutable pure-plan element. Attempt-wide pool, threshold, and
// filter policy deliberately do not live here.
type Probe struct {
	kind         EvidenceKind
	logicalTerm  string
	physicalKey  string
	physicalKeys []string
	listRole     ListRole
	valid        bool
}

func NewProbe(spec ProbeSpec) (Probe, error) {
	if !validToken(string(spec.Kind)) || !validToken(string(spec.ListRole)) ||
		!validBoundedString(spec.LogicalTerm, MaxLogicalTermRunes) {
		return Probe{}, invalidContract()
	}
	if len(spec.PhysicalKeys) > 0 {
		if spec.PhysicalKey != "" || len(spec.PhysicalKeys) > MaxAnyOfKeys {
			return Probe{}, invalidContract()
		}
		seen := make(map[string]struct{}, len(spec.PhysicalKeys))
		for _, key := range spec.PhysicalKeys {
			if !validBoundedString(key, MaxPhysicalKeyRunes) {
				return Probe{}, invalidContract()
			}
			if _, dup := seen[key]; dup {
				return Probe{}, invalidContract()
			}
			seen[key] = struct{}{}
		}
	} else if !validBoundedString(spec.PhysicalKey, MaxPhysicalKeyRunes) {
		return Probe{}, invalidContract()
	}
	return Probe{
		kind: spec.Kind, logicalTerm: spec.LogicalTerm,
		physicalKey: spec.PhysicalKey, physicalKeys: append([]string(nil), spec.PhysicalKeys...),
		listRole: spec.ListRole, valid: true,
	}, nil
}

func (p Probe) Kind() EvidenceKind  { return p.kind }
func (p Probe) LogicalTerm() string { return p.logicalTerm }
func (p Probe) PhysicalKey() string { return p.physicalKey }

// PhysicalKeys returns the any-of keys (nil for a single-key probe).
func (p Probe) PhysicalKeys() []string { return append([]string(nil), p.physicalKeys...) }
func (p Probe) ListRole() ListRole     { return p.listRole }
func (p Probe) Valid() bool            { return p.valid }

// SameProbe reports whether two probes are identical in every plan-bearing
// field, including any-of keys in order.
func SameProbe(left, right Probe) bool {
	return left.valid && right.valid && left.kind == right.kind &&
		left.logicalTerm == right.logicalTerm && left.physicalKey == right.physicalKey &&
		left.listRole == right.listRole && slices.Equal(left.physicalKeys, right.physicalKeys)
}

// CompiledPlan is an immutable ordered probe sequence.
type CompiledPlan struct {
	probes []Probe
	valid  bool
}

func NewCompiledPlan(probes []Probe) (CompiledPlan, error) {
	if len(probes) < 1 || len(probes) > MaxProbes {
		return CompiledPlan{}, invalidContract()
	}
	copyProbes := append([]Probe(nil), probes...)
	seen := make(map[probeIdentity]struct{}, len(copyProbes))
	for _, probe := range copyProbes {
		if !probe.valid {
			return CompiledPlan{}, invalidContract()
		}
		identity := probeIdentity{probe.kind, probe.logicalTerm, probe.physicalKey, strings.Join(probe.physicalKeys, "\x00"), probe.listRole}
		if _, exists := seen[identity]; exists {
			return CompiledPlan{}, invalidContract()
		}
		seen[identity] = struct{}{}
	}
	return CompiledPlan{probes: copyProbes, valid: true}, nil
}

func (p CompiledPlan) Probes() []Probe { return append([]Probe(nil), p.probes...) }
func (p CompiledPlan) Valid() bool     { return p.valid }

type probeIdentity struct {
	kind         EvidenceKind
	logicalTerm  string
	physicalKey  string
	physicalKeys string
	listRole     ListRole
}

type CandidateSpec struct {
	EntryID   string
	Score     float64
	EchoedKey string
	Payload   []byte
}

// Candidate is the immutable adapter-visible candidate tuple. The concrete
// resolver, not this common contract, interprets score, key, and payload.
type Candidate struct {
	entryID   string
	score     float64
	echoedKey string
	payload   []byte
	valid     bool
}

func NewCandidate(spec CandidateSpec) (Candidate, error) {
	if !validEntryID(spec.EntryID) || math.IsNaN(spec.Score) || math.IsInf(spec.Score, 0) ||
		!validBoundedString(spec.EchoedKey, MaxPhysicalKeyRunes) ||
		!validOpaqueJSON(spec.Payload, true) {
		return Candidate{}, invalidContract()
	}
	return Candidate{
		entryID: spec.EntryID, score: spec.Score, echoedKey: spec.EchoedKey,
		payload: bytes.Clone(spec.Payload), valid: true,
	}, nil
}

func (c Candidate) EntryID() string   { return c.entryID }
func (c Candidate) Score() float64    { return c.score }
func (c Candidate) EchoedKey() string { return c.echoedKey }
func (c Candidate) Payload() []byte   { return bytes.Clone(c.payload) }
func (c Candidate) Valid() bool       { return c.valid }

type ProbeEvidenceSpec struct {
	Probe         Probe
	RequestedPool int
	Candidates    []Candidate
}

// ProbeEvidence binds one requested probe and attempt-wide pool rung to the
// ordered candidate tuples observed for it.
type ProbeEvidence struct {
	probe         Probe
	requestedPool int
	candidates    []Candidate
	valid         bool
}

func NewProbeEvidence(spec ProbeEvidenceSpec) (ProbeEvidence, error) {
	if !spec.Probe.valid || spec.RequestedPool < 1 || spec.RequestedPool > MaxPool ||
		len(spec.Candidates) > MaxCandidatesPerProbe {
		return ProbeEvidence{}, invalidContract()
	}
	candidates := append([]Candidate(nil), spec.Candidates...)
	for _, candidate := range candidates {
		if !candidate.valid {
			return ProbeEvidence{}, invalidContract()
		}
	}
	return ProbeEvidence{
		probe: spec.Probe, requestedPool: spec.RequestedPool,
		candidates: candidates, valid: true,
	}, nil
}

func (e ProbeEvidence) Probe() Probe            { return e.probe }
func (e ProbeEvidence) RequestedPool() int      { return e.requestedPool }
func (e ProbeEvidence) Candidates() []Candidate { return append([]Candidate(nil), e.candidates...) }
func (e ProbeEvidence) Valid() bool             { return e.valid }

type ResolvedHitSpec struct {
	EntryID  string
	Result   []byte
	Evidence []byte
}

// ResolvedHit is an immutable adapter-owned result projection.
type ResolvedHit struct {
	entryID  string
	result   []byte
	evidence []byte
	valid    bool
}

func NewResolvedHit(spec ResolvedHitSpec) (ResolvedHit, error) {
	if !validEntryID(spec.EntryID) || !validOpaqueJSON(spec.Result, true) ||
		(len(spec.Evidence) != 0 && !validOpaqueJSON(spec.Evidence, true)) {
		return ResolvedHit{}, invalidContract()
	}
	return ResolvedHit{
		entryID: spec.EntryID, result: bytes.Clone(spec.Result),
		evidence: bytes.Clone(spec.Evidence), valid: true,
	}, nil
}

func (h ResolvedHit) EntryID() string  { return h.entryID }
func (h ResolvedHit) Result() []byte   { return bytes.Clone(h.result) }
func (h ResolvedHit) Evidence() []byte { return bytes.Clone(h.evidence) }
func (h ResolvedHit) Valid() bool      { return h.valid }

type ResolutionSpec struct {
	Complete bool
	Hits     []ResolvedHit
}

// Resolution is a complete immutable pure resolution. Fail-closed incomplete
// evidence cannot be represented as a successful value.
type Resolution struct {
	hits  []ResolvedHit
	valid bool
}

func NewResolution(spec ResolutionSpec) (Resolution, error) {
	if !spec.Complete {
		return Resolution{}, incomplete()
	}
	if len(spec.Hits) > MaxHits {
		return Resolution{}, invalidContract()
	}
	hits := append([]ResolvedHit(nil), spec.Hits...)
	seen := make(map[string]struct{}, len(hits))
	for _, hit := range hits {
		if !hit.valid {
			return Resolution{}, invalidContract()
		}
		if _, exists := seen[hit.entryID]; exists {
			return Resolution{}, invalidContract()
		}
		seen[hit.entryID] = struct{}{}
	}
	return Resolution{hits: hits, valid: true}, nil
}

func (r Resolution) Complete() bool      { return r.valid }
func (r Resolution) Hits() []ResolvedHit { return append([]ResolvedHit(nil), r.hits...) }
func (r Resolution) Valid() bool         { return r.valid }

// Projector, Compiler, and Resolver are deliberately synchronous pure
// semantic halves. The later runtime owns I/O, cancellation, and admission.
type Projector interface {
	Project(source []byte) ([]ProjectedEntry, error)
}

type Compiler interface {
	Compile(request, trustedScope []byte) (CompiledPlan, error)
}

type Resolver interface {
	Resolve(request, trustedScope []byte, plan CompiledPlan, evidence []ProbeEvidence) (Resolution, error)
}

// FallbackCompiler is the optional second step of a plan. After the first
// step's evidence is observed, CompileFallback either returns a further plan
// to run in the same attempt, under the same pool and filter (true), or
// declines (false). It is pure and deterministic in its inputs, so the
// resolver recomputes it to check the plan it is handed; an error means the
// plan or evidence is not the one this adapter compiled.
type FallbackCompiler interface {
	CompileFallback(request, trustedScope []byte, plan CompiledPlan, evidence []ProbeEvidence) (CompiledPlan, bool, error)
}

type Semantics interface {
	Projector
	Compiler
	Resolver
}

func validEntryID(value string) bool {
	return utf8.ValidString(value) && value != "" && len(value) <= MaxEntryIDBytes
}

func validBoundedString(value string, maximumRunes int) bool {
	return utf8.ValidString(value) && value != "" && utf8.RuneCountInString(value) <= maximumRunes
}

func validOpaqueJSON(raw []byte, nonNull bool) bool {
	if len(raw) == 0 || len(raw) > MaxOpaqueJSONBytes || !utf8.Valid(raw) || !json.Valid(raw) {
		return false
	}
	return !nonNull || !bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}
