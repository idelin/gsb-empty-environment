package netsec

import (
	"errors"
	"testing"
)

// localHost is a full-featured local device: keyboard+display, MITM and SC
// requested, 16-octet key.
var localHost = Features{
	IOCapability: IOKeyboardDisplay,
	AuthReq:      AuthReqBonding | AuthReqMITM | AuthReqSC,
	MaxKeySize:   16,
}

func pairingErrCode(t *testing.T, err error) uint8 {
	t.Helper()
	var pe *PairingError
	if !errors.As(err, &pe) {
		t.Fatalf("expected *PairingError, got %T: %v", err, err)
	}
	return pe.Code
}

// Honest peer, both sides SC-capable with Yes/No: Numeric Comparison,
// security level 4.
func TestNumericComparisonHappyPath(t *testing.T) {
	res, err := SelectMethod(localHost, localHost, Policy{RequiredLevel: SecuritySecureConnections})
	if err != nil {
		t.Fatalf("unexpected rejection: %v", err)
	}
	if res.Method != MethodNumericComparison {
		t.Errorf("method = %v, want Numeric Comparison", res.Method)
	}
	if res.Level != SecuritySecureConnections {
		t.Errorf("level = %v, want level 4", res.Level)
	}
	if !res.SecureConnections {
		t.Error("expected SecureConnections")
	}
	if res.KeySize != 16 {
		t.Errorf("key size = %d, want 16", res.KeySize)
	}
}

// Attack 1: the peer claims NoInputNoOutput, forcing Just Works. The
// derived level 2 must not satisfy a policy requiring authentication.
func TestIOCapabilityDowngradeJustWorks(t *testing.T) {
	attacker := Features{
		IOCapability: IONoInputNoOutput,
		AuthReq:      AuthReqBonding | AuthReqMITM | AuthReqSC,
		MaxKeySize:   16,
	}

	res, err := SelectMethod(localHost, attacker, Policy{RequiredLevel: SecurityUnauthenticated})
	if err != nil {
		t.Fatalf("unexpected rejection: %v", err)
	}
	if res.Method != MethodJustWorks {
		t.Errorf("method = %v, want Just Works", res.Method)
	}
	if res.Level != SecurityUnauthenticated {
		t.Errorf("level = %v, want level 2", res.Level)
	}

	_, err = SelectMethod(localHost, attacker, Policy{RequiredLevel: SecurityAuthenticated})
	if code := pairingErrCode(t, err); code != ErrCodeAuthRequirements {
		t.Errorf("error code = 0x%02x, want 0x%02x (Authentication Requirements)", code, ErrCodeAuthRequirements)
	}
}

// Attack 2: the peer clears the SC bit, forcing legacy pairing. Even with
// full IO capabilities the best achievable level is 3, so a level 4
// requirement must be rejected.
func TestSCBitClearedForcesLegacy(t *testing.T) {
	attacker := Features{
		IOCapability: IOKeyboardDisplay,
		AuthReq:      AuthReqBonding | AuthReqMITM, // SC bit cleared
		MaxKeySize:   16,
	}

	res, err := SelectMethod(localHost, attacker, Policy{RequiredLevel: SecurityAuthenticated})
	if err != nil {
		t.Fatalf("unexpected rejection: %v", err)
	}
	if res.SecureConnections {
		t.Error("legacy pairing must not report SecureConnections")
	}
	if res.Method == MethodNumericComparison {
		t.Error("legacy pairing must not use Numeric Comparison")
	}
	if res.Level != SecurityAuthenticated {
		t.Errorf("level = %v, want level 3", res.Level)
	}

	_, err = SelectMethod(localHost, attacker, Policy{RequiredLevel: SecuritySecureConnections})
	if code := pairingErrCode(t, err); code != ErrCodeAuthRequirements {
		t.Errorf("error code = 0x%02x, want 0x%02x (Authentication Requirements)", code, ErrCodeAuthRequirements)
	}
}

// Attack 3: the peer offers a key shorter than the policy minimum.
func TestShortKeyRejected(t *testing.T) {
	shortKeyPeer := localHost
	shortKeyPeer.MaxKeySize = 4

	_, err := SelectMethod(localHost, shortKeyPeer, Policy{RequiredLevel: SecuritySecureConnections})
	if code := pairingErrCode(t, err); code != ErrCodeEncryptionKeySize {
		t.Errorf("error code = 0x%02x, want 0x%02x (Encryption Key Size)", code, ErrCodeEncryptionKeySize)
	}

	// A stricter local minimum rejects a spec-legal 7-octet key too.
	_, err = SelectMethod(localHost, Features{
		IOCapability: IOKeyboardDisplay,
		AuthReq:      AuthReqBonding | AuthReqMITM | AuthReqSC,
		MaxKeySize:   7,
	}, Policy{RequiredLevel: SecuritySecureConnections, MinKeySize: 16})
	if code := pairingErrCode(t, err); code != ErrCodeEncryptionKeySize {
		t.Errorf("error code = 0x%02x, want 0x%02x (Encryption Key Size)", code, ErrCodeEncryptionKeySize)
	}
}

// Without a MITM request from either side the pairing degenerates to
// Just Works even between honest, IO-rich peers.
func TestNoMITMRequestDegeneratesToJustWorks(t *testing.T) {
	noMITM := Features{
		IOCapability: IOKeyboardDisplay,
		AuthReq:      AuthReqBonding | AuthReqSC,
		MaxKeySize:   16,
	}
	res, err := SelectMethod(noMITM, noMITM, Policy{})
	if err != nil {
		t.Fatalf("unexpected rejection: %v", err)
	}
	if res.Method != MethodJustWorks {
		t.Errorf("method = %v, want Just Works", res.Method)
	}
}

// OOB data on either side takes precedence over the IO capability matrix.
func TestOOBTakesPrecedence(t *testing.T) {
	oobPeer := localHost
	oobPeer.OOB = true
	res, err := SelectMethod(localHost, oobPeer, Policy{RequiredLevel: SecuritySecureConnections})
	if err != nil {
		t.Fatalf("unexpected rejection: %v", err)
	}
	if res.Method != MethodOutOfBand {
		t.Errorf("method = %v, want Out Of Band", res.Method)
	}
	if res.Level != SecuritySecureConnections {
		t.Errorf("level = %v, want level 4", res.Level)
	}
}

// Legacy Passkey Entry still yields authenticated (level 3) security.
func TestLegacyPasskeyEntry(t *testing.T) {
	legacy := Features{
		IOCapability: IOKeyboardOnly,
		AuthReq:      AuthReqBonding | AuthReqMITM,
		MaxKeySize:   16,
	}
	display := Features{
		IOCapability: IODisplayOnly,
		AuthReq:      AuthReqBonding | AuthReqMITM,
		MaxKeySize:   16,
	}
	res, err := SelectMethod(display, legacy, Policy{RequiredLevel: SecurityAuthenticated})
	if err != nil {
		t.Fatalf("unexpected rejection: %v", err)
	}
	if res.Method != MethodPasskeyEntry {
		t.Errorf("method = %v, want Passkey Entry", res.Method)
	}
	if res.Level != SecurityAuthenticated {
		t.Errorf("level = %v, want level 3", res.Level)
	}
}
