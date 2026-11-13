package netsec

import (
	"bytes"
	"crypto/rand"
	"errors"
	"testing"
)

func TestECDHPartyOrder(t *testing.T) {
	alice, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	bob, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}

	plaintext := []byte("meet at the usual place")
	ciphertext, err := Encrypt(plaintext, alice, bob, "alice", "bob")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	recovered, err := Decrypt(ciphertext, bob, alice, "alice", "bob")
	if err != nil {
		t.Fatalf("Decrypt with encryption order: %v", err)
	}
	if !bytes.Equal(recovered, plaintext) {
		t.Fatalf("plaintext mismatch: got %q want %q", recovered, plaintext)
	}

	if _, err := Decrypt(ciphertext, bob, alice, "bob", "alice"); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("swapped party order must fail with ErrAuthentication, got %v", err)
	}
}

func TestSharedSecretPartyOrder(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	plaintext := []byte("context-bound shared-key message")

	ciphertext, err := Encrypt(plaintext, key, "sender", "receiver")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	recovered, err := Decrypt(ciphertext, key, "sender", "receiver")
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(recovered, plaintext) {
		t.Fatalf("plaintext mismatch: got %q want %q", recovered, plaintext)
	}
	if _, err := Decrypt(ciphertext, key, "receiver", "sender"); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("swapped party order must fail with ErrAuthentication, got %v", err)
	}
}

func TestAnotherSessionCannotBeOpenedWithSwappedParties(t *testing.T) {
	sessionKey := make([]byte, 32)
	if _, err := rand.Read(sessionKey); err != nil {
		t.Fatal(err)
	}

	first, err := Encrypt([]byte("first session"), sessionKey, "u-one", "v-one")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decrypt(first, sessionKey, "v-one", "u-one"); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("swapped first-session parties must fail, got %v", err)
	}
	if _, err := Decrypt(first, sessionKey, "u-two", "v-two"); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("second-session parties must not open first session, got %v", err)
	}

	second, err := Encrypt([]byte("second session"), sessionKey, "u-two", "v-two")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decrypt(second, sessionKey, "v-two", "u-two"); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("swapped second-session parties must fail, got %v", err)
	}
	if _, err := Decrypt(second, sessionKey, "u-one", "v-one"); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("first-session parties must not open second session, got %v", err)
	}
}

func TestWrongRecipientAndTamperingFail(t *testing.T) {
	alice, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	bob, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	carol, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}

	ciphertext, err := Encrypt([]byte("private"), alice, bob, "alice", "bob")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decrypt(ciphertext, carol, alice, "alice", "bob"); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("wrong recipient must fail, got %v", err)
	}

	tampered := append([]byte(nil), ciphertext...)
	tampered[len(tampered)-1] ^= 0x80
	if _, err := Decrypt(tampered, bob, alice, "alice", "bob"); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("tampered ciphertext must fail, got %v", err)
	}
}

func TestECDHPrivateAndPublicKeys(t *testing.T) {
	alice, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	bob, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	alicePrivate, err := privateKeyFrom(alice)
	if err != nil {
		t.Fatal(err)
	}
	bobPrivate, err := privateKeyFrom(bob)
	if err != nil {
		t.Fatal(err)
	}

	plaintext := []byte("raw ecdh keys")
	ciphertext, err := Encrypt(plaintext, alicePrivate, bobPrivate.PublicKey(), []byte("U"), []byte("V"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	recovered, err := Decrypt(ciphertext, bobPrivate, alicePrivate.PublicKey(), []byte("U"), []byte("V"))
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(recovered, plaintext) {
		t.Fatalf("plaintext mismatch: got %q want %q", recovered, plaintext)
	}
	if _, err := Decrypt(ciphertext, bobPrivate, alicePrivate.PublicKey(), []byte("V"), []byte("U")); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("swapped byte parties must fail, got %v", err)
	}
}

func TestRawECDHKeysAndRawParties(t *testing.T) {
	alice, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	bob, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}

	plaintext := []byte("raw key bytes")
	ciphertext, err := Encrypt(plaintext, alice.Private, bob.Public, []byte("U"), []byte("V"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	recovered, err := Decrypt(ciphertext, bob.Private, alice.Public, []byte("U"), []byte("V"))
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(recovered, plaintext) {
		t.Fatalf("plaintext mismatch: got %q want %q", recovered, plaintext)
	}
	if _, err := Decrypt(ciphertext, bob.Private, alice.Public, []byte("V"), []byte("U")); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("swapped parties must fail, got %v", err)
	}
}

func TestKeyFirstAndPlaintextLastForms(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	plaintext := []byte("plaintext at the end")

	ciphertext, err := Encrypt(key, "U", "V", plaintext)
	if err != nil {
		t.Fatalf("key-first Encrypt: %v", err)
	}
	recovered, err := Decrypt(key, "U", "V", ciphertext)
	if err != nil {
		t.Fatalf("key-first Decrypt: %v", err)
	}
	if !bytes.Equal(recovered, plaintext) {
		t.Fatalf("plaintext mismatch: got %q want %q", recovered, plaintext)
	}
}

func TestPartiesFirstForm(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	plaintext := []byte("parties before plaintext")
	ciphertext, err := Encrypt("U", "V", plaintext, key)
	if err != nil {
		t.Fatalf("party-first Encrypt: %v", err)
	}
	recovered, err := Decrypt("U", "V", ciphertext, key)
	if err != nil {
		t.Fatalf("party-first Decrypt: %v", err)
	}
	if !bytes.Equal(recovered, plaintext) {
		t.Fatalf("plaintext mismatch: got %q want %q", recovered, plaintext)
	}
	if _, err := Decrypt("V", "U", ciphertext, key); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("swapped party-first order must fail, got %v", err)
	}

	keyFirst, err := Encrypt("U", "V", key, []byte("key before plaintext"))
	if err != nil {
		t.Fatalf("party-first key-first Encrypt: %v", err)
	}
	recovered, err = Decrypt("U", "V", keyFirst, key)
	if err != nil {
		t.Fatalf("party-first key-first Decrypt: %v", err)
	}
	if string(recovered) != "key before plaintext" {
		t.Fatalf("unexpected plaintext %q", recovered)
	}
}

func TestPartiesFirstWithKeyPairs(t *testing.T) {
	alice, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	bob, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	plaintext := []byte("key pairs after parties")
	ciphertext, err := Encrypt("U", "V", alice, bob, plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	recovered, err := Decrypt("U", "V", bob, alice, ciphertext)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(recovered, plaintext) {
		t.Fatalf("plaintext mismatch: got %q want %q", recovered, plaintext)
	}
}

func TestSessionRoundTripAndSwap(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	session, err := NewSession([]byte("U"), []byte("V"), key)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := session.Encrypt([]byte("session message"))
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := session.Decrypt(ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	if string(plaintext) != "session message" {
		t.Fatalf("unexpected plaintext %q", plaintext)
	}

	swapped, err := NewSession([]byte("V"), []byte("U"), key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := swapped.Decrypt(ciphertext); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("swapped session must fail, got %v", err)
	}
}

func TestDeriveKeyDependsOnPartyOrder(t *testing.T) {
	secret := []byte("shared secret")
	uv, err := DeriveKey(secret, "sentinel", "recipient")
	if err != nil {
		t.Fatal(err)
	}
	vu, err := DeriveKey(secret, "recipient", "sentinel")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(uv, vu) {
		t.Fatal("party order must affect the derived key")
	}
}

func TestKDFContextDependsOnPartyOrder(t *testing.T) {
	secret := []byte("shared secret")
	uv, err := DeriveKeyWithContext(secret, KDFContext{AlgorithmID: 1, PartyUInfo: []byte("U"), PartyVInfo: []byte("V")})
	if err != nil {
		t.Fatal(err)
	}
	vu, err := DeriveKeyWithContext(secret, KDFContext{AlgorithmID: 1, PartyUInfo: []byte("V"), PartyVInfo: []byte("U")})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(uv, vu) {
		t.Fatal("swapped KDFContext parties must derive different keys")
	}
}

func TestPartyInfoOrder(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	u := PartyInfo{ID: []byte("device-u"), Nonce: []byte{1}}
	v := PartyInfo{ID: []byte("device-v"), Nonce: []byte{2}}
	ciphertext, err := Encrypt([]byte("party info"), key, u, v)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decrypt(ciphertext, key, u, v); err != nil {
		t.Fatalf("ordered PartyInfo must decrypt: %v", err)
	}
	if _, err := Decrypt(ciphertext, key, v, u); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("swapped PartyInfo must fail, got %v", err)
	}
}

func TestSupportedSharedKeyCallForms(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	u := []byte("party-u")
	v := []byte("party-v")
	plaintext := []byte("same plaintext")

	tests := []struct {
		name    string
		encrypt func() ([]byte, error)
		decrypt func([]byte) ([]byte, error)
		swapped func([]byte) ([]byte, error)
	}{
		{
			name:    "plaintext key strings",
			encrypt: func() ([]byte, error) { return Encrypt(plaintext, key, "U", "V") },
			decrypt: func(ct []byte) ([]byte, error) { return Decrypt(ct, key, "U", "V") },
			swapped: func(ct []byte) ([]byte, error) { return Decrypt(ct, key, "V", "U") },
		},
		{
			name:    "plaintext strings key",
			encrypt: func() ([]byte, error) { return Encrypt(plaintext, "U", "V", key) },
			decrypt: func(ct []byte) ([]byte, error) { return Decrypt(ct, "U", "V", key) },
			swapped: func(ct []byte) ([]byte, error) { return Decrypt(ct, "V", "U", key) },
		},
		{
			name:    "parties first",
			encrypt: func() ([]byte, error) { return Encrypt("U", "V", plaintext, key) },
			decrypt: func(ct []byte) ([]byte, error) { return Decrypt("U", "V", ct, key) },
			swapped: func(ct []byte) ([]byte, error) { return Decrypt("V", "U", ct, key) },
		},
		{
			name:    "parties first key first",
			encrypt: func() ([]byte, error) { return Encrypt("U", "V", key, plaintext) },
			decrypt: func(ct []byte) ([]byte, error) { return Decrypt("U", "V", ct, key) },
			swapped: func(ct []byte) ([]byte, error) { return Decrypt("V", "U", ct, key) },
		},
		{
			name:    "key first",
			encrypt: func() ([]byte, error) { return Encrypt(key, "U", "V", plaintext) },
			decrypt: func(ct []byte) ([]byte, error) { return Decrypt(key, "U", "V", ct) },
			swapped: func(ct []byte) ([]byte, error) { return Decrypt(key, "V", "U", ct) },
		},
		{
			name:    "key then plaintext",
			encrypt: func() ([]byte, error) { return Encrypt(key, plaintext, "U", "V") },
			decrypt: func(ct []byte) ([]byte, error) { return Decrypt(key, ct, "U", "V") },
			swapped: func(ct []byte) ([]byte, error) { return Decrypt(key, ct, "V", "U") },
		},
		{
			name:    "raw parties key",
			encrypt: func() ([]byte, error) { return Encrypt(plaintext, key, u, v) },
			decrypt: func(ct []byte) ([]byte, error) { return Decrypt(ct, key, u, v) },
			swapped: func(ct []byte) ([]byte, error) { return Decrypt(ct, key, v, u) },
		},
		{
			name:    "raw parties key last",
			encrypt: func() ([]byte, error) { return Encrypt(plaintext, u, v, key) },
			decrypt: func(ct []byte) ([]byte, error) { return Decrypt(ct, u, v, key) },
			swapped: func(ct []byte) ([]byte, error) { return Decrypt(ct, v, u, key) },
		},
		{
			name:    "explicit parties",
			encrypt: func() ([]byte, error) { return EncryptWithParties(u, v, plaintext, key) },
			decrypt: func(ct []byte) ([]byte, error) { return DecryptWithParties(u, v, ct, key) },
			swapped: func(ct []byte) ([]byte, error) { return DecryptWithParties(v, u, ct, key) },
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ciphertext, err := test.encrypt()
			if err != nil {
				t.Fatalf("Encrypt: %v", err)
			}
			recovered, err := test.decrypt(ciphertext)
			if err != nil {
				t.Fatalf("Decrypt: %v", err)
			}
			if !bytes.Equal(recovered, plaintext) {
				t.Fatalf("plaintext mismatch: got %q want %q", recovered, plaintext)
			}
			if _, err := test.swapped(ciphertext); !errors.Is(err, ErrAuthentication) {
				t.Fatalf("swapped parties must fail, got %v", err)
			}
		})
	}
}
