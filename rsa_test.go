package netsec

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
)

func TestRSAOAEPRecipientsAndAuthenticatedMetadata(t *testing.T) {
	aliceKey := generateRSAKey(t, 2048)
	bobKey := generateRSAKey(t, 2048)
	registry, err := NewRegistry([]Recipient{
		{KeyName: "alice", PublicKey: &aliceKey.PublicKey},
		{KeyName: "bob", PublicKey: &bobKey.PublicKey},
	})
	if err != nil {
		t.Fatalf("create registry: %v", err)
	}

	const topic = "persistent://tenant/namespace/topic"
	encryptor, err := NewEncryptor(EncryptorConfig{
		Topic:    topic,
		Registry: registry,
	})
	if err != nil {
		t.Fatalf("new encryptor: %v", err)
	}
	wantPlaintext := []byte("end-to-end payload")
	message, err := encryptor.Encrypt(wantPlaintext)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if len(message.EncryptedKeys) != 2 {
		t.Fatalf("wrapped key count = %d, want 2", len(message.EncryptedKeys))
	}
	for i, name := range []string{"alice", "bob"} {
		if message.EncryptedKeys[i].KeyName != name {
			t.Fatalf("key %d name = %q, want %q", i, message.EncryptedKeys[i].KeyName, name)
		}
		if len(message.EncryptedKeys[i].WrappedKey) == 0 {
			t.Fatalf("recipient %q has an empty wrapped key", name)
		}
	}

	aliceDecryptor, err := NewDecryptor(DecryptorConfig{
		Topic:       topic,
		Registry:    registry,
		PrivateKeys: []RecipientPrivateKey{{KeyName: "alice", PrivateKey: aliceKey}},
	})
	if err != nil {
		t.Fatalf("new alice decryptor: %v", err)
	}
	plaintext, err := aliceDecryptor.Decrypt(message)
	if err != nil {
		t.Fatalf("decrypt as alice: %v", err)
	}
	if !bytes.Equal(plaintext, wantPlaintext) {
		t.Fatalf("alice plaintext = %q, want %q", plaintext, wantPlaintext)
	}

	bobDecryptor, err := NewDecryptor(DecryptorConfig{
		Topic:       topic,
		Registry:    registry,
		PrivateKeys: []RecipientPrivateKey{{KeyName: "bob", PrivateKey: bobKey}},
	})
	if err != nil {
		t.Fatalf("new bob decryptor: %v", err)
	}
	plaintext, err = bobDecryptor.Decrypt(message)
	if err != nil {
		t.Fatalf("decrypt as bob: %v", err)
	}
	if !bytes.Equal(plaintext, wantPlaintext) {
		t.Fatalf("bob plaintext = %q, want %q", plaintext, wantPlaintext)
	}

	encoded, err := json.Marshal(message)
	if err != nil {
		t.Fatalf("marshal message: %v", err)
	}
	var transported Message
	if err := json.Unmarshal(encoded, &transported); err != nil {
		t.Fatalf("unmarshal message: %v", err)
	}
	plaintext, err = aliceDecryptor.Decrypt(&transported)
	if err != nil {
		t.Fatalf("decrypt JSON-transported message: %v", err)
	}
	if !bytes.Equal(plaintext, wantPlaintext) {
		t.Fatalf("transported plaintext = %q, want %q", plaintext, wantPlaintext)
	}

	extraKey := cloneMessage(t, message)
	extraKey.EncryptedKeys = append(extraKey.EncryptedKeys, WrappedDataKey{
		KeyName:    "mallory",
		WrappedKey: make([]byte, 256),
	})
	if _, err := aliceDecryptor.Decrypt(extraKey); !errors.Is(err, ErrUnknownKeyName) {
		t.Fatalf("extra-key error = %v, want ErrUnknownKeyName", err)
	}

	tamperedKeyList := cloneMessage(t, message)
	tamperedKeyList.EncryptedKeys[1].WrappedKey[0] ^= 0xff
	if _, err := aliceDecryptor.Decrypt(tamperedKeyList); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("tampered key-list error = %v, want ErrDecrypt", err)
	}

	otherTopicDecryptor, err := NewDecryptor(DecryptorConfig{
		Topic:       topic + "-other",
		Registry:    registry,
		PrivateKeys: []RecipientPrivateKey{{KeyName: "alice", PrivateKey: aliceKey}},
	})
	if err != nil {
		t.Fatalf("new other-topic decryptor: %v", err)
	}
	if _, err := otherTopicDecryptor.Decrypt(message); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("wrong-topic error = %v, want ErrDecrypt", err)
	}
}
