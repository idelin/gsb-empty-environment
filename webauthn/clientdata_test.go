package webauthn

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
)

func TestClientData_Contract(t *testing.T) {
	f := newRegistrationFixture(t)

	t.Run("wrong type", func(t *testing.T) {
		raw := buildClientDataCustom(t, "webauthn.get", base64.RawURLEncoding.EncodeToString(f.challenge), f.origin, false)
		_, err := VerifyRegistration(f.buildPackedSelfObject(t), raw, f.challenge, f.policy)
		if !errors.Is(err, ErrClientData) {
			t.Fatalf("expected ErrClientData, got %v", err)
		}
	})

	t.Run("challenge mismatch", func(t *testing.T) {
		raw := buildClientDataCustom(t, expectedClientDataType, base64.RawURLEncoding.EncodeToString([]byte("attacker-challenge")), f.origin, false)
		_, err := VerifyRegistration(f.buildPackedSelfObject(t), raw, f.challenge, f.policy)
		if !errors.Is(err, ErrChallenge) {
			t.Fatalf("expected ErrChallenge, got %v", err)
		}
	})

	t.Run("origin mismatch", func(t *testing.T) {
		raw := buildClientDataCustom(t, expectedClientDataType, base64.RawURLEncoding.EncodeToString(f.challenge), "https://evil.example.org", false)
		_, err := VerifyRegistration(f.buildPackedSelfObject(t), raw, f.challenge, f.policy)
		if !errors.Is(err, ErrOrigin) {
			t.Fatalf("expected ErrOrigin, got %v", err)
		}
	})

	t.Run("padded base64url still decodes and matches", func(t *testing.T) {
		padded := base64.URLEncoding.EncodeToString(f.challenge)
		raw := buildClientDataCustom(t, expectedClientDataType, padded, f.origin, false)
		if _, err := VerifyRegistration(f.buildPackedSelfObject(t), raw, f.challenge, f.policy); err != nil {
			t.Fatalf("padded base64url should be accepted: %v", err)
		}
	})

	t.Run("duplicate JSON keys rejected", func(t *testing.T) {
		raw := []byte(`{"type":"webauthn.create","type":"webauthn.create","challenge":"` +
			base64.RawURLEncoding.EncodeToString(f.challenge) + `","origin":"` + f.origin + `"}`)
		_, err := VerifyRegistration(f.buildPackedSelfObject(t), raw, f.challenge, f.policy)
		if !errors.Is(err, ErrClientData) {
			t.Fatalf("expected ErrClientData for duplicate key, got %v", err)
		}
	})
}

func buildClientDataCustom(t *testing.T, typ, challenge, origin string, crossOrigin bool) []byte {
	t.Helper()
	payload := map[string]any{
		"type":      typ,
		"challenge": challenge,
		"origin":    origin,
	}
	if crossOrigin {
		payload["crossOrigin"] = true
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal custom client data: %v", err)
	}
	return raw
}
