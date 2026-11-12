package pqpin

import (
	"crypto/tls"
	"testing"
	"time"
)

const hybrid = uint16(tls.X25519MLKEM768)
const classic = uint16(tls.X25519)

func TestIsHybridGroup(t *testing.T) {
	if !IsHybridGroup(hybrid) {
		t.Fatalf("X25519MLKEM768 must be recognized as hybrid")
	}
	if IsHybridGroup(classic) || IsHybridGroup(uint16(tls.CurveP256)) {
		t.Fatalf("classical groups must not be treated as hybrid")
	}
	if IsHybridGroup(0) || IsHybridGroup(0xFFFF) {
		t.Fatalf("unknown/zero groups must not be treated as hybrid")
	}
}

func TestUnpinnedHostIsAllowed(t *testing.T) {
	p := New(WithTTL(time.Hour))
	obs := Observation{ServerID: "example.com:443", NegotiatedGroup: classic}
	if err := p.Evaluate(obs); err != nil {
		t.Fatalf("unpinned host must be allowed, got %v", err)
	}
	if got := p.Lookup("example.com:443"); got != nil {
		t.Fatalf("Evaluate must not write pins")
	}
}

func TestCommitOnlyFromHybridHandshake(t *testing.T) {
	p := New(WithTTL(time.Hour))

	// A classic handshake must never create a pin.
	err := p.Commit(Observation{ServerID: "h:443", NegotiatedGroup: classic})
	if _, ok := AsDowngrade(err); !ok {
		t.Fatalf("classic Commit must return DowngradeError, got %v", err)
	}
	if p.Lookup("h:443") != nil {
		t.Fatalf("classic handshake created a pin")
	}

	if err := p.Commit(Observation{ServerID: "h:443", NegotiatedGroup: hybrid}); err != nil {
		t.Fatalf("hybrid Commit failed: %v", err)
	}
	rec := p.Lookup("h:443")
	if rec == nil || rec.Group != hybrid {
		t.Fatalf("hybrid pin missing: %+v", rec)
	}
}

func TestServerHelloDowngradeAfterPin(t *testing.T) {
	p := New(WithTTL(time.Hour))
	if err := p.Commit(Observation{ServerID: "h:443", NegotiatedGroup: hybrid}); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		obs  Observation
		want Reason
	}{
		{
			name: "classic x25519 selection",
			obs:  Observation{ServerID: "h:443", NegotiatedGroup: classic},
			want: ReasonServerHello,
		},
		{
			name: "p256 selection",
			obs:  Observation{ServerID: "h:443", NegotiatedGroup: uint16(tls.CurveP256)},
			want: ReasonServerHello,
		},
		{
			name: "no key share observed",
			obs:  Observation{ServerID: "h:443", NegotiatedGroup: 0},
			want: ReasonServerHello,
		},
		{
			name: "hybrid still allowed",
			obs:  Observation{ServerID: "h:443", NegotiatedGroup: hybrid},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := p.Evaluate(tc.obs)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("expected allow, got %v", err)
				}
				return
			}
			de, ok := AsDowngrade(err)
			if !ok || de.Reason != tc.want {
				t.Fatalf("want %s, got %v", tc.want, err)
			}
		})
	}
}

func TestHRRDowngrade(t *testing.T) {
	p := New(WithTTL(time.Hour))
	_ = p.Commit(Observation{ServerID: "h:443", NegotiatedGroup: hybrid})

	// Classic group in HRR is rejected even when the final ServerHello would
	// have been hybrid, because the attacker already steered group selection.
	err := p.Evaluate(Observation{
		ServerID:        "h:443",
		HadHRR:          true,
		HRRGroup:        classic,
		NegotiatedGroup: hybrid,
	})
	de, ok := AsDowngrade(err)
	if !ok || de.Reason != ReasonHRR {
		t.Fatalf("want HRR downgrade, got %v", err)
	}

	// A hybrid HRR is fine.
	err = p.Evaluate(Observation{
		ServerID:        "h:443",
		HadHRR:          true,
		HRRGroup:        hybrid,
		NegotiatedGroup: hybrid,
	})
	if err != nil {
		t.Fatalf("hybrid HRR must be allowed, got %v", err)
	}
}

func TestResumptionDowngrade(t *testing.T) {
	p := New(WithTTL(time.Hour))
	_ = p.Commit(Observation{ServerID: "h:443", NegotiatedGroup: hybrid})

	// Resumption of a classic-origin session is downgrade regardless of the
	// fresh key share the client offered.
	err := p.Evaluate(Observation{
		ServerID:        "h:443",
		Resumed:         true,
		ResumeHybrid:    false,
		NegotiatedGroup: hybrid,
	})
	de, ok := AsDowngrade(err)
	if !ok || de.Reason != ReasonResume {
		t.Fatalf("want resumption downgrade, got %v", err)
	}

	// Resumption of a hybrid-origin session is allowed even though a resumed
	// handshake carries no fresh key share group.
	err = p.Evaluate(Observation{ServerID: "h:443", Resumed: true, ResumeHybrid: true})
	if err != nil {
		t.Fatalf("hybrid resumption must be allowed, got %v", err)
	}
}

func TestExpiryBoundary(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	clock := now
	p := New(WithTTL(time.Hour), WithClock(func() time.Time { return clock }))

	if err := p.Commit(Observation{ServerID: "h:443", NegotiatedGroup: hybrid}); err != nil {
		t.Fatal(err)
	}

	clock = now.Add(time.Hour - time.Nanosecond)
	if err := p.Evaluate(Observation{ServerID: "h:443", NegotiatedGroup: classic}); err == nil {
		t.Fatalf("classic selection must still be blocked before expiry")
	}

	// Inclusive boundary: exactly at ExpiresAt the pin is still valid.
	clock = now.Add(time.Hour)
	if err := p.Evaluate(Observation{ServerID: "h:443", NegotiatedGroup: classic}); err == nil {
		t.Fatalf("pin must remain valid at exactly ExpiresAt")
	}
	if p.Lookup("h:443") == nil {
		t.Fatalf("pin must still exist at exactly ExpiresAt")
	}

	// One nanosecond later the pin is gone; classic handshakes are allowed and
	// can establish a fresh pin.
	clock = now.Add(time.Hour + time.Nanosecond)
	if err := p.Evaluate(Observation{ServerID: "h:443", NegotiatedGroup: classic}); err != nil {
		t.Fatalf("expired pin must not block, got %v", err)
	}
	if p.Lookup("h:443") != nil {
		t.Fatalf("expired pin must be lazily deleted")
	}
	if err := p.Commit(Observation{ServerID: "h:443", NegotiatedGroup: hybrid}); err != nil {
		t.Fatal(err)
	}
	if p.Lookup("h:443") == nil {
		t.Fatalf("fresh pin after expiry was not written")
	}
}

func TestCommitRefreshesExpiry(t *testing.T) {
	start := time.Unix(1_700_000_000, 0)
	clock := start
	p := New(WithTTL(time.Hour), WithClock(func() time.Time { return clock }))

	_ = p.Commit(Observation{ServerID: "h:443", NegotiatedGroup: hybrid})
	first := *p.Lookup("h:443")

	clock = start.Add(30 * time.Minute)
	_ = p.Commit(Observation{ServerID: "h:443", NegotiatedGroup: hybrid})
	second := *p.Lookup("h:443")

	if first.CreatedAt != second.CreatedAt {
		t.Fatalf("CreatedAt must be stable across refreshes")
	}
	if second.ExpiresAt <= first.ExpiresAt {
		t.Fatalf("refresh must extend ExpiresAt")
	}
}

func TestNormalizeServerID(t *testing.T) {
	cases := []struct {
		host, port, want string
	}{
		{"example.com", "443", "example.com:443"},
		{"example.com", "", "example.com:443"},
		{"1.2.3.4", "8443", "1.2.3.4:8443"},
		{"::1", "443", "[::1]:443"},
	}
	for _, tc := range cases {
		if got := NormalizeServerID(tc.host, tc.port); got != tc.want {
			t.Errorf("NormalizeServerID(%q,%q) = %q, want %q", tc.host, tc.port, got, tc.want)
		}
	}
}
