package netsec

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"
	"time"
)

func TestRotationAfterMessageLimit(t *testing.T) {
	registry := testRegistry(t, "alice")
	encryptor, err := NewEncryptor(EncryptorConfig{
		Topic:      "topic-a",
		Registry:   registry,
		KeyWrapper: newFakeWrapper(),
		Cipher:     newRecordingCipher(),
	})
	if err != nil {
		t.Fatalf("new encryptor: %v", err)
	}

	messages := make([]*Message, DefaultMaxMessages+1)
	for i := range messages {
		messages[i], err = encryptor.Encrypt([]byte("payload"))
		if err != nil {
			t.Fatalf("encrypt message %d: %v", i+1, err)
		}
	}

	firstKeyID := messages[0].DataKeyID
	for i := uint64(0); i < DefaultMaxMessages; i++ {
		if messages[i].DataKeyID != firstKeyID {
			t.Fatalf("message %d rotated early: got %q, want %q", i+1, messages[i].DataKeyID, firstKeyID)
		}
	}
	if messages[DefaultMaxMessages].DataKeyID == firstKeyID {
		t.Fatalf("message %d did not rotate after %d messages", DefaultMaxMessages+1, DefaultMaxMessages)
	}
}

func TestRotationAtAgeBoundary(t *testing.T) {
	registry := testRegistry(t, "alice")
	now := time.Date(2026, time.November, 12, 12, 0, 0, 0, time.UTC)
	encryptor, err := NewEncryptor(EncryptorConfig{
		Topic:      "topic-a",
		Registry:   registry,
		KeyWrapper: newFakeWrapper(),
		Now:        func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("new encryptor: %v", err)
	}

	first, err := encryptor.Encrypt([]byte("one"))
	if err != nil {
		t.Fatalf("encrypt first message: %v", err)
	}

	now = now.Add(DefaultMaxAge - time.Second)
	stillFirst, err := encryptor.Encrypt([]byte("two"))
	if err != nil {
		t.Fatalf("encrypt before age limit: %v", err)
	}
	if stillFirst.DataKeyID != first.DataKeyID {
		t.Fatalf("rotated before four hours: got %q, want %q", stillFirst.DataKeyID, first.DataKeyID)
	}

	now = now.Add(time.Second)
	second, err := encryptor.Encrypt([]byte("three"))
	if err != nil {
		t.Fatalf("encrypt at age limit: %v", err)
	}
	if second.DataKeyID == first.DataKeyID {
		t.Fatalf("did not rotate at exactly four hours")
	}
}

func TestNonceAndCounterNeverRepeatWithinDataKey(t *testing.T) {
	registry := testRegistry(t, "alice")
	recorder := newRecordingCipher()
	encryptor, err := NewEncryptor(EncryptorConfig{
		Topic:      "topic-a",
		Registry:   registry,
		KeyWrapper: newFakeWrapper(),
		Cipher:     recorder,
	})
	if err != nil {
		t.Fatalf("new encryptor: %v", err)
	}

	messages := make([]*Message, DefaultMaxMessages+1)
	for i := range messages {
		messages[i], err = encryptor.Encrypt([]byte("payload"))
		if err != nil {
			t.Fatalf("encrypt message %d: %v", i+1, err)
		}
		if len(messages[i].Nonce) != nonceSize {
			t.Fatalf("message %d nonce length = %d, want %d", i+1, len(messages[i].Nonce), nonceSize)
		}
	}

	if recorder.duplicateCount() != 0 {
		t.Fatalf("payload cipher saw %d repeated nonces", recorder.duplicateCount())
	}
	if recorder.distinctKeyCount() != 2 {
		t.Fatalf("payload cipher saw %d data keys, want 2 after rotation", recorder.distinctKeyCount())
	}

	firstPrefix := messages[0].Nonce[:noncePrefixSize]
	for i := uint64(0); i < DefaultMaxMessages; i++ {
		counter := binary.BigEndian.Uint64(messages[i].Nonce[noncePrefixSize:])
		if counter != i {
			t.Fatalf("message %d counter = %d, want %d", i+1, counter, i)
		}
		if !bytes.Equal(messages[i].Nonce[:noncePrefixSize], firstPrefix) {
			t.Fatalf("message %d changed nonce prefix within one data key", i+1)
		}
	}

	nextKey := messages[DefaultMaxMessages]
	nextPrefix := nextKey.Nonce[:noncePrefixSize]
	nextCounter := binary.BigEndian.Uint64(nextKey.Nonce[noncePrefixSize:])
	if nextCounter != 0 {
		t.Fatalf("first counter under rotated key = %d, want 0", nextCounter)
	}
	if nextKey.DataKeyID == messages[0].DataKeyID {
		t.Fatalf("counter reset without a fresh data key")
	}
	if bytes.Equal(nextPrefix, firstPrefix) {
		t.Log("random four-byte nonce prefixes happened to collide across different data keys")
	}
}

func TestCounterExhaustionForcesRotation(t *testing.T) {
	registry := testRegistry(t, "alice")
	start := uint64(math.MaxUint64) - 1
	encryptor, err := NewEncryptor(EncryptorConfig{
		Topic:             "topic-a",
		Registry:          registry,
		KeyWrapper:        newFakeWrapper(),
		NonceCounterStart: start,
	})
	if err != nil {
		t.Fatalf("new encryptor: %v", err)
	}

	messages := make([]*Message, 4)
	for i := range messages {
		messages[i], err = encryptor.Encrypt([]byte("payload"))
		if err != nil {
			t.Fatalf("encrypt message %d: %v", i+1, err)
		}
	}

	if messages[0].DataKeyID != messages[1].DataKeyID {
		t.Fatalf("rotated before counter exhaustion")
	}
	if messages[2].DataKeyID == messages[1].DataKeyID {
		t.Fatalf("did not rotate after using counter value %d", uint64(math.MaxUint64))
	}
	if messages[2].DataKeyID != messages[3].DataKeyID {
		t.Fatalf("rotated unnecessarily on the first counter of a fresh key")
	}

	for i, want := range []uint64{start, start + 1, start, start + 1} {
		got := binary.BigEndian.Uint64(messages[i].Nonce[noncePrefixSize:])
		if got != want {
			t.Fatalf("message %d counter = %d, want %d", i+1, got, want)
		}
	}
}
