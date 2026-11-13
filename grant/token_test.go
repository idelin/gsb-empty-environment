package grant

import (
	"encoding/base64"
	"errors"
	"testing"
	"time"
)

func TestCanonicalContextIsLengthEncoded(t *testing.T) {
	a := canonicalContext('E', Context{"ab": "c"})
	b := canonicalContext('E', Context{"a": "bc"})
	if string(a) == string(b) {
		t.Fatalf("length-prefixed encoding must distinguish ab/c from a/bc")
	}
	c1 := canonicalContext('E', Context{"z": "1", "a": "2"})
	c2 := canonicalContext('E', Context{"a": "2", "z": "1"})
	if string(c1) != string(c2) {
		t.Fatalf("canonical encoding must be order independent")
	}
}

func TestDigestChangesWithEveryField(t *testing.T) {
	g := &Grant{
		GrantID: "g1", KeyID: "k1", KeyVersion: 1,
		GranteePrincipal: "bob", Operations: []Operation{OpDecrypt},
		Constraints: Context{"env": "prod"},
	}
	base := g.Digest()

	rotated := g.Clone()
	rotated.KeyVersion = 2
	if rotated.Digest() == base {
		t.Fatal("key version rotation must change digest")
	}

	retired := g.Clone()
	retired.Generation = 1
	retired.Status = StatusRetired
	if retired.Digest() == base {
		t.Fatal("retire generation/status must change digest")
	}

	pending := g.Clone()
	pending.Status = StatusPending
	if pending.Digest() == base {
		t.Fatal("pending vs active status must change digest")
	}
}

func TestTokenTamperRejected(t *testing.T) {
	s := newTestStore(t, nil)
	enc := activeToken(t, s, baseCreate())

	raw, err := base64.RawURLEncoding.DecodeString(enc)
	if err != nil {
		t.Fatal(err)
	}
	raw[len(raw)-1] ^= 0xFF
	forged := base64.RawURLEncoding.EncodeToString(raw)

	_, err = s.Authorize(AuthorizeRequest{
		Caller: "bob", Operation: OpDecrypt,
		Key:     KeyResource{KeyID: "k1", Version: 1},
		Context: Context{"env": "prod"}, Token: forged,
	})
	if !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("tampered token: want ErrTokenInvalid, got %v", err)
	}

	other, err := NewStore(bytes32(0x01), NewFakeClock(), AuthorityFunc(allowAll))
	if err != nil {
		t.Fatal(err)
	}
	_, err = other.Authorize(AuthorizeRequest{Token: enc})
	if !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("foreign service key: want ErrTokenInvalid, got %v", err)
	}
}

func TestExpiredTokenRejected(t *testing.T) {
	clock := NewFakeClock()
	s := newTestStore(t, clock)
	enc := activeToken(t, s, baseCreate())
	clock.Advance(2 * time.Hour)

	_, err := s.Authorize(AuthorizeRequest{
		Caller: "bob", Operation: OpDecrypt,
		Key:     KeyResource{KeyID: "k1", Version: 1},
		Context: Context{"env": "prod"}, Token: enc,
	})
	if !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("expired token: want ErrTokenExpired, got %v", err)
	}
}
