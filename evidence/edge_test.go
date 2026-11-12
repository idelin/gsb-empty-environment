package evidence_test

import (
	"crypto/ed25519"
	"strings"
	"testing"

	"example.org/domainreissue/q012/evidence"
)

func TestValidationAndEngineErrors(t *testing.T) {
	message := []byte("edge case message")
	relay := newTestNode(t, "relay-a", true)
	engine := engineFor(relay)
	valid := mustSign(t, relay, "a", "method", message, true)

	t.Run("nil rule and empty message", func(t *testing.T) {
		if _, err := engine.Evaluate(nil, message, nil); err == nil {
			t.Fatal("nil rule must error")
		}
		rule := parseRule(t, "a")
		if _, err := engine.Evaluate(rule, nil, nil); err == nil {
			t.Fatal("empty message must error")
		}
	})

	t.Run("malformed facts evaluate unknown", func(t *testing.T) {
		rule := parseRule(t, "a")
		malformed := []*evidence.Fact{
			nil,
			{Name: "a", NodeID: relay.id, Method: "m", Message: message},
			{Name: "", NodeID: relay.id, Method: "m", Message: message, Signature: make([]byte, 64)},
			{Name: "a", NodeID: "", Method: "m", Message: message, Signature: make([]byte, 64)},
			{Name: "a", NodeID: relay.id, Method: "", Message: message, Signature: make([]byte, 64)},
			{Name: "a", NodeID: relay.id, Method: "m", Message: nil, Signature: make([]byte, 64)},
			{Name: "a", NodeID: relay.id, Method: "m", Message: message, Signature: make([]byte, 63)},
		}
		for i, fact := range malformed {
			decision, err := engine.Evaluate(rule, message, []*evidence.Fact{fact})
			if err != nil {
				t.Fatalf("case %d: malformed fact must be unknown, not an error: %v", i, err)
			}
			if decision.Release || decision.Values["a"] != evidence.Unknown {
				t.Fatalf("case %d: malformed fact must be unknown", i)
			}
		}
	})

	t.Run("duplicate authenticated names rejected", func(t *testing.T) {
		second := mustSign(t, relay, "a", "method", message, true)
		rule := parseRule(t, "a")
		_, err := engine.Evaluate(rule, message, []*evidence.Fact{valid, second})
		if err == nil || !strings.Contains(err.Error(), "duplicate authenticated fact") {
			t.Fatalf("expected duplicate error, got %v", err)
		}
	})

	t.Run("NewFact rejects bad key and empty fields", func(t *testing.T) {
		if _, err := evidence.NewFact("a", relay.id, "m", message, true, ed25519.PrivateKey{}); err == nil {
			t.Fatal("short private key must error")
		}
		if _, err := evidence.NewFact("", relay.id, "m", message, true, relay.private); err == nil {
			t.Fatal("empty name must error")
		}
	})

	t.Run("custom MaxFacts", func(t *testing.T) {
		small := &evidence.Engine{Trusted: engine.Trusted, MaxFacts: 1}
		second := mustSign(t, relay, "b", "method", message, true)
		rule := parseRule(t, "a or b")
		if _, err := small.Evaluate(rule, message, []*evidence.Fact{valid, second}); err == nil {
			t.Fatal("custom MaxFacts must be enforced")
		}

		decision, err := small.Evaluate(rule, message, []*evidence.Fact{valid})
		if err != nil {
			t.Fatalf("within limit: %v", err)
		}
		if !decision.Release || len(decision.MinimalSets) != 1 {
			t.Fatalf("expected release with one minimal set, got %+v", decision)
		}
	})

	t.Run("unconfigured public key is unknown", func(t *testing.T) {
		other := newTestNode(t, "relay-a", false)
		other.id = relay.id
		badEngine := &evidence.Engine{Trusted: evidence.TrustedNodes{relay.id: other.public[:10]}}
		rule := parseRule(t, "a")
		decision, err := badEngine.Evaluate(rule, message, []*evidence.Fact{valid})
		if err != nil {
			t.Fatalf("Evaluate: %v", err)
		}
		if decision.Release || decision.Values["a"] != evidence.Unknown {
			t.Fatal("wrong-length key must leave the fact unknown")
		}
	})
}
