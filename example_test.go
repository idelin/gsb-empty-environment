package netsec

import (
	"errors"
	"testing"
	"time"
)

// TestExampleUsage exercises the public API the way an importing service
// would: build trust bundles, construct a validator for the service
// audience, and validate an inbound JWT-SVID.
func TestExampleUsage(t *testing.T) {
	s := newSigner(t, AlgES256)
	bundle, err := NewBundle("payment.example", s.public())
	if err != nil {
		t.Fatal(err)
	}
	source := MapBundleSource{}
	source.Add(bundle)

	const audience = "spiffe://payment.example/checkout"
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	validator, err := NewValidator(source, audience, WithClock(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}

	token := s.token(t, map[string]any{
		"sub": "spiffe://payment.example/workload/ordering",
		"aud": []string{audience},
		"exp": now.Add(time.Hour).Unix(),
		"iat": now.Unix(),
	})
	claims, err := validator.Validate(token)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if got, want := claims.ID.TrustDomain(), "payment.example"; got != want {
		t.Fatalf("trust domain = %q, want %q", got, want)
	}
	if got, want := claims.ID.Path(), "/workload/ordering"; got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}

	// A token minted for another service audience is rejected even though
	// the signature and subject are valid: cross-audience replay defense.
	foreign := s.token(t, map[string]any{
		"sub": "spiffe://payment.example/workload/ordering",
		"aud": []string{"spiffe://payment.example/other-service"},
		"exp": now.Add(time.Hour).Unix(),
		"iat": now.Unix(),
	})
	if _, err := validator.Validate(foreign); !errors.Is(err, ErrAudienceMismatch) {
		t.Fatalf("cross-audience replay: err = %v, want ErrAudienceMismatch", err)
	}
}
