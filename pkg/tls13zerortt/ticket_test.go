package tls13zerortt

import (
	"errors"
	"testing"
	"time"
)

func TestRecoverTicketAgeModulo(t *testing.T) {
	// RFC 8446 4.2.11.1: ticket_age = (obfuscated - age_add) mod 2^32.
	var wrapA, wrapB uint32 = 200, 1000
	cases := []struct {
		obf, add, want uint32
	}{
		{1000, 200, 800},
		{wrapA, wrapB, wrapA - wrapB}, // wraps mod 2^32
		{0, 1, 0xFFFFFFFF},            // classic wrap
		{0xFFFFFFFF, 0xFFFFFFFF, 0},
	}
	for _, c := range cases {
		if got := RecoverTicketAge(c.obf, c.add); got != c.want {
			t.Errorf("RecoverTicketAge(%d,%d)=%d want %d", c.obf, c.add, got, c.want)
		}
	}
}

func newTestTicket(now time.Time) *SessionTicket {
	return &SessionTicket{
		PSK:              testPSK(),
		Mode:             ResumptionPSK,
		TicketAgeAdd:     0x11223344,
		Lifetime:         time.Hour,
		IssuedAt:         now.Add(-10 * time.Second),
		CipherSuite:      0x1301,
		ALPN:             []byte("h2"),
		MaxEarlyDataSize: 1024,
	}
}

func TestValidateTicketSkewWindow(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	ticket := newTestTicket(now)
	measuredAge := uint32(10_000) // 10s in ms, matches IssuedAt offset.

	policy := TicketPolicy{Skew: 500 * time.Millisecond}

	// Obfuscated age that recovers to exactly the measured age.
	obf := measuredAge + ticket.TicketAgeAdd
	obs := TicketObservation{
		Ticket:              ticket,
		ObfuscatedTicketAge: obf,
		Now:                 now,
		OfferedCipherSuite:  0x1301,
		OfferedALPN:         []byte("h2"),
		ClientSentEarlyData: true,
	}
	if err := ValidateTicket(obs, policy); err != nil {
		t.Fatalf("in-window validation failed: %v", err)
	}

	// Both skew boundaries must be enforced.
	plusBoundary := measuredAge + ticket.TicketAgeAdd + 500 // +500ms, at edge
	obs.ObfuscatedTicketAge = plusBoundary
	if err := ValidateTicket(obs, policy); err != nil {
		t.Fatalf("+skew edge should be accepted: %v", err)
	}
	obs.ObfuscatedTicketAge = plusBoundary + 1
	if err := ValidateTicket(obs, policy); !errors.Is(err, ErrTicketAgeSkew) {
		t.Fatalf("beyond +skew want ErrTicketAgeSkew, got %v", err)
	}

	minusBoundary := measuredAge + ticket.TicketAgeAdd - 500 // -500ms, at edge
	obs.ObfuscatedTicketAge = minusBoundary
	if err := ValidateTicket(obs, policy); err != nil {
		t.Fatalf("-skew edge should be accepted: %v", err)
	}
	obs.ObfuscatedTicketAge = minusBoundary - 1
	if err := ValidateTicket(obs, policy); !errors.Is(err, ErrTicketAgeSkew) {
		t.Fatalf("beyond -skew want ErrTicketAgeSkew, got %v", err)
	}
}

func TestValidateTicketExpiry(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	ticket := newTestTicket(now)
	ticket.Lifetime = 5 * time.Second
	// Issued 10s ago but lifetime 5s.
	obs := TicketObservation{
		Ticket:              ticket,
		ObfuscatedTicketAge: ticket.TicketAgeAdd + 10_000,
		Now:                 now,
		OfferedCipherSuite:  0x1301,
		OfferedALPN:         []byte("h2"),
	}
	if err := ValidateTicket(obs, TicketPolicy{Skew: time.Second}); !errors.Is(err, ErrTicketExpired) {
		t.Fatalf("want ErrTicketExpired, got %v", err)
	}
}

func TestValidateTicketParameterMismatch(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	policy := TicketPolicy{Skew: time.Second}
	base := func() TicketObservation {
		ticket := newTestTicket(now)
		return TicketObservation{
			Ticket:              ticket,
			ObfuscatedTicketAge: ticket.TicketAgeAdd + 10_000,
			Now:                 now,
			OfferedCipherSuite:  0x1301,
			OfferedALPN:         []byte("h2"),
			ClientSentEarlyData: true,
		}
	}

	t.Run("cipher_suite", func(t *testing.T) {
		obs := base()
		obs.OfferedCipherSuite = 0x1302
		if err := ValidateTicket(obs, policy); !errors.Is(err, ErrCipherSuiteMismatch) {
			t.Fatalf("want cipher mismatch, got %v", err)
		}
	})

	t.Run("alpn", func(t *testing.T) {
		obs := base()
		obs.OfferedALPN = []byte("http/1.1")
		if err := ValidateTicket(obs, policy); !errors.Is(err, ErrALPNMismatch) {
			t.Fatalf("want alpn mismatch, got %v", err)
		}
	})

	t.Run("alpn_absent_vs_negotiated", func(t *testing.T) {
		obs := base()
		obs.OfferedALPN = nil
		if err := ValidateTicket(obs, policy); !errors.Is(err, ErrALPNMismatch) {
			t.Fatalf("want alpn mismatch, got %v", err)
		}
	})

	t.Run("early_data_not_allowed", func(t *testing.T) {
		obs := base()
		obs.Ticket.MaxEarlyDataSize = 0
		if err := ValidateTicket(obs, policy); !errors.Is(err, ErrEarlyDataNotNegotiated) {
			t.Fatalf("want early data not negotiated, got %v", err)
		}
	})
}

func TestEarlyDataByteBudget(t *testing.T) {
	ticket := newTestTicket(time.Now())
	if err := ticket.ChargeEarlyData(0); err != nil {
		t.Fatalf("zero charge must be a no-op: %v", err)
	}
	if err := ticket.ChargeEarlyData(512); err != nil {
		t.Fatal(err)
	}
	if err := ticket.ChargeEarlyData(512); err != nil {
		t.Fatalf("exact budget fill should pass: %v", err)
	}
	if err := ticket.ChargeEarlyData(1); !errors.Is(err, ErrEarlyDataOverflow) {
		t.Fatalf("want overflow, got %v", err)
	}
	if ticket.EarlyDataUsed != 1024 {
		t.Fatalf("counter must not mutate on overflow: %d", ticket.EarlyDataUsed)
	}
}

func TestValidateTicketBackwardsClock(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	ticket := newTestTicket(now)
	// Receive time before issuance: negative measured age must fail skew.
	obs := TicketObservation{
		Ticket:              ticket,
		ObfuscatedTicketAge: ticket.TicketAgeAdd,
		Now:                 ticket.IssuedAt.Add(-time.Second),
		OfferedCipherSuite:  0x1301,
		OfferedALPN:         []byte("h2"),
	}
	if err := ValidateTicket(obs, TicketPolicy{Skew: time.Second}); !errors.Is(err, ErrTicketAgeSkew) {
		t.Fatalf("want skew on backwards clock, got %v", err)
	}
}
