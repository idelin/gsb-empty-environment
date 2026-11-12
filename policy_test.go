package netsec

import (
	"bytes"
	"errors"
	"testing"
)

func TestEveryRegisteredRecipientGetsWrappedKey(t *testing.T) {
	registry := testRegistry(t, "alice", "bob")
	wrapper := newFakeWrapper()
	encryptor, err := NewEncryptor(EncryptorConfig{
		Topic:      "topic-a",
		Registry:   registry,
		KeyWrapper: wrapper,
	})
	if err != nil {
		t.Fatalf("new encryptor: %v", err)
	}

	wantPlaintext := []byte("shared secret")
	message, err := encryptor.Encrypt(wantPlaintext)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if len(message.EncryptedKeys) != 2 {
		t.Fatalf("wrapped key count = %d, want 2", len(message.EncryptedKeys))
	}
	for _, name := range []string{"alice", "bob"} {
		if wrapper.calls(name) != 1 {
			t.Fatalf("recipient %q was wrapped %d times, want 1", name, wrapper.calls(name))
		}
	}
	if message.EncryptedKeys[0].KeyName != "alice" || message.EncryptedKeys[1].KeyName != "bob" {
		t.Fatalf("recipient key order = %q, %q; want alice, bob",
			message.EncryptedKeys[0].KeyName, message.EncryptedKeys[1].KeyName)
	}

	sharedKey := testRSAKey(t)
	decryptor, err := NewDecryptor(DecryptorConfig{
		Topic:    "topic-a",
		Registry: registry,
		PrivateKeys: []RecipientPrivateKey{
			{KeyName: "alice", PrivateKey: sharedKey},
		},
		KeyWrapper: wrapper,
	})
	if err != nil {
		t.Fatalf("new decryptor: %v", err)
	}
	plaintext, err := decryptor.Decrypt(message)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if !bytes.Equal(plaintext, wantPlaintext) {
		t.Fatalf("plaintext = %q, want %q", plaintext, wantPlaintext)
	}
}

func TestDecryptRejectsExtraKeyNames(t *testing.T) {
	registry := testRegistry(t, "alice")
	wrapper := newFakeWrapper()
	encryptor, err := NewEncryptor(EncryptorConfig{
		Topic:      "topic-a",
		Registry:   registry,
		KeyWrapper: wrapper,
	})
	if err != nil {
		t.Fatalf("new encryptor: %v", err)
	}

	message, err := encryptor.Encrypt([]byte("sensitive"))
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	attacked := cloneMessage(t, message)
	attacked.EncryptedKeys = append(attacked.EncryptedKeys, WrappedDataKey{
		KeyName:    "mallory",
		WrappedKey: []byte("attacker-controlled wrapped key"),
	})

	sharedKey := testRSAKey(t)
	decryptor, err := NewDecryptor(DecryptorConfig{
		Topic:    "topic-a",
		Registry: registry,
		PrivateKeys: []RecipientPrivateKey{
			{KeyName: "alice", PrivateKey: sharedKey},
		},
		KeyWrapper: wrapper,
	})
	if err != nil {
		t.Fatalf("new decryptor: %v", err)
	}

	if _, err := decryptor.Decrypt(attacked); !errors.Is(err, ErrUnknownKeyName) {
		t.Fatalf("decrypt error = %v, want ErrUnknownKeyName", err)
	}
}

func TestDecryptRejectsDuplicateKeyNames(t *testing.T) {
	registry := testRegistry(t, "alice")
	wrapper := newFakeWrapper()
	encryptor, err := NewEncryptor(EncryptorConfig{
		Topic:      "topic-a",
		Registry:   registry,
		KeyWrapper: wrapper,
	})
	if err != nil {
		t.Fatalf("new encryptor: %v", err)
	}
	message, err := encryptor.Encrypt([]byte("sensitive"))
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	duplicated := cloneMessage(t, message)
	duplicated.EncryptedKeys = append(duplicated.EncryptedKeys, duplicated.EncryptedKeys[0])

	decryptor, err := NewDecryptor(DecryptorConfig{
		Topic:       "topic-a",
		Registry:    registry,
		PrivateKeys: []RecipientPrivateKey{{KeyName: "alice", PrivateKey: testRSAKey(t)}},
		KeyWrapper:  wrapper,
	})
	if err != nil {
		t.Fatalf("new decryptor: %v", err)
	}
	if _, err := decryptor.Decrypt(duplicated); !errors.Is(err, ErrDuplicateKeyName) {
		t.Fatalf("decrypt error = %v, want ErrDuplicateKeyName", err)
	}
}

func TestDecryptRejectsReplayedNonce(t *testing.T) {
	registry := testRegistry(t, "alice")
	wrapper := newFakeWrapper()
	encryptor, err := NewEncryptor(EncryptorConfig{
		Topic:      "topic-a",
		Registry:   registry,
		KeyWrapper: wrapper,
	})
	if err != nil {
		t.Fatalf("new encryptor: %v", err)
	}

	first, err := encryptor.Encrypt([]byte("first"))
	if err != nil {
		t.Fatalf("encrypt first: %v", err)
	}
	second, err := encryptor.Encrypt([]byte("second"))
	if err != nil {
		t.Fatalf("encrypt second: %v", err)
	}

	tampered := cloneMessage(t, second)
	copy(tampered.Nonce, first.Nonce)

	decryptor, err := NewDecryptor(DecryptorConfig{
		Topic:       "topic-a",
		Registry:    registry,
		PrivateKeys: []RecipientPrivateKey{{KeyName: "alice", PrivateKey: testRSAKey(t)}},
		KeyWrapper:  wrapper,
	})
	if err != nil {
		t.Fatalf("new decryptor: %v", err)
	}
	if _, err := decryptor.Decrypt(tampered); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("replayed-nonce error = %v, want ErrDecrypt", err)
	}
}
