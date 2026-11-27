package ctap2auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"testing"
)

func establish(t *testing.T) (*AuthSession, *AuthSession) {
	t.Helper()
	platform, err := NewAuthSession()
	if err != nil {
		t.Fatal(err)
	}
	authn, err := NewAuthSession()
	if err != nil {
		t.Fatal(err)
	}
	if err := platform.Establish(authn.PlatformKeyAgreement()); err != nil {
		t.Fatalf("platform establish: %v", err)
	}
	if err := authn.Establish(platform.PlatformKeyAgreement()); err != nil {
		t.Fatalf("authn establish: %v", err)
	}
	return platform, authn
}

func encryptedPinHash(t *testing.T, s *AuthSession, pin string) []byte {
	t.Helper()
	blob, err := s.Encrypt(PinHash(pin))
	if err != nil {
		t.Fatalf("encrypt pin hash: %v", err)
	}
	return blob
}

func TestSetPin_PolicyAndDefaults(t *testing.T) {
	m := NewPINManager()
	if m.Retries() != MaxPINRetries {
		t.Fatalf("default retries = %d, want %d", m.Retries(), MaxPINRetries)
	}
	if err := m.SetPin("abc"); !errors.Is(err, ErrPinPolicyViolation) {
		t.Fatalf("3-code-point PIN must violate policy, got %v", err)
	}
	long := make([]rune, maxPINCodePoints+1)
	for i := range long {
		long[i] = 'a'
	}
	if err := m.SetPin(string(long)); !errors.Is(err, ErrPinPolicyViolation) {
		t.Fatalf("64-code-point PIN must violate policy, got %v", err)
	}
	if err := m.SetPin("1234"); err != nil {
		t.Fatalf("valid PIN: %v", err)
	}
	if m.PinBlocked() {
		t.Fatal("freshly set PIN must not be blocked")
	}
}

func TestIssueToken_HappyPath(t *testing.T) {
	m := NewPINManager()
	_ = m.SetPin("1234")
	_, authn := establish(t)

	token, err := m.IssuePinUvAuthToken(authn, PermGetAssertion, "acme.example", encryptedPinHash(t, authn, "1234"))
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	if len(token.Bytes) != tokenSizeBytes {
		t.Fatalf("token length = %d", len(token.Bytes))
	}
	if token.RPID != "acme.example" || token.Permissions != PermGetAssertion {
		t.Fatalf("token binding wrong: %+v", token)
	}
	if err := m.AssertPermission(token.Bytes, PermGetAssertion, "acme.example"); err != nil {
		t.Fatalf("assert own permission: %v", err)
	}
}

func TestPinRetries_PermanentLockAtZero(t *testing.T) {
	m := NewPINManager()
	_ = m.SetPin("1234")
	_, authn := establish(t)

	for attempt := 1; attempt <= MaxPINRetries; attempt++ {
		_, err := m.IssuePinUvAuthToken(authn, PermGetAssertion, "acme.example", encryptedPinHash(t, authn, "0000"))
		if attempt < MaxPINRetries {
			if !errors.Is(err, ErrPinInvalid) {
				t.Fatalf("attempt %d: want ErrPinInvalid, got %v", attempt, err)
			}
			if m.Retries() != MaxPINRetries-attempt {
				t.Fatalf("attempt %d: retries = %d", attempt, m.Retries())
			}
		} else {
			if !errors.Is(err, ErrPinBlocked) {
				t.Fatalf("8th failure must be ErrPinBlocked, got %v", err)
			}
		}
	}
	if !m.PinBlocked() || m.Retries() != 0 {
		t.Fatal("PIN must be permanently blocked with zero retries")
	}

	// A correct PIN can never revive a permanently locked authenticator.
	_, err := m.IssuePinUvAuthToken(authn, PermGetAssertion, "acme.example", encryptedPinHash(t, authn, "1234"))
	if !errors.Is(err, ErrPinBlocked) {
		t.Fatalf("post-lock correct PIN must still fail, got %v", err)
	}
}

func TestAuthFailures_ThreeStrikeBlockAndPowerCycle(t *testing.T) {
	m := NewPINManager()
	_ = m.SetPin("1234")
	_, authn := establish(t)

	// Cryptographically invalid pinHashEnc (all-zero IV) counts as auth failures,
	// not PIN mismatches, so pinRetries must stay intact.
	badEnc := make([]byte, ivSizeBytes*2)
	for attempt := 1; attempt <= MaxAuthFailuresPerPowerUp; attempt++ {
		_, err := m.IssuePinUvAuthToken(authn, PermGetAssertion, "acme.example", badEnc)
		if attempt < MaxAuthFailuresPerPowerUp {
			if !errors.Is(err, ErrPinAuthInvalid) {
				t.Fatalf("attempt %d: want ErrPinAuthInvalid, got %v", attempt, err)
			}
		} else if !errors.Is(err, ErrPinAuthBlocked) {
			t.Fatalf("3rd failure must be ErrPinAuthBlocked, got %v", err)
		}
	}
	if !m.AuthBlocked() {
		t.Fatal("authenticator must be auth-blocked")
	}
	if m.Retries() != MaxPINRetries {
		t.Fatalf("crypto failures must not decrement pinRetries, got %d", m.Retries())
	}

	// While blocked, even valid requests are refused.
	_, err := m.IssuePinUvAuthToken(authn, PermGetAssertion, "acme.example", encryptedPinHash(t, authn, "1234"))
	if !errors.Is(err, ErrPinAuthBlocked) {
		t.Fatalf("blocked authenticator must refuse, got %v", err)
	}

	// Power cycle clears the three-strike block but preserves pinRetries.
	m.PowerCycle()
	if m.AuthBlocked() {
		t.Fatal("power cycle must clear auth block")
	}
	if m.Retries() != MaxPINRetries {
		t.Fatal("pinRetries must survive power cycle")
	}
	token, err := m.IssuePinUvAuthToken(authn, PermGetAssertion, "acme.example", encryptedPinHash(t, authn, "1234"))
	if err != nil {
		t.Fatalf("post-cycle valid PIN must issue token, got %v", err)
	}

	// pinRetries==0 (permanent block) must survive a power cycle.
	blocked := NewPINManager()
	_ = blocked.SetPin("1234")
	_, bAuthn := establish(t)
	for i := 0; i < MaxPINRetries; i++ {
		_, _ = blocked.IssuePinUvAuthToken(bAuthn, PermGetAssertion, "acme.example", encryptedPinHash(t, bAuthn, "0000"))
	}
	blocked.PowerCycle()
	if !blocked.PinBlocked() {
		t.Fatal("permanent PIN block must survive power cycle")
	}

	_ = token
}

func TestVerifyPinUvAuthParam_StateMachine(t *testing.T) {
	m := NewPINManager()
	_ = m.SetPin("1234")
	_, authn := establish(t)
	token, err := m.IssuePinUvAuthToken(authn, PermGetAssertion, "acme.example", encryptedPinHash(t, authn, "1234"))
	if err != nil {
		t.Fatal(err)
	}

	clientDataHash := make([]byte, 32)
	clientDataHash[0] = 0x7e
	mac := hmac.New(sha256.New, token.Bytes)
	mac.Write(clientDataHash)
	goodParam := mac.Sum(nil)

	ok, err := m.VerifyPinUvAuthParam(token.Bytes, clientDataHash, goodParam)
	if !ok || err != nil {
		t.Fatalf("valid param must verify: ok=%v err=%v", ok, err)
	}

	// Protocol One truncated MAC is hard rejected before any state change.
	if _, err := m.VerifyPinUvAuthParam(token.Bytes, clientDataHash, goodParam[:16]); !errors.Is(err, ErrProtocolOneDowngrade) {
		t.Fatalf("16-byte MAC downgrade must be rejected, got %v", err)
	}

	// Three consecutive bad params block the authenticator until power cycle.
	badParam := append([]byte{}, goodParam...)
	badParam[0] ^= 0xff
	for attempt := 1; attempt <= MaxAuthFailuresPerPowerUp; attempt++ {
		_, err := m.VerifyPinUvAuthParam(token.Bytes, clientDataHash, badParam)
		if attempt < MaxAuthFailuresPerPowerUp && !errors.Is(err, ErrPinAuthInvalid) {
			t.Fatalf("attempt %d: want ErrPinAuthInvalid, got %v", attempt, err)
		}
		if attempt == MaxAuthFailuresPerPowerUp && !errors.Is(err, ErrPinAuthBlocked) {
			t.Fatalf("attempt %d: want ErrPinAuthBlocked, got %v", attempt, err)
		}
	}
	if !m.AuthBlocked() {
		t.Fatal("three bad params must set auth-blocked")
	}
	// A correct param cannot clear an already-asserted block by itself.
	if _, err := m.VerifyPinUvAuthParam(token.Bytes, clientDataHash, goodParam); !errors.Is(err, ErrPinAuthBlocked) {
		t.Fatalf("blocked verifier must refuse, got %v", err)
	}
}

func TestToken_PermissionsAndRPBinding(t *testing.T) {
	m := NewPINManager()
	_ = m.SetPin("1234")
	_, authn := establish(t)

	// MC/GA require an RP binding at issuance.
	if _, err := m.IssuePinUvAuthToken(authn, PermGetAssertion, "", encryptedPinHash(t, authn, "1234")); !errors.Is(err, ErrMissingParameter) {
		t.Fatalf("RP-less GA token must be rejected, got %v", err)
	}

	token, err := m.IssuePinUvAuthToken(authn, PermMakeCredential|PermGetAssertion, "acme.example", encryptedPinHash(t, authn, "1234"))
	if err != nil {
		t.Fatal(err)
	}

	if err := m.AssertPermission(token.Bytes, PermGetAssertion, "acme.example"); err != nil {
		t.Fatalf("GA on bound RP: %v", err)
	}
	if err := m.AssertPermission(token.Bytes, PermMakeCredential, "acme.example"); err != nil {
		t.Fatalf("MC on bound RP: %v", err)
	}
	// Cross-site reuse denied.
	if err := m.AssertPermission(token.Bytes, PermGetAssertion, "evil.example"); !errors.Is(err, ErrRPIDMismatch) {
		t.Fatalf("cross-site GA must be denied, got %v", err)
	}
	// Unrequested permission bit denied.
	if err := m.AssertPermission(token.Bytes, PermCredentialMgmt, ""); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("unrequested cm must be denied, got %v", err)
	}

	// Management permission is not RP-scoped.
	mgmt, err := m.IssuePinUvAuthToken(authn, PermCredentialMgmt, "", encryptedPinHash(t, authn, "1234"))
	if err != nil {
		t.Fatal(err)
	}
	if err := m.AssertPermission(mgmt.Bytes, PermCredentialMgmt, ""); err != nil {
		t.Fatalf("cm token: %v", err)
	}

	// Revoked / unknown token.
	m.RevokeToken(token.Bytes)
	if err := m.AssertPermission(token.Bytes, PermGetAssertion, "acme.example"); !errors.Is(err, ErrUnknownToken) {
		t.Fatalf("revoked token must be unknown, got %v", err)
	}
}
