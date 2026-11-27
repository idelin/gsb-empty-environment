package pinuv

import (
	"bytes"
	"crypto/ecdh"
	"errors"
	"testing"
	"time"
)

const testPIN = "837150"

// newAuth returns an authenticator with the PIN set and a live key
// agreement, plus its store.
func newAuth(t *testing.T) (*Authenticator, *MemStore, []byte) {
	t.Helper()
	store := NewMemStore()
	a, err := NewAuthenticator(store, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.SetPIN(testPIN); err != nil {
		t.Fatal(err)
	}
	pub, err := a.GetKeyAgreement()
	if err != nil {
		t.Fatal(err)
	}
	return a, store, pub
}

// platformProof plays the platform side of getPinUvAuthTokenFromPin:
// ECDH against the authenticator's public key, then
// pinHashEnc = encrypt(LEFT(SHA-256(pin),16)),
// pinUvAuthParam = authenticate(pinHashEnc).
func platformProof(t *testing.T, authPub []byte, pin string) (peerPub, pinHashEnc, param []byte) {
	t.Helper()
	peerPriv, peerPub, err := GenerateKeyAgreement()
	if err != nil {
		t.Fatal(err)
	}
	authKey, err := ecdh.P256().NewPublicKey(authPub)
	if err != nil {
		t.Fatal(err)
	}
	secret, err := peerPriv.ECDH(authKey)
	if err != nil {
		t.Fatal(err)
	}
	proto, err := NewProtocolV2(secret)
	if err != nil {
		t.Fatal(err)
	}
	pinHashEnc, err = proto.Encrypt(PINHash16(pin))
	if err != nil {
		t.Fatal(err)
	}
	return peerPub, pinHashEnc, proto.Authenticate(pinHashEnc)
}

func failOnce(t *testing.T, a *Authenticator, authPub []byte) error {
	t.Helper()
	peerPub, enc, param := platformProof(t, authPub, "000000")
	_, err := a.GetPinUvToken(peerPub, enc, param, PermMakeCredential, "rp.example")
	return err
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	secret := bytes.Repeat([]byte{0x42}, KeyLength)
	p, err := NewProtocolV2(secret)
	if err != nil {
		t.Fatal(err)
	}
	msg := []byte("attestation-object-bytes")
	ct, err := p.Encrypt(msg)
	if err != nil {
		t.Fatal(err)
	}
	if len(ct) != IVLength+32 { // 24-byte payload pads to 32
		t.Fatalf("ciphertext length = %d", len(ct))
	}
	pt, err := p.Decrypt(ct)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(pt, msg) {
		t.Fatal("round trip mismatch")
	}
	// Random IV: same plaintext encrypts differently each time.
	ct2, _ := p.Encrypt(msg)
	if bytes.Equal(ct, ct2) {
		t.Fatal("IV reuse detected")
	}
}

func TestKeySeparationHMACvsAES(t *testing.T) {
	secret := bytes.Repeat([]byte{0x07}, KeyLength)
	p, err := NewProtocolV2(secret)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(p.hmacKey[:], p.aesKey[:]) {
		t.Fatal("hmacKey and aesKey must be independent HKDF outputs")
	}
	// A ciphertext produced under aesKey must not be decryptable by a
	// confused implementation that (Protocol-One-style) uses the HMAC
	// key material for AES.
	ct, err := p.Encrypt([]byte("0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	confused := &Protocol{rand: p.rand}
	copy(confused.aesKey[:], p.hmacKey[:]) // wrong key for AES
	if _, err := confused.Decrypt(ct); err == nil {
		t.Fatal("decryption under hmacKey must fail (key separation violated)")
	}
	// And the AES key must not authenticate.
	tag := hmacSHA256(p.aesKey[:], []byte("msg"))
	if p.Verify([]byte("msg"), tag) {
		t.Fatal("tag computed under aesKey must not verify")
	}
}

func TestProtocolDowngradeConfusion(t *testing.T) {
	secret := bytes.Repeat([]byte{0x11}, KeyLength)
	p, err := NewProtocolV2(secret)
	if err != nil {
		t.Fatal(err)
	}
	msg := []byte("clientDataHash")
	full := p.Authenticate(msg)
	if len(full) != AuthLength {
		t.Fatalf("Protocol Two tag must be 32 bytes, got %d", len(full))
	}
	// Protocol One style 16-byte truncated tag must be rejected.
	if p.Verify(msg, full[:16]) {
		t.Fatal("truncated 16-byte (Protocol One) tag accepted: downgrade confusion")
	}
	// Trailing-garbage 33-byte tag must be rejected.
	if p.Verify(msg, append(full, 0x00)) {
		t.Fatal("oversized tag accepted")
	}
	// Keys derived with swapped/wrong info strings must not interoperate.
	wrong := &Protocol{rand: p.rand}
	copy(wrong.hmacKey[:], hkdfSHA256(secret, []byte(infoAESKey), KeyLength))
	copy(wrong.aesKey[:], hkdfSHA256(secret, []byte(infoHMACKey), KeyLength))
	if wrong.Verify(msg, full) {
		t.Fatal("tag verified under swapped-info keys")
	}
	ct, _ := p.Encrypt([]byte("0123456789abcdef"))
	if _, err := wrong.Decrypt(ct); err == nil {
		t.Fatal("ciphertext decrypted under swapped-info keys")
	}
}

func TestRetryDecrementPersistedBeforeCompare(t *testing.T) {
	a, store, pub := newAuth(t)

	// Each wrong attempt must be visible in persistent storage even if
	// the device "loses power" immediately after the failure.
	for i := 1; i <= MaxConsecutivePINFailures; i++ {
		if err := failOnce(t, a, pub); !errors.Is(err, ErrPINInvalid) {
			t.Fatalf("attempt %d: %v", i, err)
		}
		persisted, _ := store.Load()
		if persisted.Retries != MaxPINRetries-i {
			t.Fatalf("after %d failures persisted retries = %d, want %d",
				i, persisted.Retries, MaxPINRetries-i)
		}
	}
	// 3 consecutive failures in one power cycle: further attempts are
	// refused WITHOUT consuming retries until reboot.
	if err := failOnce(t, a, pub); !errors.Is(err, ErrPINAuthBlocked) {
		t.Fatalf("want ErrPINAuthBlocked, got %v", err)
	}
	if a.Retries() != MaxPINRetries-3 {
		t.Fatalf("blocked attempt consumed a retry: retries = %d", a.Retries())
	}

	// Power cut: reboot clears only the consecutive counter.
	a.Reboot()
	pub, _ = a.GetKeyAgreement()
	if a.Retries() != MaxPINRetries-3 {
		t.Fatalf("reboot must not restore retries, got %d", a.Retries())
	}

	// Burn the remaining 5 retries across further power cycles.
	for a.Retries() > 0 {
		if err := failOnce(t, a, pub); err != nil && !errors.Is(err, ErrPINInvalid) {
			if errors.Is(err, ErrPINAuthBlocked) {
				a.Reboot()
				pub, _ = a.GetKeyAgreement()
				continue
			}
			t.Fatal(err)
		}
		a.Reboot()
		pub, _ = a.GetKeyAgreement()
	}
	if err := failOnce(t, a, pub); !errors.Is(err, ErrPINBlocked) {
		t.Fatalf("want ErrPINBlocked at zero retries, got %v", err)
	}
	// Even the correct PIN is refused once hard-blocked.
	peerPub, enc, param := platformProof(t, pub, testPIN)
	if _, err := a.GetPinUvToken(peerPub, enc, param, PermMakeCredential, "rp.example"); !errors.Is(err, ErrPINBlocked) {
		t.Fatalf("correct PIN must stay blocked, got %v", err)
	}
	// Only a hardware reset recovers.
	if err := a.HardReset(); err != nil {
		t.Fatal(err)
	}
	if a.Retries() != MaxPINRetries {
		t.Fatal("hard reset must restore retries")
	}
}

func TestPowerCutReplayCannotBruteForce(t *testing.T) {
	a, store, pub := newAuth(t)

	// Attacker model: after each failed guess, cut power and reconnect
	// (fresh Authenticator over the same flash). Because the decrement
	// is persisted before the comparison, the replayed session always
	// sees the consumed attempt.
	for guess := 0; guess < MaxPINRetries; guess++ {
		// Fresh device view after "power cycle".
		a, err := NewAuthenticator(store, 0)
		if err != nil {
			t.Fatal(err)
		}
		pub, err = a.GetKeyAgreement()
		if err != nil {
			t.Fatal(err)
		}
		if err := failOnce(t, a, pub); !errors.Is(err, ErrPINInvalid) {
			t.Fatalf("guess %d: %v", guess, err)
		}
		persisted, _ := store.Load()
		if persisted.Retries != MaxPINRetries-(guess+1) {
			t.Fatalf("power-cut replay gained a free attempt: retries = %d", persisted.Retries)
		}
	}
	// All 8 attempts consumed; device is hard-blocked despite never
	// having seen 3 consecutive failures within a single power cycle.
	a, _ = NewAuthenticator(store, 0)
	pub, _ = a.GetKeyAgreement()
	peerPub, enc, param := platformProof(t, pub, testPIN)
	if _, err := a.GetPinUvToken(peerPub, enc, param, PermMakeCredential, "rp.example"); !errors.Is(err, ErrPINBlocked) {
		t.Fatalf("want ErrPINBlocked after replayed power cycles, got %v", err)
	}
}

func TestSuccessRestoresRetriesAndIssuesFreshToken(t *testing.T) {
	a, _, pub := newAuth(t)
	if err := failOnce(t, a, pub); !errors.Is(err, ErrPINInvalid) {
		t.Fatal(err)
	}
	if a.Retries() != MaxPINRetries-1 {
		t.Fatal("retry not consumed")
	}
	peerPub, enc, param := platformProof(t, pub, testPIN)
	tok1, err := a.GetPinUvToken(peerPub, enc, param, PermMakeCredential|PermGetAssertion, "rp.example")
	if err != nil {
		t.Fatal(err)
	}
	if a.Retries() != MaxPINRetries {
		t.Fatal("success must restore full retry budget")
	}
	if len(tok1.Value) != TokenLength {
		t.Fatal("token must be 32 bytes")
	}
	// A second successful issuance replaces the token with a fresh one.
	peerPub, enc, param = platformProof(t, pub, testPIN)
	tok2, err := a.GetPinUvToken(peerPub, enc, param, PermMakeCredential, "rp.example")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(tok1.Value, tok2.Value) {
		t.Fatal("tokens must be freshly random")
	}
	if err := a.Consume(tok1.Value, PermMakeCredential, "rp.example"); !errors.Is(err, ErrTokenInvalid) {
		t.Fatal("superseded token must be invalid")
	}
}

func TestCrossRpTokenRejected(t *testing.T) {
	a, _, pub := newAuth(t)
	peerPub, enc, param := platformProof(t, pub, testPIN)
	tok, err := a.GetPinUvToken(peerPub, enc, param, PermMakeCredential|PermGetAssertion, "bank.example")
	if err != nil {
		t.Fatal(err)
	}
	// Same token presented to a different RP must fail exact-match.
	if err := a.Consume(tok.Value, PermGetAssertion, "evil.example"); !errors.Is(err, ErrRpIDMismatch) {
		t.Fatalf("cross-RP use must fail, got %v", err)
	}
	// Substring/suffix tricks must fail too.
	if err := a.Consume(tok.Value, PermGetAssertion, "bank.example.evil.example"); !errors.Is(err, ErrRpIDMismatch) {
		t.Fatalf("rpId suffix confusion must fail, got %v", err)
	}
	if err := a.Consume(tok.Value, PermMakeCredential, "bank.example"); err != nil {
		t.Fatalf("bound RP must succeed, got %v", err)
	}
}

func TestLowPermissionCannotEscalateToCM(t *testing.T) {
	a, _, pub := newAuth(t)
	peerPub, enc, param := platformProof(t, pub, testPIN)
	// Token issued for mc|ga only, no cm.
	tok, err := a.GetPinUvToken(peerPub, enc, param, PermMakeCredential|PermGetAssertion, "rp.example")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Consume(tok.Value, PermCredentialMgmt, ""); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("mc/ga token used for credential management, got %v", err)
	}
	if err := a.Consume(tok.Value, PermBioEnrollment, ""); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("mc/ga token used for bio enrollment, got %v", err)
	}
	if err := a.Consume(tok.Value, PermLargeBlobWrite, ""); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("mc/ga token used for largeBlobWrite, got %v", err)
	}
	// Combined requirement must also be contained.
	if err := a.Consume(tok.Value, PermMakeCredential|PermCredentialMgmt, "rp.example"); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("partial permission overlap must fail, got %v", err)
	}
	if err := a.Consume(tok.Value, PermGetAssertion, "rp.example"); err != nil {
		t.Fatalf("in-scope use must succeed, got %v", err)
	}
}

func TestTokenInvalidatedByKeyRotationAndExpiry(t *testing.T) {
	a, _, pub := newAuth(t)
	peerPub, enc, param := platformProof(t, pub, testPIN)
	tok, err := a.GetPinUvToken(peerPub, enc, param, PermMakeCredential, "rp.example")
	if err != nil {
		t.Fatal(err)
	}
	// getKeyAgreement rotates keys and must void the old token.
	if _, err := a.GetKeyAgreement(); err != nil {
		t.Fatal(err)
	}
	if err := a.Consume(tok.Value, PermMakeCredential, "rp.example"); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("token survived key rotation, got %v", err)
	}

	// Expiry: advance the authenticator clock past the TTL.
	pub, _ = a.GetKeyAgreement()
	peerPub, enc, param = platformProof(t, pub, testPIN)
	tok2, err := a.GetPinUvToken(peerPub, enc, param, PermMakeCredential, "rp.example")
	if err != nil {
		t.Fatal(err)
	}
	base := a.now()
	a.now = func() time.Time { return base.Add(DefaultTokenTTL + time.Second) }
	if err := a.Consume(tok2.Value, PermMakeCredential, "rp.example"); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("expired token must fail, got %v", err)
	}
}

func TestForgedAndTruncatedTokensRejected(t *testing.T) {
	a, _, pub := newAuth(t)
	peerPub, enc, param := platformProof(t, pub, testPIN)
	tok, err := a.GetPinUvToken(peerPub, enc, param, PermMakeCredential, "rp.example")
	if err != nil {
		t.Fatal(err)
	}
	forged := append([]byte(nil), tok.Value...)
	forged[0] ^= 0x01
	if err := a.Consume(forged, PermMakeCredential, "rp.example"); !errors.Is(err, ErrTokenInvalid) {
		t.Fatal("forged token accepted")
	}
	if err := a.Consume(tok.Value[:16], PermMakeCredential, "rp.example"); !errors.Is(err, ErrTokenInvalid) {
		t.Fatal("truncated token accepted")
	}
}
