package netsec

import (
	"bytes"
	"errors"
	"testing"
)

func TestCookieMintUnwrapRoundTrip(t *testing.T) {
	ring, err := NewCookieKeyRing(AEADAESGCM128, bytes.Repeat([]byte{0x01}, 16))
	if err != nil {
		t.Fatalf("NewCookieKeyRing: %v", err)
	}
	secrets, err := GenerateSecrets(AEADAESGCM128)
	if err != nil {
		t.Fatalf("GenerateSecrets: %v", err)
	}
	cookie, err := ring.Mint(secrets)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	recovered, err := ring.Unwrap(cookie)
	if err != nil {
		t.Fatalf("Unwrap: %v", err)
	}
	if recovered.AEAD != secrets.AEAD ||
		!bytes.Equal(recovered.C2S, secrets.C2S) ||
		!bytes.Equal(recovered.S2C, secrets.S2C) {
		t.Fatal("round-tripped secrets do not match original")
	}
}

func TestCookieRotationKeepsOldCookiesValid(t *testing.T) {
	clock := newFakeClock()
	srv := newTestServer(t, clock)
	secrets, err := GenerateSecrets(AEADAESGCM128)
	if err != nil {
		t.Fatalf("GenerateSecrets: %v", err)
	}

	oldCookie, err := srv.MintCookie(secrets)
	if err != nil {
		t.Fatalf("MintCookie before rotation: %v", err)
	}
	if err := srv.RotateCookieKey(AEADAESGCM128, bytes.Repeat([]byte{0x02}, 16)); err != nil {
		t.Fatalf("RotateCookieKey: %v", err)
	}

	// 轮换后旧 Cookie 仍可解封，新 Cookie 由新密钥签发。
	if _, err := srv.keyRing.Unwrap(oldCookie); err != nil {
		t.Fatalf("old cookie must survive rotation: %v", err)
	}
	newCookie, err := srv.MintCookie(secrets)
	if err != nil {
		t.Fatalf("MintCookie after rotation: %v", err)
	}
	if bytes.Equal(newCookie, oldCookie) {
		t.Fatal("cookies minted under different keys must differ")
	}
	if _, err := srv.keyRing.Unwrap(newCookie); err != nil {
		t.Fatalf("new cookie must unwrap: %v", err)
	}
}

func TestForgedCookieRejected(t *testing.T) {
	ring, err := NewCookieKeyRing(AEADAESGCM128, bytes.Repeat([]byte{0x03}, 16))
	if err != nil {
		t.Fatalf("NewCookieKeyRing: %v", err)
	}
	if _, err := ring.Unwrap(bytes.Repeat([]byte{0xAA}, 40)); !errors.Is(err, ErrUnknownCookie) {
		t.Fatalf("forged cookie want %v, got %v", ErrUnknownCookie, err)
	}
}

func TestAEADKeySizeValidation(t *testing.T) {
	if _, err := NewAEAD(AEADAESGCM128, make([]byte, 15)); !errors.Is(err, ErrInvalidKeySize) {
		t.Fatalf("bad key size want %v, got %v", ErrInvalidKeySize, err)
	}
	if _, err := NewAEAD(0x9999, make([]byte, 16)); !errors.Is(err, ErrUnsupportedAEAD) {
		t.Fatalf("unknown aead want %v, got %v", ErrUnsupportedAEAD, err)
	}
}
