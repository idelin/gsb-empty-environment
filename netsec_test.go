package netsec

import (
	"bytes"
	"crypto/ecdh"
	"crypto/rand"
	"errors"
	"testing"
)

type session struct {
	ctx    *Context
	uPriv  *ecdh.PrivateKey
	vPriv  *ecdh.PrivateKey
	uKeyID []byte
	vKeyID []byte
}

func randBytes(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return b
}

// newSession establishes a key-agreement session between two parties.
func newSession(t *testing.T, uName, vName string) *session {
	t.Helper()
	uPriv, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	vPriv, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	return &session{
		ctx: &Context{
			SessionID: randBytes(t, 16),
			PartyU: PartyInfo{
				Identity:  []byte(uName),
				Nonce:     randBytes(t, 16),
				PublicKey: uPriv.PublicKey().Bytes(),
			},
			PartyV: PartyInfo{
				Identity:  []byte(vName),
				Nonce:     randBytes(t, 16),
				PublicKey: vPriv.PublicKey().Bytes(),
			},
		},
		uPriv:  uPriv,
		vPriv:  vPriv,
		uKeyID: []byte(uName),
		vKeyID: []byte(vName),
	}
}

func TestDecryptWithMatchingContext(t *testing.T) {
	s := newSession(t, "alice", "bob")
	plaintext := []byte("session keys rotated")
	aad := []byte("header")

	msg, err := Encrypt(s.ctx, plaintext, aad, []*RecipientKey{
		{KeyID: s.vKeyID, PublicKey: s.vPriv.PublicKey()},
	})
	if err != nil {
		t.Fatal(err)
	}

	got, err := Decrypt(msg, s.ctx, s.vKeyID, s.vPriv, aad)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("plaintext mismatch: got %q", got)
	}
}

// TestDecryptWithForeignSessionContext uses the party information of another
// session to decrypt: the recipient key unwraps, but the context check must
// withhold the plaintext.
func TestDecryptWithForeignSessionContext(t *testing.T) {
	sessionA := newSession(t, "alice", "bob")
	sessionB := newSession(t, "carol", "dave")
	plaintext := []byte("session A secret")

	msg, err := Encrypt(sessionA.ctx, plaintext, nil, []*RecipientKey{
		{KeyID: sessionA.vKeyID, PublicKey: sessionA.vPriv.PublicKey()},
	})
	if err != nil {
		t.Fatal(err)
	}

	// The message is replayed into session B: the recipient's private key
	// still unwraps the CEK, but the context must not match.
	got, err := Decrypt(msg, sessionB.ctx, sessionA.vKeyID, sessionA.vPriv, nil)
	if !errors.Is(err, ErrContextMismatch) {
		t.Fatalf("expected ErrContextMismatch, got %v", err)
	}
	if got != nil {
		t.Fatalf("plaintext must not be returned on context mismatch, got %q", got)
	}
}

// TestDecryptWithoutContext covers a recipient that has no key-agreement
// context at all: decryption must not yield trusted plaintext.
func TestDecryptWithoutContext(t *testing.T) {
	s := newSession(t, "alice", "bob")
	msg, err := Encrypt(s.ctx, []byte("secret"), nil, []*RecipientKey{
		{KeyID: s.vKeyID, PublicKey: s.vPriv.PublicKey()},
	})
	if err != nil {
		t.Fatal(err)
	}

	got, err := Decrypt(msg, nil, s.vKeyID, s.vPriv, nil)
	if !errors.Is(err, ErrMissingContext) {
		t.Fatalf("expected ErrMissingContext, got %v", err)
	}
	if got != nil {
		t.Fatalf("plaintext must not be returned without context, got %q", got)
	}
}

func TestEncryptRequiresContext(t *testing.T) {
	s := newSession(t, "alice", "bob")
	if _, err := Encrypt(nil, []byte("x"), nil, []*RecipientKey{
		{KeyID: s.vKeyID, PublicKey: s.vPriv.PublicKey()},
	}); !errors.Is(err, ErrMissingContext) {
		t.Fatalf("expected ErrMissingContext, got %v", err)
	}
}

func TestDecryptWrongKey(t *testing.T) {
	s := newSession(t, "alice", "bob")
	msg, err := Encrypt(s.ctx, []byte("secret"), nil, []*RecipientKey{
		{KeyID: s.vKeyID, PublicKey: s.vPriv.PublicKey()},
	})
	if err != nil {
		t.Fatal(err)
	}

	got, err := Decrypt(msg, s.ctx, s.vKeyID, s.uPriv, nil)
	if !errors.Is(err, ErrDecryption) {
		t.Fatalf("expected ErrDecryption, got %v", err)
	}
	if got != nil {
		t.Fatalf("plaintext must not be returned for wrong key, got %q", got)
	}
}

func TestDecryptUnknownRecipient(t *testing.T) {
	s := newSession(t, "alice", "bob")
	msg, err := Encrypt(s.ctx, []byte("secret"), nil, []*RecipientKey{
		{KeyID: s.vKeyID, PublicKey: s.vPriv.PublicKey()},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decrypt(msg, s.ctx, []byte("mallory"), s.vPriv, nil); !errors.Is(err, ErrNoRecipient) {
		t.Fatalf("expected ErrNoRecipient, got %v", err)
	}
}

func TestDecryptTamperedCiphertext(t *testing.T) {
	s := newSession(t, "alice", "bob")
	msg, err := Encrypt(s.ctx, []byte("secret"), nil, []*RecipientKey{
		{KeyID: s.vKeyID, PublicKey: s.vPriv.PublicKey()},
	})
	if err != nil {
		t.Fatal(err)
	}
	msg.Ciphertext[len(msg.Ciphertext)-1] ^= 0xff
	if _, err := Decrypt(msg, s.ctx, s.vKeyID, s.vPriv, nil); !errors.Is(err, ErrDecryption) {
		t.Fatalf("expected ErrDecryption, got %v", err)
	}
}

func TestMarshalRoundTrip(t *testing.T) {
	s := newSession(t, "alice", "bob")
	plaintext := []byte("portable message")
	msg, err := Encrypt(s.ctx, plaintext, nil, []*RecipientKey{
		{KeyID: s.vKeyID, PublicKey: s.vPriv.PublicKey()},
		{KeyID: s.uKeyID, PublicKey: s.uPriv.PublicKey()},
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := msg.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseMessage(data)
	if err != nil {
		t.Fatal(err)
	}
	for _, rcpt := range []struct {
		keyID []byte
		priv  *ecdh.PrivateKey
	}{
		{s.vKeyID, s.vPriv},
		{s.uKeyID, s.uPriv},
	} {
		got, err := Decrypt(parsed, s.ctx, rcpt.keyID, rcpt.priv, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, plaintext) {
			t.Fatalf("plaintext mismatch: got %q", got)
		}
	}
}
