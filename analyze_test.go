package netsec

import (
	"testing"
)

// reportFor finds the report for the requested term.
func reportFor(t *testing.T, reps []LeakReport, want Term) LeakReport {
	t.Helper()
	for _, r := range reps {
		if r.Secret.ID() == want.ID() {
			return r
		}
	}
	t.Fatalf("no report for %s", want)
	return LeakReport{}
}

// checkProof walks the proof tree and verifies that every node's time is the
// max of its premises and that leaves are grounded in timeline events.
func checkProof(t *testing.T, p Proof) {
	t.Helper()
	if len(p.SubProofs) == 0 {
		if p.Event == nil {
			t.Fatalf("leaf proof %s has no grounding event", p.Term)
		}
		if p.At != p.Event.Time {
			t.Fatalf("leaf %s at %d, event at %d", p.Term, p.At, p.Event.Time)
		}
		return
	}
	want := p.SubProofs[0].At
	for _, sub := range p.SubProofs {
		checkProof(t, sub)
		if sub.At > want {
			want = sub.At
		}
	}
	if p.At != want {
		t.Fatalf("proof node %s at %d but premises finish at %d", p.Term, p.At, want)
	}
	switch p.Rule {
	case RulePair, RulePairBuild, RuleDecrypt, RuleEncrypt, RuleDerive:
	default:
		t.Fatalf("internal node uses leaf rule %q", p.Rule)
	}
}

// TestLongTermKeyCompromise is the baseline lesson: traffic sent at t=1 stays
// protected until the long-term key leaks at t=5, which retroactively exposes
// every past ciphertext under that key.
func TestLongTermKeyCompromise(t *testing.T) {
	m := NewModel()
	ltk := m.Name("ltk")
	payload := m.Name("payload")
	msg := m.Enc(ltk, payload)

	send := m.Send(msg, 1)
	m.Compromise(ltk, 5)

	reps := m.Analyze(payload, ltk, msg)

	r := reportFor(t, reps, payload)
	if !r.Leaked || r.Earliest != 5 {
		t.Fatalf("payload leak = %+v, want leaked at t=5", r)
	}
	if r.Proof.Rule != RuleDecrypt {
		t.Fatalf("payload proof rule = %q, want decrypt", r.Proof.Rule)
	}
	checkProof(t, *r.Proof)

	k := reportFor(t, reps, ltk)
	if !k.Leaked || k.Earliest != 5 || k.Proof.Event.Index != send.Index+1 {
		t.Fatalf("ltk report = %+v", k)
	}

	c := reportFor(t, reps, msg)
	if !c.Leaked || c.Earliest != 1 {
		t.Fatalf("ciphertext was on the wire at t=1, got %+v", c)
	}
}

// TestForwardSecrecy shows the contrast the package exists to teach: a session
// key destroyed at t=3 cannot be compromised at t=5, so the past session
// traffic remains secret even though later keys leak.
func TestForwardSecrecy(t *testing.T) {
	m := NewModel()
	sessKey := m.Name("session_key")
	ltk := m.Name("ltk")
	secret1 := m.Name("session_secret")
	secret2 := m.Name("later_secret")

	m.Send(m.Enc(sessKey, secret1), 1)
	m.Destroy(sessKey, 3)
	m.Send(m.Enc(ltk, secret2), 4)
	m.Compromise(sessKey, 5) // too late: the key was destroyed at t=3
	m.Compromise(ltk, 6)

	reps := m.Analyze(secret1, secret2, sessKey)

	s1 := reportFor(t, reps, secret1)
	if s1.Leaked {
		t.Fatalf("past session secret must stay secret, got %+v", s1)
	}
	if s1.Proof != nil {
		t.Fatalf("unleaked secret must carry no proof, got %v", s1.Proof)
	}

	s2 := reportFor(t, reps, secret2)
	if !s2.Leaked || s2.Earliest != 6 {
		t.Fatalf("later secret should fall at t=6 with ltk, got %+v", s2)
	}
	checkProof(t, *s2.Proof)

	k := reportFor(t, reps, sessKey)
	if k.Leaked {
		t.Fatalf("destroyed session key must not be learnable, got %+v", k)
	}
}

// TestCompromiseBeforeDestroy covers the other ordering: knowledge gained
// before destruction survives the destruction, and earlier traffic under the
// same key is already exposed at the earlier compromise time.
func TestCompromiseBeforeDestroy(t *testing.T) {
	m := NewModel()
	sessKey := m.Name("session_key")
	secret := m.Name("session_secret")

	m.Send(m.Enc(sessKey, secret), 1)
	m.Compromise(sessKey, 2)
	m.Destroy(sessKey, 3)

	reps := m.Analyze(secret, sessKey)

	k := reportFor(t, reps, sessKey)
	if !k.Leaked || k.Earliest != 2 {
		t.Fatalf("compromise at t=2 must stick, got %+v", k)
	}
	s := reportFor(t, reps, secret)
	if !s.Leaked || s.Earliest != 2 {
		t.Fatalf("secret exposed at t=2 and stays known, got %+v", s)
	}
	checkProof(t, *s.Proof)
}

// TestSameTimeCompromiseAndDestroy pins the boundary rule: a compromise at the
// destruction time is too late, because destruction already covers that time.
func TestSameTimeCompromiseAndDestroy(t *testing.T) {
	m := NewModel()
	k := m.Name("k")
	secret := m.Name("secret")
	m.Send(m.Enc(k, secret), 1)
	m.Destroy(k, 4)
	m.Compromise(k, 4)

	reps := m.Analyze(secret)
	if r := reportFor(t, reps, secret); r.Leaked {
		t.Fatalf("same-time compromise must be rejected, got %+v", r)
	}
}

// TestEphemeralKeyScope shows that a session key only opens its own session:
// leaking it does not touch another session's traffic or long-term material.
func TestEphemeralKeyScope(t *testing.T) {
	m := NewModel()
	k1 := m.Name("session_k1")
	k2 := m.Name("session_k2")
	s1 := m.Name("secret_1")
	s2 := m.Name("secret_2")

	m.Send(m.Enc(k1, s1), 1)
	m.Send(m.Enc(k2, s2), 2)
	m.Compromise(k1, 3)

	reps := m.Analyze(s1, s2, k2)
	if r := reportFor(t, reps, s1); !r.Leaked || r.Earliest != 3 {
		t.Fatalf("session 1 secret leaks at t=3, got %+v", r)
	}
	if r := reportFor(t, reps, s2); r.Leaked {
		t.Fatalf("session 2 secret must be untouched, got %+v", r)
	}
	if r := reportFor(t, reps, k2); r.Leaked {
		t.Fatalf("uncompromised key must stay secret, got %+v", r)
	}
}

// TestDerivationOneWay encodes the irreversibility rule: the derivation is
// computable only after every input is known, and observing its output never
// feeds the inputs back to the attacker.
func TestDerivationOneWay(t *testing.T) {
	m := NewModel()
	a := m.Name("a")
	b := m.Name("b")
	secret := m.Name("secret")
	derived := m.Derive(a, b)

	// Only the derived value is ever sent; neither input is on the wire.
	m.Send(derived, 1)

	first := m.Analyze(a, b, derived)
	if r := reportFor(t, first, derived); !r.Leaked || r.Earliest != 1 {
		t.Fatalf("derived term itself is visible, got %+v", r)
	}
	if r := reportFor(t, first, a); r.Leaked {
		t.Fatalf("derivation must be one-way, input a leaked: %+v", r)
	}
	if r := reportFor(t, first, b); r.Leaked {
		t.Fatalf("derivation must be one-way, input b leaked: %+v", r)
	}

	// One input is not enough.
	m.Compromise(a, 2)
	half := m.Analyze(secret, derived, b)
	if r := reportFor(t, half, b); r.Leaked {
		t.Fatalf("one input must not complete the derivation, got %+v", r)
	}

	// Both inputs known: the attacker can recompute, but still cannot invert.
	m.Compromise(b, 4)
	full := m.Analyze(derived, secret)
	r := reportFor(t, full, derived)
	if !r.Leaked || r.Earliest != 1 {
		t.Fatalf("wire observation at t=1 is still the earliest, got %+v", r)
	}
	if r.Proof.Rule != RuleSend {
		t.Fatalf("earliest proof should be the wire send, got %q", r.Proof.Rule)
	}
	if r := reportFor(t, full, secret); r.Leaked {
		t.Fatalf("unrelated secret must stay unknown, got %+v", r)
	}
}

// TestPairAndNestedEncryption exercises the transparent/opaque boundary:
// pair components strip for free, while the ciphertext inside a pair keeps
// its plaintext until the key arrives.
func TestPairAndNestedEncryption(t *testing.T) {
	m := NewModel()
	k := m.Name("k")
	inner := m.Name("inner_secret")
	outer := m.Name("outer_field")
	msg := m.Pair(outer, m.Enc(k, inner))

	m.Send(msg, 1)

	reps := m.Analyze(outer, inner)
	if r := reportFor(t, reps, outer); !r.Leaked || r.Earliest != 1 {
		t.Fatalf("pair field should be free at t=1, got %+v", r)
	}
	if r := reportFor(t, reps, inner); r.Leaked {
		t.Fatalf("encrypted half must stay protected, got %+v", r)
	}

	m.Compromise(k, 3)
	reps = m.Analyze(outer, inner)
	r := reportFor(t, reps, inner)
	if !r.Leaked || r.Earliest != 3 || r.Proof.Rule != RuleDecrypt {
		t.Fatalf("inner secret decrypts at t=3, got %+v", r)
	}
	checkProof(t, *r.Proof)
}

// TestEarliestProofPicksMinimum builds a secret with two independent exposure
// paths and checks that the report keeps the earlier one.
func TestEarliestProofPicksMinimum(t *testing.T) {
	m := NewModel()
	k := m.Name("k")
	secret := m.Name("secret")
	m.Send(m.Enc(k, secret), 1)
	m.Compromise(secret, 2) // direct leak, earlier than decryption path
	m.Compromise(k, 5)

	reps := m.Analyze(secret)
	r := reportFor(t, reps, secret)
	if !r.Leaked || r.Earliest != 2 {
		t.Fatalf("direct compromise at t=2 must win, got %+v", r)
	}
	if r.Proof.Rule != RuleCompromise {
		t.Fatalf("want compromise proof, got %q", r.Proof.Rule)
	}
}

// TestProofStringRendersTree is a smoke test for the teaching output format.
func TestProofStringRendersTree(t *testing.T) {
	m := NewModel()
	k := m.Name("k")
	secret := m.Name("secret")
	m.Send(m.Enc(k, secret), 1)
	m.Compromise(k, 4)

	reps := m.Analyze(secret)
	rendered := reps[0].Proof.String()
	wantSubstrings := []string{"t=4 decrypt: secret", "t=1 send: enc(k, secret)", "t=4 compromise: k"}
	for _, w := range wantSubstrings {
		if !contains(rendered, w) {
			t.Fatalf("proof render %q missing %q", rendered, w)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
