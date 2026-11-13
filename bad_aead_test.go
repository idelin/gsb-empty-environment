package netsec

import (
	"bytes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"testing"
)

// permissiveAEAD intentionally ignores the key. It still authenticates the
// nonce, AAD, and ciphertext, but it is not key-committing: opening the same
// ciphertext under any key succeeds.
type permissiveAEAD struct{}

const permissiveOverhead = sha256.Size

func (permissiveAEAD) NonceSize() int { return 12 }
func (permissiveAEAD) Overhead() int  { return permissiveOverhead }

func (permissiveAEAD) Seal(dst, nonce, plaintext, aad []byte) []byte {
	body := permissiveXOR(nonce, plaintext)
	tag := permissiveTag(nonce, aad, body)
	dst = append(dst, body...)
	return append(dst, tag[:]...)
}

func (permissiveAEAD) Open(dst, nonce, ciphertext, aad []byte) ([]byte, error) {
	if len(ciphertext) < permissiveOverhead {
		return nil, errors.New("permissive AEAD: short ciphertext")
	}
	split := len(ciphertext) - permissiveOverhead
	body := ciphertext[:split]
	tag := ciphertext[split:]
	expected := permissiveTag(nonce, aad, body)
	if !bytes.Equal(tag, expected[:]) {
		return nil, errors.New("permissive AEAD: bad tag")
	}
	return append(dst, permissiveXOR(nonce, body)...), nil
}

func permissiveXOR(nonce, input []byte) []byte {
	output := make([]byte, len(input))
	var counter uint64
	for offset := 0; offset < len(input); offset += sha256.Size {
		blockHash := sha256.New()
		blockHash.Write(nonce)
		var counterBytes [8]byte
		binary.BigEndian.PutUint64(counterBytes[:], counter)
		blockHash.Write(counterBytes[:])
		block := blockHash.Sum(nil)
		for i := 0; i < sha256.Size && offset+i < len(input); i++ {
			output[offset+i] = input[offset+i] ^ block[i]
		}
		counter++
	}
	return output
}

func permissiveTag(nonce, aad, body []byte) [sha256.Size]byte {
	tagHash := sha256.New()
	writeLengthPrefixed(tagHash, nonce)
	writeLengthPrefixed(tagHash, aad)
	writeLengthPrefixed(tagHash, body)
	var tag [sha256.Size]byte
	copy(tag[:], tagHash.Sum(nil))
	return tag
}

func TestCommitmentBlocksCrossKeyIDOpenWithBadAEAD(t *testing.T) {
	keyA := testKey(t, "tenant-a")
	keyB := testKey(t, "tenant-b")
	ring := testRing(t, keyA, keyB)
	envelope, err := NewEnvelope(AEADFactoryFunc(func([]byte) (cipher.AEAD, error) {
		return permissiveAEAD{}, nil
	}), ring)
	if err != nil {
		t.Fatalf("NewEnvelope: %v", err)
	}

	plaintext := []byte("tenant-a secret")
	aad := []byte("object=7")
	record := mustSeal(t, envelope, keyA.ID, plaintext, aad)

	// The injected AEAD itself accepts this ciphertext without knowing which
	// key was used. This is precisely why the independent commitment exists.
	rawPlaintext, err := permissiveAEAD{}.Open(nil, record.Nonce, record.Ciphertext, aad)
	if err != nil {
		t.Fatalf("permissive AEAD Open: %v", err)
	}
	if !bytes.Equal(rawPlaintext, plaintext) {
		t.Fatalf("permissive AEAD plaintext = %q", rawPlaintext)
	}

	crossTenant := record.Clone()
	crossTenant.KeyID = keyB.ID
	opened, err := envelope.Open(crossTenant, aad)
	if err != ErrOpen {
		t.Fatalf("cross-key Open error = %v, want exactly ErrOpen", err)
	}
	if opened != nil {
		t.Fatalf("cross-key Open returned plaintext %q", opened)
	}
}

type countingAEAD struct {
	inner cipher.AEAD
	opens *int
}

func (aead countingAEAD) NonceSize() int { return aead.inner.NonceSize() }
func (aead countingAEAD) Overhead() int  { return aead.inner.Overhead() }
func (aead countingAEAD) Seal(dst, nonce, plaintext, aad []byte) []byte {
	return aead.inner.Seal(dst, nonce, plaintext, aad)
}
func (aead countingAEAD) Open(dst, nonce, ciphertext, aad []byte) ([]byte, error) {
	*aead.opens++
	return aead.inner.Open(dst, nonce, ciphertext, aad)
}

func TestCommitmentIsVerifiedBeforeAEADOpen(t *testing.T) {
	baseEnvelope := testEnvelope(t, testRing(t, testKey(t, "tenant-a")))
	record := mustSeal(t, baseEnvelope, "tenant-a", []byte("secret"), []byte("aad"))

	openCalls := 0
	envelope, err := NewEnvelope(AEADFactoryFunc(func(key []byte) (cipher.AEAD, error) {
		inner, err := aesGCMFactory(key)
		if err != nil {
			return nil, err
		}
		return countingAEAD{inner: inner, opens: &openCalls}, nil
	}), testRing(t, testKey(t, "tenant-a")))
	if err != nil {
		t.Fatalf("NewEnvelope: %v", err)
	}

	tampered := record.Clone()
	tampered.Commit[0] ^= 1
	plaintext, err := envelope.Open(tampered, []byte("aad"))
	if err != ErrOpen || plaintext != nil {
		t.Fatalf("tampered Open = (%q, %v)", plaintext, err)
	}
	if openCalls != 0 {
		t.Fatalf("AEAD.Open called %d times before commitment verification", openCalls)
	}

	plaintext, err = envelope.Open(record, []byte("aad"))
	if err != nil {
		t.Fatalf("valid Open: %v", err)
	}
	if !bytes.Equal(plaintext, []byte("secret")) {
		t.Fatalf("valid plaintext = %q", plaintext)
	}
	if openCalls != 1 {
		t.Fatalf("AEAD.Open called %d times for one valid open", openCalls)
	}
}

type failOpenAEAD struct {
	inner cipher.AEAD
}

func (aead failOpenAEAD) NonceSize() int { return aead.inner.NonceSize() }
func (aead failOpenAEAD) Overhead() int  { return aead.inner.Overhead() }
func (aead failOpenAEAD) Seal(dst, nonce, plaintext, aad []byte) []byte {
	return aead.inner.Seal(dst, nonce, plaintext, aad)
}
func (aead failOpenAEAD) Open(dst, nonce, ciphertext, aad []byte) ([]byte, error) {
	plaintext, _ := aead.inner.Open(dst, nonce, ciphertext, aad)
	return plaintext, errors.New("injected AEAD failure")
}

func TestAEADFailureUsesUniformErrorAndDiscardsPlaintext(t *testing.T) {
	ring := testRing(t, testKey(t, "tenant-a"))
	envelope, err := NewEnvelope(AEADFactoryFunc(func(key []byte) (cipher.AEAD, error) {
		inner, err := aesGCMFactory(key)
		if err != nil {
			return nil, err
		}
		return failOpenAEAD{inner: inner}, nil
	}), ring)
	if err != nil {
		t.Fatalf("NewEnvelope: %v", err)
	}

	record := mustSeal(t, envelope, "tenant-a", []byte("secret"), nil)
	plaintext, err := envelope.Open(record, nil)
	if err != ErrOpen {
		t.Fatalf("Open error = %v, want exactly ErrOpen", err)
	}
	if plaintext != nil {
		t.Fatalf("AEAD failure returned plaintext %q", plaintext)
	}
}
