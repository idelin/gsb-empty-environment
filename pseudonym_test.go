package netsec

import (
	"context"
	"strings"
	"testing"
)

const (
	testGenV1 uint32 = 1
	testGenV2 uint32 = 2
)

func newTestPseudonymizer(t *testing.T) *Pseudonymizer {
	t.Helper()
	keyring := NewKeyring()
	if err := keyring.Add(testGenV1, bytesOf("0123456789abcdef0123456789abcdef")); err != nil {
		t.Fatalf("add gen1 key: %v", err)
	}
	if err := keyring.Add(testGenV2, bytesOf("fedcba9876543210fedcba9876543210")); err != nil {
		t.Fatalf("add gen2 key: %v", err)
	}
	return NewPseudonymizer(keyring)
}

func bytesOf(s string) []byte {
	return []byte(s)
}

func TestPseudonymIsStableAndDomainSeparated(t *testing.T) {
	p := newTestPseudonymizer(t)
	const sharedEmail = "alice@example.com"

	tokenA1, err := p.Pseudonymize(testGenV1, "tenant-a", "email", bytesOf(sharedEmail))
	if err != nil {
		t.Fatalf("pseudonymize tenant-a: %v", err)
	}
	tokenA1Again, err := p.Pseudonymize(testGenV1, "tenant-a", "email", bytesOf(sharedEmail))
	if err != nil {
		t.Fatalf("pseudonymize tenant-a again: %v", err)
	}
	if !tokenA1.Equal(tokenA1Again) || tokenA1.String() != tokenA1Again.String() {
		t.Fatal("same (tenant, field, secret, generation) must produce a stable token")
	}

	tokenB, err := p.Pseudonymize(testGenV1, "tenant-b", "email", bytesOf(sharedEmail))
	if err != nil {
		t.Fatalf("pseudonymize tenant-b: %v", err)
	}
	tokenOtherField, err := p.Pseudonymize(testGenV1, "tenant-a", "user_id", bytesOf(sharedEmail))
	if err != nil {
		t.Fatalf("pseudonymize other field: %v", err)
	}
	if tokenA1.Equal(tokenB) {
		t.Fatal("same email across tenants must not correlate to one token")
	}
	if tokenA1.Equal(tokenOtherField) {
		t.Fatal("same secret across field types must not correlate to one token")
	}

	tokenA2, err := p.Pseudonymize(testGenV2, "tenant-a", "email", bytesOf(sharedEmail))
	if err != nil {
		t.Fatalf("pseudonymize gen2: %v", err)
	}
	if tokenA1.Equal(tokenA2) || tokenA2.Generation != testGenV2 {
		t.Fatal("token generation must be bound to the key generation")
	}

	if strings.Contains(tokenA1.String(), sharedEmail) {
		t.Fatal("token must not embed the raw secret")
	}
}

func TestTokenRoundTripAndTamperDetection(t *testing.T) {
	p := newTestPseudonymizer(t)
	token, err := p.Pseudonymize(testGenV1, "tenant-a", "email", bytesOf("alice@example.com"))
	if err != nil {
		t.Fatalf("pseudonymize: %v", err)
	}
	parsed, err := ParseToken(token.String())
	if err != nil {
		t.Fatalf("parse token: %v", err)
	}
	if !parsed.Equal(token) || parsed.Generation != testGenV1 {
		t.Fatal("parsed token must round-trip identically")
	}

	ok, err := p.Verify(token, "tenant-a", "email", bytesOf("alice@example.com"))
	if err != nil || !ok {
		t.Fatalf("verify original secret: ok=%v err=%v", ok, err)
	}
	ok, err = p.Verify(token, "tenant-a", "email", bytesOf("mallory@example.com"))
	if err != nil || ok {
		t.Fatalf("verify wrong secret must fail closed: ok=%v err=%v", ok, err)
	}

	for _, malformed := range []string{"", "pn1", "pn1.###", "pn1.AAAA", "other.x"} {
		if _, err := ParseToken(malformed); err == nil {
			t.Fatalf("malformed token %q accepted", malformed)
		}
	}
}

func TestPseudonymizeRejectsDegenerateDomains(t *testing.T) {
	p := newTestPseudonymizer(t)
	cases := []struct {
		name      string
		tenant    string
		fieldType string
		secret    []byte
		want      error
	}{
		{"empty tenant", "", "email", bytesOf("a@b.c"), ErrEmptyTenant},
		{"empty field", "tenant-a", "", bytesOf("a@b.c"), ErrEmptyFieldType},
		{"empty secret", "tenant-a", "email", nil, ErrEmptySecret},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := p.Pseudonymize(testGenV1, tc.tenant, tc.fieldType, tc.secret); err != tc.want {
				t.Fatalf("want %v, got %v", tc.want, err)
			}
		})
	}

	keyring := NewKeyring()
	if err := keyring.Add(testGenV1, bytesOf("short")); err != ErrWeakKey {
		t.Fatalf("weak key rejection: want %v, got %v", ErrWeakKey, err)
	}
	if _, err := p.Pseudonymize(99, "tenant-a", "email", bytesOf("a@b.c")); err != ErrUnknownGeneration {
		t.Fatalf("unknown generation: want %v, got %v", ErrUnknownGeneration, err)
	}
}

func TestTenantContextPropagation(t *testing.T) {
	p := newTestPseudonymizer(t)
	ctx := WithTenant(context.Background(), "tenant-a")
	fromCtx, err := p.PseudonymizeFromContext(ctx, testGenV1, "email", bytesOf("alice@example.com"))
	if err != nil {
		t.Fatalf("pseudonymize from context: %v", err)
	}
	explicit, err := p.Pseudonymize(testGenV1, "tenant-a", "email", bytesOf("alice@example.com"))
	if err != nil {
		t.Fatalf("pseudonymize explicit: %v", err)
	}
	if !fromCtx.Equal(explicit) {
		t.Fatal("context-derived token must match explicit tenant token")
	}
	if got := TenantFromContext(ctx); got != "tenant-a" {
		t.Fatalf("tenant round-trip: got %q", got)
	}
	if _, err := p.PseudonymizeFromContext(context.Background(), testGenV1, "email", bytesOf("alice@example.com")); err != ErrEmptyTenant {
		t.Fatalf("missing tenant must fail: want %v, got %v", ErrEmptyTenant, err)
	}
}
