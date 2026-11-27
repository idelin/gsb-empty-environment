package sctpauth

import (
	"errors"
	"testing"
	"time"
)

func TestMalformedCookieBodies(t *testing.T) {
	_, clock := newTestClock()
	keys := testKeyRing()
	signer := NewCookieSigner(keys).withClock(clock)
	verifier := NewCookieVerifier(keys).withClock(clock)

	_, wire, err := signer.Issue(validParams())
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	t.Run("unsupported version byte", func(t *testing.T) {
		bad := append([]byte(nil), wire...)
		bad[len(cookieMagic)] = 99
		body := bad[len(cookieMagic)+1 : len(bad)-hmacSize]
		body[0] = 99
		if _, err := verifier.Verify(CookieEcho{
			SourceAddr:      "192.0.2.10",
			VerificationTag: 100,
			Cookie:          bad,
		}); !errors.Is(err, ErrMalformedCookie) && !errors.Is(err, ErrCookieSignature) {
			t.Fatalf("bad version error = %v", err)
		}
	})

	t.Run("empty cookie", func(t *testing.T) {
		if _, err := verifier.Verify(CookieEcho{
			SourceAddr:      "192.0.2.10",
			VerificationTag: 100,
			Cookie:          nil,
		}); !errors.Is(err, ErrMalformedCookie) {
			t.Fatalf("empty cookie error = %v, want ErrMalformedCookie", err)
		}
	})
}

func TestHandleINITValidation(t *testing.T) {
	server := newTestServer(t)

	if _, err := server.HandleINIT(InitRequest{
		SourceAddr:  peerPrimary,
		InitiateTag: 0,
	}); !errors.Is(err, ErrForbiddenTag) {
		t.Fatalf("zero initiate tag error = %v, want ErrForbiddenTag", err)
	}

	if _, err := server.HandleINIT(InitRequest{
		SourceAddr:  "",
		InitiateTag: 1,
	}); !errors.Is(err, ErrSourceAddress) {
		t.Fatalf("empty source error = %v, want ErrSourceAddress", err)
	}

	ack, err := server.HandleINIT(InitRequest{
		SourceAddr:  peerPrimary,
		InitiateTag: 909,
		InitiateTSN: 5,
	})
	if err != nil {
		t.Fatalf("valid INIT: %v", err)
	}
	if ack.MyTag == 0 || ack.CookieState.MyTag != ack.MyTag {
		t.Fatal("server tag must be non-zero and echoed in cookie")
	}
}

func TestPathStateStringerAndDefaultWindow(t *testing.T) {
	if PathUnconfirmed.String() != "UNCONFIRMED" || PathConfirmed.String() != "CONFIRMED" {
		t.Fatal("unexpected path state labels")
	}

	window := NewReceiveWindow(100, 0)
	if window.WindowSize() != DefaultReceiveWindow {
		t.Fatalf("default window = %d, want %d", window.WindowSize(), DefaultReceiveWindow)
	}

	big := NewReceiveWindow(0, maxReceiveWindow+10)
	if big.WindowSize() != maxReceiveWindow {
		t.Fatalf("window cap = %d, want %d", big.WindowSize(), maxReceiveWindow)
	}
}

func TestReplayCacheSweepsExpiredNonces(t *testing.T) {
	nowPtr, clock := newTestClock()
	keys := testKeyRing()
	signer := NewCookieSigner(keys).withClock(clock)
	signer.SetLifespan(time.Second)
	verifier := NewCookieVerifier(keys).withClock(clock)

	_, wire, err := signer.Issue(validParams())
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if _, err := verifier.Verify(CookieEcho{
		SourceAddr:      "192.0.2.10",
		VerificationTag: 100,
		Cookie:          wire,
	}); err != nil {
		t.Fatalf("first use: %v", err)
	}

	*nowPtr = nowPtr.Add(2 * time.Second)
	verifier.sweptAt = replaySweepThreshold
	verifier.sweepExpired(verifier.now())
	if len(verifier.seen) != 0 {
		t.Fatalf("expired nonces should be swept, have %d", len(verifier.seen))
	}
}

func TestCanonicalAddressHelpers(t *testing.T) {
	if got := canonicalAddr("not-an-ip"); got != "not-an-ip" {
		t.Fatalf("invalid addr passthrough = %q", got)
	}
	set := canonicalAddrSet([]string{"10.0.0.1", "", "10.0.0.1", "10.0.0.2"})
	if len(set) != 2 {
		t.Fatalf("canonical set = %#v, want 2 unique", set)
	}
}
