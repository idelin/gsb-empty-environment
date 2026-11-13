package netsec

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"testing"
)

func testKey(t *testing.T, id string) Key {
	t.Helper()
	digest := sha256.Sum256([]byte("test-key-material:" + id))
	key, err := NewKey(id, digest[:])
	if err != nil {
		t.Fatalf("NewKey(%q): %v", id, err)
	}
	return key
}

func testRing(t *testing.T, keys ...Key) *StaticKeyRing {
	t.Helper()
	ring, err := NewStaticKeyRing(keys...)
	if err != nil {
		t.Fatalf("NewStaticKeyRing: %v", err)
	}
	return ring
}

func aesGCMFactory(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func testEnvelope(t *testing.T, ring KeyRing) *Envelope {
	t.Helper()
	envelope, err := NewEnvelope(AEADFactoryFunc(aesGCMFactory), ring)
	if err != nil {
		t.Fatalf("NewEnvelope: %v", err)
	}
	return envelope
}

func mustSeal(t *testing.T, envelope *Envelope, keyID string, plaintext, aad []byte) *Record {
	t.Helper()
	record, err := envelope.Seal(keyID, plaintext, aad)
	if err != nil {
		t.Fatalf("Seal(%q): %v", keyID, err)
	}
	return record
}

func TestRoundTripAndCanonicalEncoding(t *testing.T) {
	ring := testRing(t, testKey(t, "tenant-a"))
	envelope := testEnvelope(t, ring)
	plaintext := []byte("tenant-a secret object")
	aad := []byte("bucket=objects;object=42")

	record := mustSeal(t, envelope, "tenant-a", plaintext, aad)
	if record.Version != RecordVersion {
		t.Fatalf("version = %d, want %d", record.Version, RecordVersion)
	}
	if record.KeyID != "tenant-a" {
		t.Fatalf("key ID = %q", record.KeyID)
	}
	if len(record.Nonce) != 12 {
		t.Fatalf("nonce length = %d, want 12", len(record.Nonce))
	}
	if record.Commit == ([CommitmentSize]byte{}) {
		t.Fatal("commitment is all zero")
	}

	encoded, err := record.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary: %v", err)
	}
	parsed, err := ParseRecord(encoded)
	if err != nil {
		t.Fatalf("ParseRecord: %v", err)
	}
	reencoded, err := parsed.MarshalBinary()
	if err != nil {
		t.Fatalf("parsed MarshalBinary: %v", err)
	}
	if !bytes.Equal(encoded, reencoded) {
		t.Fatal("record encoding is not canonical across parse/marshal")
	}

	opened, err := envelope.Open(parsed, aad)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !bytes.Equal(opened, plaintext) {
		t.Fatalf("plaintext = %q, want %q", opened, plaintext)
	}

	opened, err = envelope.OpenBytes(encoded, aad)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	if !bytes.Equal(opened, plaintext) {
		t.Fatalf("OpenBytes plaintext = %q, want %q", opened, plaintext)
	}
}

func TestSealUsesFreshRandomNonce(t *testing.T) {
	envelope := testEnvelope(t, testRing(t, testKey(t, "tenant-a")))
	first := mustSeal(t, envelope, "tenant-a", []byte("same"), nil)
	second := mustSeal(t, envelope, "tenant-a", []byte("same"), nil)
	if bytes.Equal(first.Nonce, second.Nonce) {
		t.Fatal("two seals reused a nonce")
	}
	if bytes.Equal(first.Ciphertext, second.Ciphertext) {
		t.Fatal("two seals produced identical ciphertext")
	}
}

func TestCommitmentMatchesSpecification(t *testing.T) {
	key := testKey(t, "tenant-a")
	envelope := testEnvelope(t, testRing(t, key))
	aad := []byte("object-aad")
	record := mustSeal(t, envelope, key.ID, []byte("payload"), aad)

	commitmentKeyMAC := hmac.New(sha256.New, key.Material[:])
	commitmentKeyMAC.Write([]byte(CommitmentKeyLabel))
	commitmentKey := commitmentKeyMAC.Sum(nil)

	mac := hmac.New(sha256.New, commitmentKey)
	mac.Write([]byte{record.Version})
	writeTestLength(mac, []byte(record.KeyID))
	writeTestLength(mac, record.Nonce)
	writeTestLength(mac, aad)
	writeTestLength(mac, record.Ciphertext)
	expected := mac.Sum(nil)[:CommitmentSize]

	if !hmac.Equal(expected, record.Commit[:]) {
		t.Fatalf("commitment = %x, want %x", record.Commit, expected)
	}
}

func writeTestLength(mac interface{ Write([]byte) (int, error) }, value []byte) {
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(value)))
	mac.Write(length[:])
	mac.Write(value)
}

func TestTamperingReturnsUniformOpenError(t *testing.T) {
	keyA := testKey(t, "tenant-a")
	keyB := testKey(t, "tenant-b")
	envelope := testEnvelope(t, testRing(t, keyA, keyB))
	plaintext := []byte("do not disclose")
	aad := []byte("aad")
	record := mustSeal(t, envelope, keyA.ID, plaintext, aad)

	tests := []struct {
		name   string
		mutate func(*Record) []byte
	}{
		{
			name: "key ID",
			mutate: func(record *Record) []byte {
				record.KeyID = keyB.ID
				return aad
			},
		},
		{
			name: "nonce",
			mutate: func(record *Record) []byte {
				record.Nonce[0] ^= 0x80
				return aad
			},
		},
		{
			name: "nonce length",
			mutate: func(record *Record) []byte {
				record.Nonce = record.Nonce[:len(record.Nonce)-1]
				return aad
			},
		},
		{
			name: "AAD",
			mutate: func(record *Record) []byte {
				return []byte("different AAD")
			},
		},
		{
			name: "AAD length",
			mutate: func(record *Record) []byte {
				return []byte("aad!")
			},
		},
		{
			name: "ciphertext",
			mutate: func(record *Record) []byte {
				record.Ciphertext[len(record.Ciphertext)-1] ^= 0x01
				return aad
			},
		},
		{
			name: "ciphertext length",
			mutate: func(record *Record) []byte {
				record.Ciphertext = record.Ciphertext[:len(record.Ciphertext)-1]
				return aad
			},
		},
		{
			name: "commitment",
			mutate: func(record *Record) []byte {
				record.Commit[0] ^= 0x01
				return aad
			},
		},
		{
			name: "version",
			mutate: func(record *Record) []byte {
				record.Version++
				return aad
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tampered := record.Clone()
			tamperedAAD := test.mutate(tampered)
			plaintext, err := envelope.Open(tampered, tamperedAAD)
			if err != ErrOpen {
				t.Fatalf("error = %v, want exactly ErrOpen", err)
			}
			if plaintext != nil {
				t.Fatalf("plaintext was returned on failure: %q", plaintext)
			}
		})
	}
}

func TestKeyIDIsBoundEvenWhenKeysAreIdentical(t *testing.T) {
	keyA := testKey(t, "tenant-a")
	keyB := keyA
	keyB.ID = "tenant-b"

	envelope := testEnvelope(t, testRing(t, keyA, keyB))
	record := mustSeal(t, envelope, keyA.ID, []byte("secret"), nil)
	tampered := record.Clone()
	tampered.KeyID = keyB.ID

	plaintext, err := envelope.Open(tampered, nil)
	if err != ErrOpen || plaintext != nil {
		t.Fatalf("Open = (%q, %v), want (nil, ErrOpen)", plaintext, err)
	}
}

func TestUnknownAndDuplicateKeyIDsUseUniformError(t *testing.T) {
	keyA := testKey(t, "tenant-a")
	envelope := testEnvelope(t, testRing(t, keyA))
	record := mustSeal(t, envelope, keyA.ID, []byte("secret"), nil)

	unknown := record.Clone()
	unknown.KeyID = "tenant-unknown"
	plaintext, err := envelope.Open(unknown, nil)
	if err != ErrOpen || plaintext != nil {
		t.Fatalf("unknown key Open = (%q, %v), want (nil, ErrOpen)", plaintext, err)
	}

	duplicateRing := testRing(t, keyA, keyA)
	duplicateEnvelope := testEnvelope(t, duplicateRing)
	plaintext, err = duplicateEnvelope.Open(record, nil)
	if err != ErrOpen || plaintext != nil {
		t.Fatalf("duplicate key Open = (%q, %v), want (nil, ErrOpen)", plaintext, err)
	}

	if _, err := duplicateEnvelope.Seal(keyA.ID, []byte("secret"), nil); !errors.Is(err, ErrKeyUnavailable) {
		t.Fatalf("duplicate key Seal error = %v, want ErrKeyUnavailable", err)
	}
}

func TestMalformedRecordEncodingIsRejected(t *testing.T) {
	envelope := testEnvelope(t, testRing(t, testKey(t, "tenant-a")))
	record := mustSeal(t, envelope, "tenant-a", []byte("secret"), nil)
	valid, err := record.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary: %v", err)
	}

	version := cloneBytes(valid)
	version[recordVersionOffset] = 2
	zeroKeyIDLength := cloneBytes(valid)
	zeroKeyIDLength[recordKeyIDLenOffset] = 0
	zeroNonceLength := cloneBytes(valid)
	zeroNonceLength[recordNonceLenOffset] = 0
	oversizedCiphertext := cloneBytes(valid)
	binary.BigEndian.PutUint32(oversizedCiphertext[recordCiphertextLenOffset:recordCommitOffset], MaxCiphertextLen+1)

	tests := map[string][]byte{
		"empty":               nil,
		"short header":        valid[:recordHeaderSize-1],
		"truncated payload":   valid[:len(valid)-1],
		"trailing data":       append(cloneBytes(valid), 0),
		"unsupported version": version,
		"zero key ID length":  zeroKeyIDLength,
		"zero nonce length":   zeroNonceLength,
		"oversized ct":        oversizedCiphertext,
	}

	for name, encoded := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseRecord(encoded); !errors.Is(err, ErrMalformedRecord) {
				t.Fatalf("ParseRecord error = %v, want ErrMalformedRecord", err)
			}
			plaintext, err := envelope.OpenBytes(encoded, nil)
			if err != ErrOpen || plaintext != nil {
				t.Fatalf("OpenBytes = (%q, %v), want (nil, ErrOpen)", plaintext, err)
			}
		})
	}
}

func TestSealInputLimits(t *testing.T) {
	envelope := testEnvelope(t, testRing(t, testKey(t, "tenant-a")))

	if _, err := envelope.Seal("", []byte("secret"), nil); !errors.Is(err, ErrInvalidKeyID) {
		t.Fatalf("empty key ID error = %v", err)
	}
	if _, err := envelope.Seal(string(bytes.Repeat([]byte("k"), MaxKeyIDLen+1)), []byte("secret"), nil); !errors.Is(err, ErrInvalidKeyID) {
		t.Fatalf("overlong key ID error = %v", err)
	}
	if _, err := envelope.Seal("tenant-a", []byte("secret"), make([]byte, MaxAADLen+1)); !errors.Is(err, ErrInputTooLarge) {
		t.Fatalf("oversized AAD error = %v", err)
	}
}

func TestNewKeyRejectsWrongLength(t *testing.T) {
	if _, err := NewKey("tenant-a", make([]byte, KeySize-1)); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("short key error = %v", err)
	}
	if _, err := NewKey("tenant-a", make([]byte, KeySize+1)); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("long key error = %v", err)
	}
}
