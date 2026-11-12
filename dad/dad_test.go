package dad

import (
	"bytes"
	"net/netip"
	"testing"
)

var (
	tentative  = netip.MustParseAddr("2001:db8::1")
	other      = netip.MustParseAddr("2001:db8::2")
	unspec     = netip.MustParseAddr("::")
	foreignNS  = Message{Src: unspec, Target: tentative, Nonce: []byte{9, 9, 9, 9, 9, 9}}
	defenderNA = Message{IsNA: true, Src: tentative, HopLimit: 255, Target: tentative}
)

// A probe we sent loops back (or is replayed by an attacker) unchanged:
// the matching nonce marks it as our own and it must not fail DAD.
func TestLoopbackProbeIgnored(t *testing.T) {
	d := New(tentative, Config{})
	pkt, ok := d.NextProbe()
	if !ok {
		t.Fatal("NextProbe returned no probe")
	}
	m, err := Parse(unspec, 255, pkt)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if m.IsNA || m.Target != tentative || len(m.Nonce) != NonceLen {
		t.Fatalf("parsed probe mismatch: %+v", m)
	}
	if got := d.HandleMessage(m); got != VerdictLoopback {
		t.Fatalf("HandleMessage = %v, want VerdictLoopback", got)
	}
	if d.State() != StateTentative {
		t.Fatalf("state = %v, want StateTentative", d.State())
	}
	// A replayed probe must not consume the retry budget either.
	if got := d.HandleMessage(m); got != VerdictLoopback {
		t.Fatalf("replayed HandleMessage = %v, want VerdictLoopback", got)
	}
}

// Another host probing the same address concurrently sends an NS from the
// unspecified address with a nonce that is not ours: a real conflict.
func TestConcurrentProbeConflict(t *testing.T) {
	d := New(tentative, Config{MaxRetries: 1})
	if _, ok := d.NextProbe(); !ok {
		t.Fatal("NextProbe returned no probe")
	}
	if got := d.HandleMessage(foreignNS); got != VerdictConflict {
		t.Fatalf("HandleMessage = %v, want VerdictConflict", got)
	}
	// Retry budget allows one restart with fresh nonces.
	if d.State() != StateTentative || d.ProbesExhausted() {
		t.Fatalf("after first conflict: state=%v exhausted=%v", d.State(), d.ProbesExhausted())
	}
	if _, ok := d.NextProbe(); !ok {
		t.Fatal("retry attempt emitted no probe")
	}
	// Budget exhausted: the address fails and never probes again.
	if got := d.HandleMessage(foreignNS); got != VerdictConflict {
		t.Fatalf("HandleMessage = %v, want VerdictConflict", got)
	}
	if d.State() != StateFailed {
		t.Fatalf("state = %v, want StateFailed", d.State())
	}
	if _, ok := d.NextProbe(); ok {
		t.Fatal("failed address emitted another probe")
	}
	if got := d.HandleMessage(foreignNS); got != VerdictIgnore {
		t.Fatalf("failed run still reacts: %v, want VerdictIgnore", got)
	}
}

// Forged NAs must not disturb DAD: only hop limit 255 with our tentative
// address as target counts as a defender.
func TestForgedNA(t *testing.T) {
	d := New(tentative, Config{})
	forged := []Message{
		{IsNA: true, Src: tentative, HopLimit: 254, Target: tentative}, // forwarded/off-link
		{IsNA: true, Src: tentative, HopLimit: 255, Target: other},     // defends another address
		{IsNA: true, Src: other, HopLimit: 64, Target: tentative},      // low hop limit
	}
	for i, m := range forged {
		if got := d.HandleMessage(m); got != VerdictIgnore {
			t.Fatalf("forged NA %d: verdict = %v, want VerdictIgnore", i, got)
		}
	}
	if d.State() != StateTentative {
		t.Fatalf("state = %v, want StateTentative", d.State())
	}
	if got := d.HandleMessage(defenderNA); got != VerdictConflict {
		t.Fatalf("genuine defender NA: verdict = %v, want VerdictConflict", got)
	}
	if d.State() != StateFailed {
		t.Fatalf("state = %v, want StateFailed (no retries configured)", d.State())
	}
}

// No conflicting traffic: all probes go out, then the address becomes
// valid and stops reacting to ND traffic.
func TestLegitimateConfiguration(t *testing.T) {
	d := New(tentative, Config{Probes: 2})
	var nonces [][]byte
	for {
		pkt, ok := d.NextProbe()
		if !ok {
			break
		}
		m, err := Parse(unspec, 255, pkt)
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		nonces = append(nonces, m.Nonce)
	}
	if len(nonces) != 2 {
		t.Fatalf("sent %d probes, want 2", len(nonces))
	}
	if bytes.Equal(nonces[0], nonces[1]) {
		t.Fatal("probes reused a nonce")
	}
	if !d.ProbesExhausted() {
		t.Fatal("probe budget not exhausted")
	}
	if !d.Finish() || d.State() != StateValid {
		t.Fatalf("Finish: state = %v, want StateValid", d.State())
	}
	if got := d.HandleMessage(defenderNA); got != VerdictIgnore {
		t.Fatalf("valid address reacts to NA: %v, want VerdictIgnore", got)
	}
}

// Parsing rejects malformed or irrelevant packets.
func TestParseRejects(t *testing.T) {
	if _, err := Parse(unspec, 255, []byte{137, 0, 0, 0}); err == nil {
		t.Fatal("accepted non-NS/NA type")
	}
	short := make([]byte, 19)
	short[0] = icmpTypeNS
	if _, err := Parse(unspec, 255, short); err == nil {
		t.Fatal("accepted truncated NS")
	}
	bad := marshalNS(tentative, []byte{1, 2, 3, 4, 5, 6})
	bad[25] = 0 // zero option length
	if _, err := Parse(unspec, 255, bad); err == nil {
		t.Fatal("accepted zero-length option")
	}
}
