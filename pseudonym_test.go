package netsec

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func testKeyring(t *testing.T) *Keyring {
	t.Helper()
	keys := map[Generation][]byte{
		1: bytes.Repeat([]byte{0x11}, MinKeySize),
		2: bytes.Repeat([]byte{0x22}, MinKeySize),
		3: bytes.Repeat([]byte{0x33}, MinKeySize),
	}
	ring, err := NewKeyring(2, keys)
	if err != nil {
		t.Fatalf("NewKeyring: %v", err)
	}
	return ring
}

func testPseudonymizer(t *testing.T) *Pseudonymizer {
	t.Helper()
	p, err := NewPseudonymizer(testKeyring(t))
	if err != nil {
		t.Fatalf("NewPseudonymizer: %v", err)
	}
	return p
}

func mustToken(t *testing.T, p *Pseudonymizer, tenant TenantID, field FieldType, value string, gen Generation) Token {
	t.Helper()
	token, err := p.Pseudonymize(tenant, field, value, gen)
	if err != nil {
		t.Fatalf("Pseudonymize(%q, %q, generation %s): %v", tenant, field, gen, err)
	}
	return token
}

func TestPseudonymStableScopedAndOpaque(t *testing.T) {
	const email = "alice@example.com"

	first := testPseudonymizer(t)
	second := testPseudonymizer(t)

	a1 := mustToken(t, first, "tenant-a", "email", email, 1)
	a1Again := mustToken(t, second, "tenant-a", "email", email, 1)
	if a1 != a1Again {
		t.Fatalf("same tenant/field/value/generation must correlate: %q != %q", a1, a1Again)
	}

	a2 := mustToken(t, first, "tenant-b", "email", email, 1)
	if a1 == a2 {
		t.Fatalf("same raw email leaked across tenants: %q", a1)
	}

	phone := mustToken(t, first, "tenant-a", "phone", email, 1)
	if a1 == phone {
		t.Fatalf("field type was not bound into the pseudonym: %q", a1)
	}

	a1NextGeneration := mustToken(t, first, "tenant-a", "email", email, 2)
	if a1 == a1NextGeneration {
		t.Fatalf("key generation was not bound into the pseudonym: %q", a1)
	}

	gen, ok := a1.Generation()
	if !ok || gen != 1 {
		t.Fatalf("token generation = %s, %v; want 1, true", gen, ok)
	}
	if !a1.Valid() {
		t.Fatalf("token %q is not valid", a1)
	}
	for _, secret := range []string{"alice", "example.com", "tenant-a"} {
		if strings.Contains(string(a1), secret) {
			t.Fatalf("token %q discloses %q", a1, secret)
		}
	}
}

func TestDomainEncodingIsUnambiguous(t *testing.T) {
	p := testPseudonymizer(t)

	left := mustToken(t, p, "a", "bc", "same-value", 1)
	right := mustToken(t, p, "ab", "c", "same-value", 1)
	if left == right {
		t.Fatal("tenant/field concatenation produced the same pseudonym")
	}

	nulField := mustToken(t, p, "a", FieldType("email\x00"), "same-value", 1)
	plainField := mustToken(t, p, "a", "email", "same-value", 1)
	if nulField == plainField {
		t.Fatal("delimiter inside a field changed the domain binding")
	}
}

func TestKeyringAndInputValidation(t *testing.T) {
	if _, err := NewKeyring(1, nil); err == nil {
		t.Fatal("NewKeyring accepted an empty keyring")
	}
	if _, err := NewKeyring(1, map[Generation][]byte{1: []byte("short")}); !errors.Is(err, ErrShortKey) {
		t.Fatalf("short key error = %v, want ErrShortKey", err)
	}
	if _, err := NewKeyring(2, map[Generation][]byte{1: bytes.Repeat([]byte{1}, MinKeySize)}); !errors.Is(err, ErrUnknownGeneration) {
		t.Fatalf("missing current generation error = %v, want ErrUnknownGeneration", err)
	}
	if _, err := NewPseudonymizer(nil); !errors.Is(err, ErrNilKeyring) {
		t.Fatalf("nil keyring error = %v, want ErrNilKeyring", err)
	}

	key := bytes.Repeat([]byte{0x44}, MinKeySize)
	ring, err := NewKeyring(1, map[Generation][]byte{1: key})
	if err != nil {
		t.Fatalf("NewKeyring: %v", err)
	}
	p, err := NewPseudonymizer(ring)
	if err != nil {
		t.Fatalf("NewPseudonymizer: %v", err)
	}
	before := mustToken(t, p, "tenant", "email", "value@example.com", 1)
	for i := range key {
		key[i] = 0
	}
	after := mustToken(t, p, "tenant", "email", "value@example.com", 1)
	if before != after {
		t.Fatal("mutating the caller's key slice changed pseudonymization")
	}

	if _, err := p.Pseudonymize("", "email", "value", 1); !errors.Is(err, ErrEmptyTenant) {
		t.Fatalf("empty tenant error = %v, want ErrEmptyTenant", err)
	}
	if _, err := p.Pseudonymize("tenant", "", "value", 1); !errors.Is(err, ErrEmptyFieldType) {
		t.Fatalf("empty field error = %v, want ErrEmptyFieldType", err)
	}
	if _, err := p.Pseudonymize("tenant", "email", "", 1); !errors.Is(err, ErrEmptyValue) {
		t.Fatalf("empty value error = %v, want ErrEmptyValue", err)
	}
	if _, err := p.Pseudonymize("tenant", "email", "value", 99); !errors.Is(err, ErrUnknownGeneration) {
		t.Fatalf("unknown generation error = %v, want ErrUnknownGeneration", err)
	}

	current := mustToken(t, p, "tenant", "email", "value@example.com", 1)
	convenience, err := p.PseudonymizeCurrent("tenant", "email", "value@example.com")
	if err != nil {
		t.Fatalf("PseudonymizeCurrent: %v", err)
	}
	if convenience != current {
		t.Fatalf("PseudonymizeCurrent = %q, want %q", convenience, current)
	}
}

func TestParseTokenRejectsMalformedText(t *testing.T) {
	p := testPseudonymizer(t)
	valid := string(mustToken(t, p, "tenant", "email", "value", 1))

	bad := []string{
		"",
		"p1",
		"p1.1",
		"v1.1." + strings.Repeat("A", 43),
		"p1.01." + strings.Split(valid, ".")[2],
		"p1.1.not-base64!",
		"p1.1." + strings.Repeat("A", 10),
	}
	for _, token := range bad {
		if _, _, err := ParseToken(token); !errors.Is(err, ErrInvalidToken) {
			t.Fatalf("ParseToken(%q) error = %v, want ErrInvalidToken", token, err)
		}
	}
}
