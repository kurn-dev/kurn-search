package adapter

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"strings"
	"testing"
)

func TestIdentityFrameGoldenAndIndependentEncoding(t *testing.T) {
	source := mustComponent(t, "source-projection", "neutral/source/v1", []byte("source\n"))
	query := mustComponent(t, "query-codec", "neutral/query/v1", []byte("query\n"))
	frame, err := Frame(DomainCodec, []Component{source, query})
	if err != nil {
		t.Fatal(err)
	}
	wantFrame := independentFrame(string(DomainCodec), []Component{source, query})
	if !bytes.Equal(frame, wantFrame) {
		t.Fatal("frame differs from independent encoding")
	}
	digest, err := CodecDigest(source, query)
	if err != nil {
		t.Fatal(err)
	}
	const want = "a6ae2f140ee0ced89bcde88c29a5cbcb0a316e51a8908d1a4362554a1cecfc8c"
	if digest.Hex() != want {
		t.Fatalf("codec golden got %s want %s", digest.Hex(), want)
	}
}

func TestIdentityCompositionAndMutation(t *testing.T) {
	analyzerAuthority := mustComponent(t, "analyzer-authority", "neutral/analyzer-authority/v1", []byte("authority\n"))
	analyzerOutput := mustComponent(t, "analyzer-output", "neutral/analyzer-output/v1", []byte("output\n"))
	source := mustComponent(t, "source-projection", "neutral/source/v1", []byte("source\n"))
	query := mustComponent(t, "query-codec", "neutral/query/v1", []byte("query\n"))
	compiled := mustComponent(t, "compiled-plan", "neutral/compiled/v1", []byte("compiled\n"))
	resolved := mustComponent(t, "resolved-result", "neutral/resolved/v1", []byte("resolved\n"))
	codec, _ := CodecDigest(source, query)
	search, _ := SearchDigest(compiled, resolved)
	behavior, err := BehaviorDigest(analyzerAuthority, analyzerOutput, codec, search)
	if err != nil || !behavior.Valid() || len(behavior.Bytes()) != sha256.Size {
		t.Fatalf("behavior digest: %v", err)
	}

	changedQuery := mustComponent(t, "query-codec", "neutral/query/v1", []byte("changed\n"))
	changedCodec, _ := CodecDigest(source, changedQuery)
	changedBehavior, _ := BehaviorDigest(analyzerAuthority, analyzerOutput, changedCodec, search)
	if codec.Equal(changedCodec) || behavior.Equal(changedBehavior) {
		t.Fatal("semantic mutation did not move identity")
	}
	if _, err := BehaviorDigest(analyzerOutput, analyzerAuthority, codec, search); err == nil {
		t.Fatal("reordered behavior components accepted")
	}
	if _, err := DigestFrame(DomainCodec, []Component{query, source}); err == nil {
		t.Fatal("reordered codec components accepted")
	}
	if _, err := ParseDigestHex(strings.ToUpper(codec.Hex())); err == nil {
		t.Fatal("uppercase digest accepted")
	}
	parsed, err := ParseDigestHex(codec.Hex())
	if err != nil || !parsed.Equal(codec) {
		t.Fatalf("digest parse: %v", err)
	}
}

func TestIdentityArtifactExactCeiling(t *testing.T) {
	exact := bytes.Repeat([]byte{'x'}, MaxIdentityArtifactBytes)
	component, err := NewComponent(ComponentSpec{
		Label: "source-projection", Schema: "neutral/source/v1", Artifact: exact,
	})
	if err != nil || len(component.Artifact()) != MaxIdentityArtifactBytes {
		t.Fatalf("exact artifact ceiling: %v", err)
	}
	over := bytes.Repeat([]byte{'x'}, MaxIdentityArtifactBytes+1)
	if _, err := NewComponent(ComponentSpec{
		Label: "source-projection", Schema: "neutral/source/v1", Artifact: over,
	}); err == nil {
		t.Fatal("one-over artifact accepted")
	}
}

func TestIdentityRefusalAndNoLeak(t *testing.T) {
	secret := "secret-artifact-value"
	invalid := []ComponentSpec{
		{},
		{Label: "Upper", Schema: "neutral/v1", Artifact: []byte(secret)},
		{Label: "label", Schema: "Bad Schema", Artifact: []byte(secret)},
		{Label: "label", Schema: "neutral/v1"},
	}
	for index, spec := range invalid {
		_, err := NewComponent(spec)
		if err == nil || strings.Contains(err.Error(), secret) {
			t.Fatalf("invalid component %d error %v", index, err)
		}
	}
	source := mustComponent(t, "source-projection", "neutral/v1", []byte(secret))
	if _, err := DigestFrame(Domain("unknown"), []Component{source}); err == nil {
		t.Fatal("unknown domain accepted")
	}
	_, err := ParseDigestHex(strings.Repeat("g", 64))
	if err == nil || strings.Contains(err.Error(), strings.Repeat("g", 64)) || err.Error() != "adapter: identity_mismatch" {
		t.Fatalf("unsafe identity error: %v", err)
	}
}

func TestIdentityTokenAndComponentCountEdges(t *testing.T) {
	label := "a" + strings.Repeat("b", MaxIdentityLabelBytes-1)
	schema := "a" + strings.Repeat("b", MaxIdentitySchemaBytes-1)
	component := mustComponent(t, label, schema, []byte("artifact"))
	if _, err := Frame(DomainComponent, []Component{component}); err != nil {
		t.Fatalf("identity token edges: %v", err)
	}
	if _, err := NewComponent(ComponentSpec{
		Label: "a" + strings.Repeat("b", MaxIdentityLabelBytes), Schema: "neutral/v1", Artifact: []byte("x"),
	}); err == nil {
		t.Fatal("one-over identity label accepted")
	}
	if _, err := NewComponent(ComponentSpec{
		Label: "label", Schema: "a" + strings.Repeat("b", MaxIdentitySchemaBytes), Artifact: []byte("x"),
	}); err == nil {
		t.Fatal("one-over identity schema accepted")
	}
	if _, err := Frame(DomainComponent, nil); err == nil {
		t.Fatal("zero identity components accepted")
	}
	if _, err := Frame(DomainComponent, []Component{component, component}); err == nil {
		t.Fatal("component-domain count mismatch accepted")
	}
}

func mustComponent(t *testing.T, label, schema string, artifact []byte) Component {
	t.Helper()
	component, err := NewComponent(ComponentSpec{Label: label, Schema: schema, Artifact: artifact})
	if err != nil {
		t.Fatal(err)
	}
	return component
}

func independentFrame(domain string, components []Component) []byte {
	var out bytes.Buffer
	write := func(value []byte) {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(value)))
		out.Write(length[:])
		out.Write(value)
	}
	write([]byte(domain))
	var count [8]byte
	binary.BigEndian.PutUint64(count[:], uint64(len(components)))
	out.Write(count[:])
	for _, component := range components {
		write([]byte(component.Label()))
		write([]byte(component.Schema()))
		write(component.Artifact())
	}
	return out.Bytes()
}
