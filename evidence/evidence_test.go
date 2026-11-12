package evidence_test

import (
	"bytes"
	"crypto/ed25519"
	"testing"

	"example.org/domainreissue/q012/evidence"
)

type testNode struct {
	id      string
	public  ed25519.PublicKey
	private ed25519.PrivateKey
	trusted bool
}

func newTestNode(t testing.TB, id string, trusted bool) testNode {
	t.Helper()
	public, private, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key for %s: %v", id, err)
	}
	return testNode{id: id, public: public, private: private, trusted: trusted}
}

func mustSign(t testing.TB, node testNode, name, method string, message []byte, claim bool) *evidence.Fact {
	t.Helper()
	fact, err := evidence.NewFact(name, node.id, method, message, claim, node.private)
	if err != nil {
		t.Fatalf("NewFact(%s): %v", name, err)
	}
	return fact
}

func engineFor(nodes ...testNode) *evidence.Engine {
	trusted := evidence.TrustedNodes{}
	for _, node := range nodes {
		if node.trusted {
			trusted[node.id] = node.public
		}
	}
	return &evidence.Engine{Trusted: trusted}
}

func parseRule(t testing.TB, text string) evidence.Expr {
	t.Helper()
	rule, err := evidence.ParseRule(text)
	if err != nil {
		t.Fatalf("ParseRule(%q): %v", text, err)
	}
	return rule
}

func TestSignedFactsAndExternalSameName(t *testing.T) {
	message := []byte("From: alice@example.com\r\n\r\nhello")
	relay := newTestNode(t, "relay-a", true)
	rogue := newTestNode(t, "rogue", false)
	engine := engineFor(relay, rogue)

	trustedPass := mustSign(t, relay, "spf.pass", "spf", message, true)
	trustedDkim := mustSign(t, relay, "dkim.pass", "dkim", message, true)

	t.Run("trusted true facts release conjunction", func(t *testing.T) {
		rule := parseRule(t, "spf.pass and dkim.pass")
		decision, err := engine.Evaluate(rule, message, []*evidence.Fact{trustedPass, trustedDkim})
		if err != nil {
			t.Fatalf("Evaluate: %v", err)
		}
		if !decision.Release || decision.Value != evidence.True {
			t.Fatalf("expected release, got value=%s", decision.Value)
		}
		if len(decision.Authenticated) != 2 {
			t.Fatalf("expected 2 authenticated facts, got %d", len(decision.Authenticated))
		}
		if len(decision.MinimalSets) != 1 || len(decision.MinimalSets[0]) != 2 {
			t.Fatalf("expected one 2-fact minimal set, got %+v", decision.MinimalSets)
		}
	})

	t.Run("untrusted same-name declaration stays unknown", func(t *testing.T) {
		forged := mustSign(t, rogue, "spf.pass", "spf", message, true)
		rule := parseRule(t, "spf.pass")

		externalOnly, err := engine.Evaluate(rule, message, []*evidence.Fact{forged})
		if err != nil {
			t.Fatalf("Evaluate external only: %v", err)
		}
		if externalOnly.Release {
			t.Fatal("external declaration must not release")
		}
		if externalOnly.Values["spf.pass"] != evidence.Unknown {
			t.Fatalf("external fact must be unknown, got %s", externalOnly.Values["spf.pass"])
		}
		if len(externalOnly.Authenticated) != 0 {
			t.Fatalf("external fact must not authenticate, got %d", len(externalOnly.Authenticated))
		}

		// Same name as a trusted fact: the external declaration must never
		// override or contaminate the trusted binding.
		mixed, err := engine.Evaluate(rule, message, []*evidence.Fact{forged, trustedPass})
		if err != nil {
			t.Fatalf("Evaluate mixed: %v", err)
		}
		if !mixed.Release || mixed.Values["spf.pass"] != evidence.True {
			t.Fatalf("trusted binding must dominate external declaration, got %s", mixed.Values["spf.pass"])
		}
		if len(mixed.Authenticated) != 1 || mixed.Authenticated[0].NodeID != relay.id {
			t.Fatalf("only the trusted fact may authenticate")
		}
	})

	t.Run("trusted false claim binds false", func(t *testing.T) {
		failed := mustSign(t, relay, "spf.pass", "spf", message, false)
		rule := parseRule(t, "spf.pass")
		decision, err := engine.Evaluate(rule, message, []*evidence.Fact{failed})
		if err != nil {
			t.Fatalf("Evaluate: %v", err)
		}
		if decision.Release || decision.Value != evidence.False {
			t.Fatalf("expected false without release, got %s", decision.Value)
		}
		if decision.Values["spf.pass"] != evidence.False {
			t.Fatalf("expected false binding, got %s", decision.Values["spf.pass"])
		}
	})

	t.Run("unsigned and malformed evidence is unknown", func(t *testing.T) {
		unsigned := &evidence.Fact{
			Name: "spf.pass", NodeID: relay.id, Method: "spf",
			Message: append([]byte(nil), message...), Claim: true,
		}
		rule := parseRule(t, "spf.pass")
		decision, err := engine.Evaluate(rule, message, []*evidence.Fact{unsigned})
		if err != nil {
			t.Fatalf("Evaluate: %v", err)
		}
		if decision.Release || decision.Values["spf.pass"] != evidence.Unknown {
			t.Fatalf("unsigned fact must be unknown, got %+v", decision.Values)
		}
	})
}

func TestTamperingInvalidatesSignature(t *testing.T) {
	message := []byte("message-1")
	relay := newTestNode(t, "relay-a", true)
	engine := engineFor(relay)
	rule := parseRule(t, "dkim.pass")

	original := mustSign(t, relay, "dkim.pass", "dkim", message, true)

	cases := map[string]func(*evidence.Fact){
		"message": func(f *evidence.Fact) { f.Message = append(f.Message, '!') },
		"claim":   func(f *evidence.Fact) { f.Claim = false },
		"method":  func(f *evidence.Fact) { f.Method = "dmarc" },
		"name":    func(f *evidence.Fact) { f.Name = "dmarc.pass" },
		"node id": func(f *evidence.Fact) { f.NodeID = "relay-b" },
		"signature": func(f *evidence.Fact) {
			f.Signature[0] ^= 0xFF
		},
	}
	for label, mutate := range cases {
		t.Run(label, func(t *testing.T) {
			fact := cloneFact(original)
			mutate(fact)
			decision, err := engine.Evaluate(rule, message, []*evidence.Fact{fact})
			if err != nil {
				t.Fatalf("Evaluate: %v", err)
			}
			if decision.Release {
				t.Fatalf("tampered %s must not release", label)
			}
			if got := decision.Values[ruleName(rule)]; got != evidence.Unknown {
				t.Fatalf("tampered %s must be unknown, got %s", label, got)
			}
		})
	}

	t.Run("wrong verification key", func(t *testing.T) {
		other := newTestNode(t, "relay-a", true)
		other.id = relay.id
		wrongEngine := engineFor(other)
		decision, err := wrongEngine.Evaluate(rule, message, []*evidence.Fact{cloneFact(original)})
		if err != nil {
			t.Fatalf("Evaluate: %v", err)
		}
		if decision.Release || decision.Values["dkim.pass"] != evidence.Unknown {
			t.Fatalf("foreign key must make fact unknown, got %s", decision.Values["dkim.pass"])
		}
	})
}

func TestCrossMailSplicing(t *testing.T) {
	current := []byte("RFC5322 current message body")
	previous := []byte("RFC5322 previous message body")
	relay := newTestNode(t, "relay-a", true)
	engine := engineFor(relay)

	currentFact := mustSign(t, relay, "dkim.pass", "dkim", current, true)
	spliced := mustSign(t, relay, "dkim.pass", "dkim", previous, true)
	rule := parseRule(t, "dkim.pass")

	decision, err := engine.Evaluate(rule, current, []*evidence.Fact{spliced, currentFact})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !decision.Release {
		t.Fatal("current-message fact should release")
	}
	if len(decision.Authenticated) != 1 || !bytes.Equal(decision.Authenticated[0].Message, current) {
		t.Fatal("only the fact bound to the current message may authenticate")
	}

	splicedOnly, err := engine.Evaluate(rule, current, []*evidence.Fact{cloneFact(spliced)})
	if err != nil {
		t.Fatalf("Evaluate spliced only: %v", err)
	}
	if splicedOnly.Release || splicedOnly.Values["dkim.pass"] != evidence.Unknown {
		t.Fatalf("spliced fact from another message must be unknown, got %s",
			splicedOnly.Values["dkim.pass"])
	}
	if len(splicedOnly.Authenticated) != 0 {
		t.Fatal("spliced fact must not authenticate against the current message")
	}
}

func TestDigestBinding(t *testing.T) {
	message := []byte("abc")
	if !bytes.Equal(evidence.MessageDigest(message), evidence.MessageDigest(append([]byte(nil), message...))) {
		t.Fatal("digest must be stable for identical messages")
	}
	if bytes.Equal(evidence.MessageDigest(message), evidence.MessageDigest([]byte("abd"))) {
		t.Fatal("distinct messages must have distinct digests")
	}
}

func cloneFact(f *evidence.Fact) *evidence.Fact {
	copyOf := *f
	copyOf.Message = append([]byte(nil), f.Message...)
	copyOf.Signature = append([]byte(nil), f.Signature...)
	return &copyOf
}

func ruleName(rule evidence.Expr) string {
	names := evidence.Names(rule)
	if len(names) != 1 {
		return names[0]
	}
	return names[0]
}
