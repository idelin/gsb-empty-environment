package ctap2auth

import (
	"errors"
	"testing"
)

// End-to-end: platform and authenticator run a Protocol Two ClientPIN flow,
// obtain an RP-bound token, and use it to gate a level-3 discoverable credential.
func TestIntegration_ClientPINAndCredProtect(t *testing.T) {
	platform, err := NewAuthSession()
	if err != nil {
		t.Fatal(err)
	}
	authenticator := NewPINManager()
	if err := authenticator.SetPin("1234"); err != nil {
		t.Fatal(err)
	}

	// Authenticator side holds its own ephemeral key and derives identical keys.
	authSession, err := NewAuthSession()
	if err != nil {
		t.Fatal(err)
	}
	if err := authSession.Establish(platform.PlatformKeyAgreement()); err != nil {
		t.Fatal(err)
	}
	if err := platform.Establish(authSession.PlatformKeyAgreement()); err != nil {
		t.Fatal(err)
	}

	// Platform encrypts the PIN hash with a random IV under the AES key.
	pinHashEnc, err := platform.Encrypt(PinHash("1234"))
	if err != nil {
		t.Fatal(err)
	}

	token, err := authenticator.IssuePinUvAuthToken(authSession, PermGetAssertion, "acme.example", pinHashEnc)
	if err != nil {
		t.Fatalf("token issuance: %v", err)
	}

	// Platform computes HMAC over the clientDataHash with the token; host verifies.
	clientDataHash := make([]byte, 32)
	clientDataHash[31] = 0x42
	param, err := platformAuthenticateToken(token.Bytes, clientDataHash)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := authenticator.VerifyPinUvAuthParam(token.Bytes, clientDataHash, param); !ok || err != nil {
		t.Fatalf("token param verify: ok=%v err=%v", ok, err)
	}
	if err := authenticator.AssertPermission(token.Bytes, PermGetAssertion, "acme.example"); err != nil {
		t.Fatalf("permission: %v", err)
	}

	// Level-3 resident key stays hidden until UV completes.
	rk := CredentialInfo{CredentialID: []byte("rk-l3"), RPID: "acme.example", CredProtect: 3}
	noUV := AssertionRequest{RPID: "acme.example", UserVerified: false}
	if err := authenticator.AssertPermission(token.Bytes, PermGetAssertion, "acme.example"); err != nil {
		t.Fatal(err)
	}
	if err := EnsureAssertionAccess(rk, noUV); !errors.Is(err, ErrOperationDenied) {
		t.Fatalf("level 3 without UV must be denied, got %v", err)
	}
	withUV := AssertionRequest{RPID: "acme.example", UserVerified: true, AllowList: [][]byte{rk.CredentialID}}
	if err := EnsureAssertionAccess(rk, withUV); err != nil {
		t.Fatalf("level 3 with UV and token must pass, got %v", err)
	}
}

func platformAuthenticateToken(token, message []byte) ([]byte, error) {
	return Authenticate(token, message)
}
