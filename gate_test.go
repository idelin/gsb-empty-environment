package netsec

import (
	"bytes"
	"errors"
	"testing"
)

const pub = "publisher-1"

func prefix(b byte) [4]byte { return [4]byte{b, b, b, b} }

func TestParseEncodeRoundTrip(t *testing.T) {
	h := NewSecurityHeader(0xAABBCCDD, prefix(0x11), 0x01020304)
	wire := h.AppendEncode(nil)
	if len(wire) != SecurityHeaderLen {
		t.Fatalf("encoded length = %d, want %d", len(wire), SecurityHeaderLen)
	}
	got, rest, err := ParseSecurityHeader(append(wire, 0xDE, 0xAD))
	if err != nil {
		t.Fatalf("ParseSecurityHeader: %v", err)
	}
	if got != h {
		t.Fatalf("round trip mismatch: got %+v want %+v", got, h)
	}
	if !bytes.Equal(rest, []byte{0xDE, 0xAD}) {
		t.Fatalf("rest = %x, want dead", rest)
	}
	if got.SecurityTokenId != 0xAABBCCDD || got.NonceCounter() != 0x01020304 {
		t.Fatalf("decoded fields wrong: %+v", got)
	}
	if _, _, err := ParseSecurityHeader(wire[:SecurityHeaderLen-1]); !errors.Is(err, ErrSecurityHeaderShort) {
		t.Fatalf("short buffer err = %v, want ErrSecurityHeaderShort", err)
	}
}

func TestReplayRejected(t *testing.T) {
	g := NewGate(100, 99)
	if err := g.Admit(pub, NewSecurityHeader(100, prefix(1), 7), true); err != nil {
		t.Fatalf("first admit: %v", err)
	}
	// Exact replay of the last accepted message.
	if err := g.Admit(pub, NewSecurityHeader(100, prefix(1), 7), true); !errors.Is(err, ErrNonceReplay) {
		t.Fatalf("exact replay err = %v, want ErrNonceReplay", err)
	}
	// Rollback to an older counter.
	if err := g.Admit(pub, NewSecurityHeader(100, prefix(1), 3), true); !errors.Is(err, ErrNonceReplay) {
		t.Fatalf("rollback err = %v, want ErrNonceReplay", err)
	}
	// Strictly greater counter is accepted.
	if err := g.Admit(pub, NewSecurityHeader(100, prefix(1), 8), true); err != nil {
		t.Fatalf("next counter: %v", err)
	}
}

func TestCrossTokenNonceRollback(t *testing.T) {
	g := NewGate(100, 99)
	// Build up state on token 100.
	for c := uint32(1); c <= 10; c++ {
		if err := g.Admit(pub, NewSecurityHeader(100, prefix(1), c), true); err != nil {
			t.Fatalf("admit token 100 counter %d: %v", c, err)
		}
	}
	// Key rotation: 101 becomes current, 100 is retained as previous.
	g.Rotate(101)
	// The new token starts a fresh nonce lifecycle: a low counter is fine.
	if err := g.Admit(pub, NewSecurityHeader(101, prefix(9), 1), true); err != nil {
		t.Fatalf("new token fresh counter: %v", err)
	}
	// Replay under the retired (previous) token with a counter at or
	// below its recorded maximum must still be rejected.
	if err := g.Admit(pub, NewSecurityHeader(100, prefix(1), 10), true); !errors.Is(err, ErrNonceReplay) {
		t.Fatalf("previous-token replay err = %v, want ErrNonceReplay", err)
	}
	if err := g.Admit(pub, NewSecurityHeader(100, prefix(1), 4), true); !errors.Is(err, ErrNonceReplay) {
		t.Fatalf("previous-token rollback err = %v, want ErrNonceReplay", err)
	}
	// A genuinely newer counter on the previous token is still admitted
	// during the one-generation grace window.
	if err := g.Admit(pub, NewSecurityHeader(100, prefix(1), 11), true); err != nil {
		t.Fatalf("previous-token newer counter: %v", err)
	}
}

func TestNoncePrefixChangeRejected(t *testing.T) {
	g := NewGate(100, 99)
	if err := g.Admit(pub, NewSecurityHeader(100, prefix(1), 1), true); err != nil {
		t.Fatalf("first admit: %v", err)
	}
	// Same token, higher counter, but a different random prefix: forged.
	if err := g.Admit(pub, NewSecurityHeader(100, prefix(2), 2), true); !errors.Is(err, ErrNoncePrefixChanged) {
		t.Fatalf("prefix change err = %v, want ErrNoncePrefixChanged", err)
	}
	// The forged message must not have poisoned state: the legitimate
	// next counter is still accepted.
	if err := g.Admit(pub, NewSecurityHeader(100, prefix(1), 2), true); err != nil {
		t.Fatalf("legit message after forgery attempt: %v", err)
	}
	// A different publisher has an independent nonce stream/prefix.
	if err := g.Admit("publisher-2", NewSecurityHeader(100, prefix(2), 1), true); err != nil {
		t.Fatalf("other publisher prefix: %v", err)
	}
}

func TestGrandparentTokenRejected(t *testing.T) {
	g := NewGate(100, 99)
	// Token 98 predates the previous token and is not current.
	if err := g.Admit(pub, NewSecurityHeader(98, prefix(1), 1), true); !errors.Is(err, ErrTokenTooOld) {
		t.Fatalf("grandparent token err = %v, want ErrTokenTooOld", err)
	}
	// Rotate twice so token 99 falls out of the window entirely.
	g.Rotate(101)
	g.Rotate(102)
	if err := g.Admit(pub, NewSecurityHeader(99, prefix(1), 1), true); !errors.Is(err, ErrTokenTooOld) {
		t.Fatalf("expired previous token err = %v, want ErrTokenTooOld", err)
	}
	// A token id that is neither current nor previous (but newer) is
	// rejected as well.
	if err := g.Admit(pub, NewSecurityHeader(500, prefix(1), 1), true); !errors.Is(err, ErrTokenRejected) {
		t.Fatalf("unknown token err = %v, want ErrTokenRejected", err)
	}
	// Current and previous are accepted.
	if err := g.Admit(pub, NewSecurityHeader(102, prefix(1), 1), true); err != nil {
		t.Fatalf("current token: %v", err)
	}
	if err := g.Admit(pub, NewSecurityHeader(101, prefix(1), 1), true); err != nil {
		t.Fatalf("previous token: %v", err)
	}
}

func TestVerificationFailureDoesNotAdvanceState(t *testing.T) {
	g := NewGate(100, 99)
	if err := g.Admit(pub, NewSecurityHeader(100, prefix(1), 5), true); err != nil {
		t.Fatalf("first admit: %v", err)
	}
	// A message with a bad signature must be rejected and must not
	// advance the recorded maximum counter.
	bad := NewSecurityHeader(100, prefix(1), 9)
	if err := g.Admit(pub, bad, false); !errors.Is(err, ErrVerificationFailed) {
		t.Fatalf("unverified message err = %v, want ErrVerificationFailed", err)
	}
	// Counter 9 was never committed: a properly verified message with
	// that same counter must still be admitted.
	if err := g.Admit(pub, NewSecurityHeader(100, prefix(1), 9), true); err != nil {
		t.Fatalf("counter 9 after failed verification: %v", err)
	}
	// Check/Commit split: Check alone must not advance state either.
	if err := g.Check(pub, NewSecurityHeader(100, prefix(1), 12)); err != nil {
		t.Fatalf("check: %v", err)
	}
	if err := g.Admit(pub, NewSecurityHeader(100, prefix(1), 12), true); err != nil {
		t.Fatalf("counter 12 after bare check: %v", err)
	}
	// And a failed verification must not clobber the recorded prefix.
	forged := NewSecurityHeader(100, prefix(7), 20)
	if err := g.Admit(pub, forged, false); !errors.Is(err, ErrVerificationFailed) {
		t.Fatalf("unverified forged prefix err = %v, want ErrVerificationFailed", err)
	}
	if err := g.Admit(pub, NewSecurityHeader(100, prefix(1), 13), true); err != nil {
		t.Fatalf("legit prefix after failed forgery: %v", err)
	}
}
