package sctpauth

import (
	"bytes"
	"errors"
	"testing"
	"time"
)

func newTestClock() (*time.Time, func() time.Time) {
	now := time.Date(2026, 11, 28, 9, 0, 0, 0, time.UTC)
	return &now, func() time.Time { return now }
}

func testKeyRing() *KeyRing {
	return NewKeyRing(bytes.Repeat([]byte{0xA1}, KeySize))
}

func validParams() HandshakeParams {
	return HandshakeParams{
		InitiateTag: 100,
		MyTag:       200,
		InitiateTSN: 1000,
		MyTSN:       2000,
		PeerAddrs:   []string{"192.0.2.10", "2001:db8::10"},
	}
}

func TestCookieRoundTripBindsAllFields(t *testing.T) {
	_, clock := newTestClock()
	keys := testKeyRing()
	signer := NewCookieSigner(keys).withClock(clock)
	verifier := NewCookieVerifier(keys).withClock(clock)

	cookie, wire, err := signer.Issue(validParams())
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	got, err := verifier.Verify(CookieEcho{
		SourceAddr:      "192.0.2.10",
		VerificationTag: 100,
		Cookie:          wire,
	})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if got.MyTag != 200 || got.MyTSN != 2000 || got.InitiateTSN != 1000 {
		t.Fatalf("recovered cookie mismatch: %+v", got)
	}
	if len(got.PeerAddrs) != 2 || got.PeerAddrs[0] != "192.0.2.10" {
		t.Fatalf("bound address list wrong: %#v", got.PeerAddrs)
	}
	if got.Nonce != cookie.Nonce {
		t.Fatal("nonce not preserved through marshal")
	}
}

func TestCookieForgeriesAreRejected(t *testing.T) {
	_, clock := newTestClock()
	keys := testKeyRing()
	signer := NewCookieSigner(keys).withClock(clock)
	verifier := NewCookieVerifier(keys).withClock(clock)

	_, wire, err := signer.Issue(validParams())
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	t.Run("bit flip in body", func(t *testing.T) {
		forged := append([]byte(nil), wire...)
		forged[len(cookieMagic)+5] ^= 0xFF
		if _, err := verifier.Verify(CookieEcho{
			SourceAddr:      "192.0.2.10",
			VerificationTag: 100,
			Cookie:          forged,
		}); !errors.Is(err, ErrCookieSignature) {
			t.Fatalf("tampered body error = %v, want ErrCookieSignature", err)
		}
	})

	t.Run("bit flip in HMAC", func(t *testing.T) {
		forged := append([]byte(nil), wire...)
		forged[len(forged)-1] ^= 0x01
		if _, err := verifier.Verify(CookieEcho{
			SourceAddr:      "192.0.2.10",
			VerificationTag: 100,
			Cookie:          forged,
		}); !errors.Is(err, ErrCookieSignature) {
			t.Fatalf("tampered MAC error = %v, want ErrCookieSignature", err)
		}
	})

	t.Run("foreign key cannot authenticate", func(t *testing.T) {
		attacker := NewKeyRing(bytes.Repeat([]byte{0x42}, KeySize))
		if _, err := NewCookieVerifier(attacker).withClock(clock).Verify(CookieEcho{
			SourceAddr:      "192.0.2.10",
			VerificationTag: 100,
			Cookie:          wire,
		}); !errors.Is(err, ErrCookieSignature) {
			t.Fatalf("foreign key error = %v, want ErrCookieSignature", err)
		}
	})

	t.Run("truncated cookie fails authentication", func(t *testing.T) {
		if _, err := verifier.Verify(CookieEcho{
			SourceAddr:      "192.0.2.10",
			VerificationTag: 100,
			Cookie:          wire[:len(wire)-40],
		}); !errors.Is(err, ErrCookieSignature) {
			t.Fatalf("truncation error = %v, want ErrCookieSignature", err)
		}
	})

	t.Run("bad magic is malformed", func(t *testing.T) {
		badMagic := append([]byte(nil), wire...)
		badMagic[0] = 'X'
		if _, err := verifier.Verify(CookieEcho{
			SourceAddr:      "192.0.2.10",
			VerificationTag: 100,
			Cookie:          badMagic,
		}); !errors.Is(err, ErrMalformedCookie) {
			t.Fatalf("bad magic error = %v, want ErrMalformedCookie", err)
		}
	})
}

func TestCookieExpiryAndNonceReplay(t *testing.T) {
	nowPtr, clock := newTestClock()
	keys := testKeyRing()
	signer := NewCookieSigner(keys).withClock(clock)
	signer.SetLifespan(10 * time.Second)
	verifier := NewCookieVerifier(keys).withClock(clock)

	_, wire, err := signer.Issue(validParams())
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	echo := CookieEcho{
		SourceAddr:      "192.0.2.10",
		VerificationTag: 100,
		Cookie:          wire,
	}

	if _, err := verifier.Verify(echo); err != nil {
		t.Fatalf("first presentation: %v", err)
	}
	if _, err := verifier.Verify(echo); !errors.Is(err, ErrCookieReplay) {
		t.Fatalf("nonce replay error = %v, want ErrCookieReplay", err)
	}

	*nowPtr = nowPtr.Add(11 * time.Second)
	freshVerifier := NewCookieVerifier(keys).withClock(clock)
	if _, err := freshVerifier.Verify(echo); !errors.Is(err, ErrCookieExpired) {
		t.Fatalf("expired cookie error = %v, want ErrCookieExpired", err)
	}
}

func TestCookieTagAndSourceEnforcement(t *testing.T) {
	_, clock := newTestClock()
	keys := testKeyRing()
	signer := NewCookieSigner(keys).withClock(clock)
	verifier := NewCookieVerifier(keys).withClock(clock)

	_, wire, err := signer.Issue(validParams())
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	t.Run("zero verification tag", func(t *testing.T) {
		_, err := verifier.Verify(CookieEcho{
			SourceAddr:      "192.0.2.10",
			VerificationTag: 0,
			Cookie:          wire,
		})
		if !errors.Is(err, ErrForbiddenTag) {
			t.Fatalf("zero tag error = %v, want ErrForbiddenTag", err)
		}
	})

	t.Run("wrong verification tag", func(t *testing.T) {
		_, err := verifier.Verify(CookieEcho{
			SourceAddr:      "192.0.2.10",
			VerificationTag: 999,
			Cookie:          wire,
		})
		if !errors.Is(err, ErrVerificationTag) {
			t.Fatalf("wrong tag error = %v, want ErrVerificationTag", err)
		}
	})

	t.Run("cross-source replay from unbound address", func(t *testing.T) {
		_, err := verifier.Verify(CookieEcho{
			SourceAddr:      "198.51.100.66",
			VerificationTag: 100,
			Cookie:          wire,
		})
		if !errors.Is(err, ErrSourceAddress) {
			t.Fatalf("cross-source error = %v, want ErrSourceAddress", err)
		}
	})

	t.Run("zero initiate tag cannot be issued", func(t *testing.T) {
		bad := validParams()
		bad.InitiateTag = 0
		if _, _, err := signer.Issue(bad); !errors.Is(err, ErrForbiddenTag) {
			t.Fatalf("zero initiate tag error = %v, want ErrForbiddenTag", err)
		}
	})
}

func TestCookieSurvivesOneKeyRotation(t *testing.T) {
	nowPtr, clock := newTestClock()
	keys := testKeyRing()
	signer := NewCookieSigner(keys).withClock(clock)

	_, wire, err := signer.Issue(validParams())
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	keys.Rotate(bytes.Repeat([]byte{0xB7}, KeySize))
	verifier := NewCookieVerifier(keys).withClock(clock)
	if _, err := verifier.Verify(CookieEcho{
		SourceAddr:      "192.0.2.10",
		VerificationTag: 100,
		Cookie:          wire,
	}); err != nil {
		t.Fatalf("previous-generation cookie should verify: %v", err)
	}

	keys.Rotate(bytes.Repeat([]byte{0xC9}, KeySize))
	*nowPtr = nowPtr.Add(time.Second)
	if _, err := NewCookieVerifier(keys).withClock(clock).Verify(CookieEcho{
		SourceAddr:      "192.0.2.10",
		VerificationTag: 100,
		Cookie:          wire,
	}); !errors.Is(err, ErrCookieSignature) {
		t.Fatalf("two-rotations-old cookie error = %v, want ErrCookieSignature", err)
	}
}

func TestDRBGNoncesAreUnique(t *testing.T) {
	keys := testKeyRing()
	seen := make(map[[nonceSize]byte]struct{}, 64)
	for i := 0; i < 64; i++ {
		nonce := keys.Nonce()
		if _, ok := seen[nonce]; ok {
			t.Fatal("DRBG produced a repeated nonce")
		}
		seen[nonce] = struct{}{}
	}
}
