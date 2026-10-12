package lifecycle

import (
	"bytes"
	"strings"
	"testing"
)

func TestStrictEnvelopeCodec(t *testing.T) {
	envelope := validFixtureEnvelope(t)
	raw, err := MarshalEnvelope(envelope)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Envelope
	if err := decodeCanonical(raw, MaxControlBytes, &decoded); err != nil || decoded != envelope {
		t.Fatal("canonical envelope did not round-trip")
	}
	mutations := map[string][]byte{
		"trailing":     append(append([]byte(nil), raw...), []byte("{}")...),
		"noncanonical": bytes.Replace(raw, []byte("  \"schema\""), []byte(" \"schema\""), 1),
		"unknown":      bytes.Replace(raw, []byte("{\n"), []byte("{\n  \"unknown\": true,\n"), 1),
		"duplicate":    bytes.Replace(raw, []byte("{\n"), []byte("{\n  \"schema\": \"kurn-search-artifact-envelope/v1\",\n"), 1),
	}
	for name, changed := range mutations {
		t.Run(name, func(t *testing.T) {
			var value Envelope
			if decodeCanonical(changed, MaxControlBytes, &value) == nil {
				t.Fatal("invalid encoding was accepted")
			}
		})
	}
}

func TestPayloadDigestExcludesProofAndBindsReplayRelation(t *testing.T) {
	envelope := validFixtureEnvelope(t)
	first, err := PayloadSHA256(envelope.Payload)
	if err != nil {
		t.Fatal(err)
	}
	envelope.Proof.Hex = strings.Repeat("a", 64)
	second, err := PayloadSHA256(envelope.Payload)
	if err != nil || second != first {
		t.Fatal("proof bytes entered the payload identity")
	}
	changed := envelope.Payload
	changed.ArtifactGeneration = 2
	changed.PredecessorArtifactID = strings.Repeat("a", 64)
	if _, err := PayloadSHA256(changed); err == nil {
		t.Fatal("stale replay nonce was accepted")
	}
	changed.ReplayNonce = ReplayNonce(changed.CandidateSHA256, changed.PredecessorArtifactID, changed.ArtifactGeneration, changed.RouteNamespace)
	third, err := PayloadSHA256(changed)
	if err != nil || third == first {
		t.Fatal("generation and predecessor did not enter the payload identity")
	}
}

func TestFixtureVerifierIsClosedAndNonAuthenticating(t *testing.T) {
	envelope := validFixtureEnvelope(t)
	raw, err := MarshalEnvelope(envelope)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := VerifyFixtureEnvelope(raw, envelope.Payload.VerifierSHA256)
	if err != nil || receipt.Decision != "accepted" || receipt.ProofKind != FixtureProofKind || receipt.ArtifactID == "" {
		t.Fatal("valid fixture envelope refused")
	}
	for name, mutate := range map[string]func(*Envelope){
		"proof": func(value *Envelope) { value.Proof.Hex = strings.Repeat("b", 64) },
		"kind": func(value *Envelope) {
			value.Proof.Kind = "detached-signature/v1"
			value.Payload.ProofKind = "detached-signature/v1"
		},
		"profile":    func(value *Envelope) { value.Payload.VerifierProfileID = "production-profile/v1" },
		"trust-root": func(value *Envelope) { value.Payload.TrustRootID = "production-root/v1" },
		"publisher":  func(value *Envelope) { value.Payload.PublisherID = "production-publisher/v1" },
		"namespace":  func(value *Envelope) { value.Payload.RouteNamespace = "production-route/v1" },
		"executable": func(value *Envelope) { value.Payload.VerifierSHA256 = strings.Repeat("b", 64) },
	} {
		t.Run(name, func(t *testing.T) {
			changed := envelope
			mutate(&changed)
			changedRaw, marshalErr := MarshalEnvelope(changed)
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			if _, verifyErr := VerifyFixtureEnvelope(changedRaw, envelope.Payload.VerifierSHA256); verifyErr == nil {
				t.Fatal("identity substitution was accepted")
			}
		})
	}
}

func TestBoundedVerifierOutput(t *testing.T) {
	buffer := &boundedBuffer{limit: 4}
	if n, err := buffer.Write([]byte("1234")); err != nil || n != 4 {
		t.Fatal("bounded output refused exact limit")
	}
	if _, err := buffer.Write([]byte("5")); err == nil || buffer.String() != "1234" {
		t.Fatal("bounded output accepted overflow")
	}
}

func TestRegisterAndRouteCodecsRejectMissingFields(t *testing.T) {
	for name, raw := range map[string][]byte{
		"register": []byte("{\n  \"schema\": \"kurn-search-lifecycle-register/v1\"\n}\n"),
		"route":    []byte("{\n  \"schema\": \"kurn-search-route-intent/v1\"\n}\n"),
	} {
		t.Run(name, func(t *testing.T) {
			var destination any
			if name == "register" {
				destination = &RegisterSpec{}
			} else {
				destination = &RouteIntent{}
			}
			if decodeCanonical(raw, MaxControlBytes, destination) == nil {
				t.Fatal("missing required fields were accepted")
			}
		})
	}
}

func validFixtureEnvelope(t *testing.T) Envelope {
	t.Helper()
	hash := func(character string) string { return strings.Repeat(character, 64) }
	payload := EnvelopePayload{
		ProtocolVersion: VerifierProtocol, CandidateSHA256: hash("1"), KurnManifestSHA256: hash("2"),
		ConfigSHA256: hash("3"), BaseSHA256: hash("4"), IndexSHA256: hash("5"), KurnSHA256: hash("6"),
		ArtifactGeneration: 1, AdapterBehaviorSHA256: hash("7"), OperationalProfileSHA256: hash("8"),
		PublisherID: FixturePublisherID, VerifierProfileID: FixtureProfileID, TrustRootID: FixtureTrustRootID,
		VerifierSHA256: hash("9"), ProofKind: FixtureProofKind, RouteNamespace: FixtureRouteScope,
	}
	payload.ReplayNonce = ReplayNonce(payload.CandidateSHA256, payload.PredecessorArtifactID, payload.ArtifactGeneration, payload.RouteNamespace)
	payloadSHA, err := PayloadSHA256(payload)
	if err != nil {
		t.Fatal(err)
	}
	return Envelope{Schema: EnvelopeSchema, Payload: payload, Proof: EnvelopeProof{Kind: FixtureProofKind, Hex: FixtureProof(payloadSHA)}}
}
