package protocol

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
)

func TestSignedMessageBindsRouteContextAndPayload(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	original := map[string]any{"protocolVersion": "v2", "agentId": "analyst", "sessionKey": "session-a", "expiresAt": 123456, "metadata": map[string]any{"tenant": "a"}, "payload": map[string]any{"text": "hello"}}
	input, err := SignedBytes("bot.a.inbox", original)
	if err != nil {
		t.Fatal(err)
	}
	sig := ed25519.Sign(priv, input)
	for _, field := range []string{"protocolVersion", "agentId", "sessionKey", "expiresAt", "metadata", "payload"} {
		t.Run(field, func(t *testing.T) {
			changed := make(map[string]any, len(original))
			for key, value := range original {
				changed[key] = value
			}
			changed[field] = "tampered"
			data, err := SignedBytes("bot.a.inbox", changed)
			if err != nil {
				t.Fatal(err)
			}
			if ed25519.Verify(pub, data, sig) {
				t.Fatalf("tampered %s verified", field)
			}
		})
	}
	otherSubject, err := SignedBytes("bot.b.inbox", original)
	if err != nil {
		t.Fatal(err)
	}
	if ed25519.Verify(pub, otherSubject, sig) {
		t.Fatal("signature valid for another subject")
	}
}

func TestStrictMessageRejectsAmbiguousJSON(t *testing.T) {
	for _, data := range []string{
		`{"to":"a","to":"b"}`,
		`{"payload":{"text":"a","text":"b"}}`,
		`{"metadata":{"nested":[{"x":1,"x":2}]}}`,
		`{"to":"a"} {"to":"b"}`,
		`{"unknown":"value"}`,
	} {
		var env MessageEnvelope
		if err := DecodeStrict([]byte(data), &env); err == nil {
			t.Fatalf("accepted ambiguous message %s", data)
		}
	}
}

func TestSignatureSurvivesStructuredInputRoundTripWithoutNumberLoss(t *testing.T) {
	data := []byte(`{"protocolVersion":"v2","id":"id","type":"task.request","from":"a","to":"b","payload":{"input":{"large":9007199254740993,"tiny":1e-20}},"metadata":{"large":9007199254740993},"ts":1,"expiresAt":2}`)
	var first MessageEnvelope
	if err := DecodeStrict(data, &first); err != nil {
		t.Fatal(err)
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	input, err := SignedBytes("subject", first)
	if err != nil {
		t.Fatal(err)
	}
	sig := ed25519.Sign(priv, input)
	var second MessageEnvelope
	if err := DecodeStrict(data, &second); err != nil {
		t.Fatal(err)
	}
	verified, err := SignedBytes("subject", second)
	if err != nil {
		t.Fatal(err)
	}
	if !ed25519.Verify(pub, verified, sig) {
		t.Fatal("signed input lost numeric precision")
	}
	second.Metadata["large"] = 9007199254740992.0
	changed, err := SignedBytes("subject", second)
	if err != nil {
		t.Fatal(err)
	}
	if ed25519.Verify(pub, changed, sig) {
		t.Fatal("rounded numeric value verified")
	}
}
