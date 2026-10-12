package adapter

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"regexp"
)

const (
	MaxIdentityComponents  = 16
	MaxIdentityLabelBytes  = 32
	MaxIdentitySchemaBytes = 64
)

type Domain string

const (
	DomainComponent Domain = "kurn-search/component/v1"
	DomainCodec     Domain = "kurn-search/codec/v1"
	DomainSearch    Domain = "kurn-search/search/v1"
	DomainBehavior  Domain = "kurn-search/behavior/v1"
)

const DigestSchema = "sha256/v1"

var (
	identityLabelRE  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)
	identitySchemaRE = regexp.MustCompile(`^[a-z0-9][a-z0-9./-]{0,63}$`)
)

type ComponentSpec struct {
	Label    string
	Schema   string
	Artifact []byte
}

// Component is one immutable identity-frame component.
type Component struct {
	label    string
	schema   string
	artifact []byte
	valid    bool
}

func NewComponent(spec ComponentSpec) (Component, error) {
	if !identityLabelRE.MatchString(spec.Label) || !identitySchemaRE.MatchString(spec.Schema) ||
		len(spec.Artifact) < 1 || len(spec.Artifact) > MaxIdentityArtifactBytes {
		return Component{}, invalidContract()
	}
	return Component{
		label: spec.Label, schema: spec.Schema,
		artifact: bytes.Clone(spec.Artifact), valid: true,
	}, nil
}

func (c Component) Label() string    { return c.label }
func (c Component) Schema() string   { return c.schema }
func (c Component) Artifact() []byte { return bytes.Clone(c.artifact) }
func (c Component) Valid() bool      { return c.valid }

// Digest is an immutable validated SHA-256 identity.
type Digest struct {
	sum   [sha256.Size]byte
	valid bool
}

func ParseDigestHex(value string) (Digest, error) {
	if len(value) != sha256.Size*2 {
		return Digest{}, identityMismatch()
	}
	raw, err := hex.DecodeString(value)
	if err != nil || hex.EncodeToString(raw) != value {
		return Digest{}, identityMismatch()
	}
	var sum [sha256.Size]byte
	copy(sum[:], raw)
	return Digest{sum: sum, valid: true}, nil
}

func PlainSHA256(raw []byte) Digest {
	return Digest{sum: sha256.Sum256(raw), valid: true}
}

func (d Digest) Hex() string {
	if !d.valid {
		return ""
	}
	return hex.EncodeToString(d.sum[:])
}

func (d Digest) Bytes() []byte {
	if !d.valid {
		return nil
	}
	return bytes.Clone(d.sum[:])
}

func (d Digest) Valid() bool { return d.valid }
func (d Digest) Equal(other Digest) bool {
	return d.valid && other.valid && d.sum == other.sum
}

// Frame emits the exact u64-big-endian length-framed identity bytes for one
// closed domain. Component order is part of the domain schema.
func Frame(domain Domain, components []Component) ([]byte, error) {
	if len(components) < 1 || len(components) > MaxIdentityComponents || !validDomain(domain) {
		return nil, invalidContract()
	}
	copyComponents := append([]Component(nil), components...)
	if !validComponentsForDomain(domain, copyComponents) {
		return nil, invalidContract()
	}

	capacity := 16 + len(domain)
	for _, component := range copyComponents {
		capacity += 24 + len(component.label) + len(component.schema) + len(component.artifact)
	}
	out := make([]byte, 0, capacity)
	out = appendField(out, []byte(domain))
	out = appendUint64(out, uint64(len(copyComponents)))
	for _, component := range copyComponents {
		out = appendField(out, []byte(component.label))
		out = appendField(out, []byte(component.schema))
		out = appendField(out, component.artifact)
	}
	return out, nil
}

func DigestFrame(domain Domain, components []Component) (Digest, error) {
	frame, err := Frame(domain, components)
	if err != nil {
		return Digest{}, err
	}
	return PlainSHA256(frame), nil
}

func ComponentDigest(component Component) (Digest, error) {
	return DigestFrame(DomainComponent, []Component{component})
}

func CodecDigest(sourceProjection, queryCodec Component) (Digest, error) {
	return DigestFrame(DomainCodec, []Component{sourceProjection, queryCodec})
}

func SearchDigest(compiledPlan, resolvedResult Component) (Digest, error) {
	return DigestFrame(DomainSearch, []Component{compiledPlan, resolvedResult})
}

func BehaviorDigest(analyzerAuthority, analyzerOutput Component, codec, search Digest) (Digest, error) {
	if !codec.valid || !search.valid {
		return Digest{}, identityMismatch()
	}
	codecComponent, _ := NewComponent(ComponentSpec{
		Label: "codec", Schema: DigestSchema, Artifact: codec.Bytes(),
	})
	searchComponent, _ := NewComponent(ComponentSpec{
		Label: "search", Schema: DigestSchema, Artifact: search.Bytes(),
	})
	return DigestFrame(DomainBehavior, []Component{
		analyzerAuthority, analyzerOutput, codecComponent, searchComponent,
	})
}

func validDomain(domain Domain) bool {
	switch domain {
	case DomainComponent, DomainCodec, DomainSearch, DomainBehavior:
		return true
	default:
		return false
	}
}

func validComponentsForDomain(domain Domain, components []Component) bool {
	for _, component := range components {
		if !component.valid || len(component.label) > MaxIdentityLabelBytes ||
			len(component.schema) > MaxIdentitySchemaBytes ||
			len(component.artifact) > MaxIdentityArtifactBytes {
			return false
		}
	}
	var labels []string
	switch domain {
	case DomainComponent:
		return len(components) == 1
	case DomainCodec:
		labels = []string{"source-projection", "query-codec"}
	case DomainSearch:
		labels = []string{"compiled-plan", "resolved-result"}
	case DomainBehavior:
		labels = []string{"analyzer-authority", "analyzer-output", "codec", "search"}
	default:
		return false
	}
	if len(labels) != len(components) {
		return false
	}
	for index, label := range labels {
		if components[index].label != label {
			return false
		}
	}
	if domain == DomainBehavior {
		for _, index := range []int{2, 3} {
			if components[index].schema != DigestSchema || len(components[index].artifact) != sha256.Size {
				return false
			}
		}
	}
	return true
}

func appendField(dst, field []byte) []byte {
	dst = appendUint64(dst, uint64(len(field)))
	return append(dst, field...)
}

func appendUint64(dst []byte, value uint64) []byte {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], value)
	return append(dst, encoded[:]...)
}
