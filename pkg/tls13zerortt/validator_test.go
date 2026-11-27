package tls13zerortt

import (
	"errors"
	"testing"
	"time"
)

func newTestValidator(t *testing.T, idempotent IdempotencyPolicy) (*Validator, *SessionTicket, time.Time) {
	t.Helper()
	now := time.Unix(1_700_000_000, 0)
	ticket := newTestTicket(now)
	replay, err := NewExactReplayFilter(time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	v, err := NewValidator(Config{
		Policy:       TicketPolicy{Skew: 500 * time.Millisecond},
		Replay:       replay,
		IsIdempotent: idempotent,
	})
	if err != nil {
		t.Fatal(err)
	}
	return v, ticket, now
}

func admitRequest(t *testing.T, ticket *SessionTicket, now time.Time, withEarlyData bool,
	obfAge uint32, alpn []byte) AdmissionRequest {
	t.Helper()
	msg := buildClientHello(ticket.PSK, ticket.Mode, []byte("ticket-xyz"), obfAge,
		ticket.CipherSuite, alpn, withEarlyData, nil)
	parsed, err := ParseClientHello(msg)
	if err != nil {
		t.Fatal(err)
	}
	return AdmissionRequest{
		Parsed:         parsed,
		IdentityIndex:  0,
		Ticket:         ticket,
		Now:            now,
		OfferedALPN:    alpn,
		RouteKey:       "POST /pay",
		EarlyDataBytes: 256,
	}
}

func TestAdmitHappyPathThenReplayDowngrade(t *testing.T) {
	v, ticket, now := newTestValidator(t, func(string) bool { return true })
	obf := uint32(10_000) + ticket.TicketAgeAdd

	first := admitRequest(t, ticket, now, true, obf, []byte("h2"))
	res := v.Admit(first)
	if res.Decision != DecisionAcceptEarlyData || !res.EarlyData {
		t.Fatalf("first = %+v, want accept early data", res)
	}
	if err := ticket.ChargeEarlyData(256); err != nil {
		t.Fatal(err)
	}

	// Replayed ClientHello (same ticket identity) carrying early_data again.
	replay := admitRequest(t, ticket, now.Add(100*time.Millisecond), true,
		obf+100, []byte("h2"))
	res = v.Admit(replay)
	if res.Decision != DecisionResume1RTT {
		t.Fatalf("replay decision = %v, want 1-RTT downgrade", res.Decision)
	}
	if !errors.Is(res.Reason, ErrReplayed) {
		t.Fatalf("replay reason = %v, want ErrReplayed", res.Reason)
	}
	if res.EarlyData {
		t.Fatal("replay must never enable early data")
	}
}

func TestAdmitForgeBinderFailsFullHandshake(t *testing.T) {
	v, ticket, now := newTestValidator(t, nil)
	obf := uint32(10_000) + ticket.TicketAgeAdd

	msg := buildClientHello(ticket.PSK, ticket.Mode, []byte("ticket-xyz"), obf,
		ticket.CipherSuite, []byte("h2"), true, func(m []byte) []byte {
			// Tamper with the ClientHello random so the binder no longer binds.
			m[10] ^= 0x01
			return m
		})
	parsed, err := ParseClientHello(msg)
	if err != nil {
		t.Fatal(err)
	}
	res := v.Admit(AdmissionRequest{
		Parsed: parsed, Ticket: ticket, Now: now,
		OfferedALPN: []byte("h2"),
	})
	if res.Decision != DecisionFullHandshake {
		t.Fatalf("forged binder decision = %v, want full handshake", res.Decision)
	}
	if !errors.Is(res.Reason, ErrBinderMismatch) {
		t.Fatalf("reason = %v, want binder mismatch", res.Reason)
	}
}

func TestAdmitSkewDowngradesTo1RTT(t *testing.T) {
	v, ticket, now := newTestValidator(t, nil)
	// Recovered age is 10s measured + 2s skew, beyond the 500ms window.
	obf := uint32(12_000) + ticket.TicketAgeAdd
	req := admitRequest(t, ticket, now, true, obf, []byte("h2"))
	res := v.Admit(req)
	if res.Decision != DecisionResume1RTT || !errors.Is(res.Reason, ErrTicketAgeSkew) {
		t.Fatalf("skew result = %+v", res)
	}
}

func TestAdmitExpiredTicketFullHandshake(t *testing.T) {
	v, ticket, now := newTestValidator(t, nil)
	ticket.Lifetime = time.Second // issued 10s ago in newTestTicket
	obf := uint32(10_000) + ticket.TicketAgeAdd
	req := admitRequest(t, ticket, now, true, obf, []byte("h2"))
	res := v.Admit(req)
	if res.Decision != DecisionFullHandshake || !errors.Is(res.Reason, ErrTicketExpired) {
		t.Fatalf("expiry result = %+v", res)
	}
}

func TestAdmitParameterMismatchDowngrades(t *testing.T) {
	v, ticket, now := newTestValidator(t, nil)
	obf := uint32(10_000) + ticket.TicketAgeAdd

	t.Run("alpn", func(t *testing.T) {
		msg := buildClientHello(ticket.PSK, ticket.Mode, []byte("id-alpn"), obf,
			ticket.CipherSuite, []byte("http/1.1"), true, nil)
		parsed, _ := ParseClientHello(msg)
		res := v.Admit(AdmissionRequest{
			Parsed: parsed, Ticket: ticket, Now: now,
			OfferedALPN: []byte("http/1.1"),
		})
		if res.Decision != DecisionResume1RTT || !errors.Is(res.Reason, ErrALPNMismatch) {
			t.Fatalf("alpn result = %+v", res)
		}
	})

	t.Run("cipher_suite", func(t *testing.T) {
		msg := buildClientHello(ticket.PSK, ticket.Mode, []byte("id-cs"), obf,
			0x1302, []byte("h2"), true, nil)
		parsed, _ := ParseClientHello(msg)
		res := v.Admit(AdmissionRequest{
			Parsed: parsed, Ticket: ticket, Now: now,
			OfferedALPN: []byte("h2"),
		})
		if res.Decision != DecisionResume1RTT || !errors.Is(res.Reason, ErrCipherSuiteMismatch) {
			t.Fatalf("cipher result = %+v", res)
		}
	})
}

func TestAdmitNonIdempotentRejected(t *testing.T) {
	// Policy marks every route non-idempotent.
	v, ticket, now := newTestValidator(t, func(string) bool { return false })
	obf := uint32(10_000) + ticket.TicketAgeAdd
	req := admitRequest(t, ticket, now, true, obf, []byte("h2"))
	res := v.Admit(req)
	if res.Decision != DecisionResume1RTT || !errors.Is(res.Reason, ErrNonIdempotent) {
		t.Fatalf("non-idempotent result = %+v", res)
	}

	// Because early data was refused, the identity must not have consumed its
	// single-use slot: a later idempotent admission still succeeds.
	v.cfg.IsIdempotent = func(string) bool { return true }
	req2 := admitRequest(t, ticket, now.Add(50*time.Millisecond), true, obf+50, []byte("h2"))
	res2 := v.Admit(req2)
	if res2.Decision != DecisionAcceptEarlyData {
		t.Fatalf("subsequent idempotent admission = %+v", res2)
	}
}

func TestAdmitNoEarlyDataResumes1RTT(t *testing.T) {
	v, ticket, now := newTestValidator(t, nil)
	obf := uint32(10_000) + ticket.TicketAgeAdd
	req := admitRequest(t, ticket, now, false, obf, []byte("h2"))
	res := v.Admit(req)
	if res.Decision != DecisionResume1RTT || res.EarlyData {
		t.Fatalf("no-early-data result = %+v", res)
	}
}

func TestAdmitBoundedRegistrySaturationFailsClosed(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	ticket := newTestTicket(now)
	replay, _ := NewBoundedReplayFilter(time.Minute, 1)
	v, err := NewValidator(Config{
		Policy: TicketPolicy{Skew: time.Second},
		Replay: replay,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Consume the single capacity slot with another identity.
	replay.CheckAndRegister("other", now)

	obf := uint32(10_000) + ticket.TicketAgeAdd
	req := admitRequest(t, ticket, now, true, obf, []byte("h2"))
	res := v.Admit(req)
	if res.Decision != DecisionResume1RTT || !errors.Is(res.Reason, ErrRegistryUnavailable) {
		t.Fatalf("saturation result = %+v", res)
	}
}

func TestNewValidatorRequiresReplayFilter(t *testing.T) {
	if _, err := NewValidator(Config{}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("missing replay filter = %v", err)
	}
}

func TestAdmitNilInputsAndNegativeSkew(t *testing.T) {
	v, _, _ := newTestValidator(t, nil)
	if res := v.Admit(AdmissionRequest{}); res.Decision != DecisionFullHandshake {
		t.Fatalf("nil request = %+v", res)
	}
	if _, err := NewValidator(Config{
		Replay: mustReplay(t),
		Policy: TicketPolicy{Skew: -1},
	}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("negative skew = %v", err)
	}
}

func TestAdmitEarlyDataByteBudgetOverflow(t *testing.T) {
	v, ticket, now := newTestValidator(t, nil)
	ticket.EarlyDataUsed = 900
	obf := uint32(10_000) + ticket.TicketAgeAdd
	req := admitRequest(t, ticket, now, true, obf, []byte("h2"))
	req.EarlyDataBytes = 200 // 900 + 200 > 1024
	res := v.Admit(req)
	if res.Decision != DecisionResume1RTT || !errors.Is(res.Reason, ErrEarlyDataOverflow) {
		t.Fatalf("overflow result = %+v", res)
	}
}

func mustReplay(t *testing.T) ReplayFilter {
	t.Helper()
	f, err := NewExactReplayFilter(time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return f
}
