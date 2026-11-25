package namecrypt

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
)

// Hasher turns a canonical name into the on-disk lookup key. The default
// implementation is the first LookupKeySize bytes of
// HMAC-SHA256(nameKey, "lookup"||canonical). Tests may substitute a
// collision-prone implementation to exercise bucket coexistence.
type Hasher interface {
	LookupKey(nameKey []byte, canonical string) [LookupKeySize]byte
}

// HMACHasher is the production Hasher.
type HMACHasher struct{}

// LookupKey implements Hasher using keyed HMAC-SHA256 truncated to
// LookupKeySize bytes.
func (HMACHasher) LookupKey(nameKey []byte, canonical string) [LookupKeySize]byte {
	mac := hmac.New(sha256.New, nameKey)
	mac.Write([]byte("lookup"))
	mac.Write([]byte(canonical))
	sum := mac.Sum(nil)
	var key [LookupKeySize]byte
	copy(key[:], sum[:LookupKeySize])
	return key
}

func putUint64(dst []byte, v uint64) {
	binary.BigEndian.PutUint64(dst, v)
}

// policyDigest binds every immutable policy field plus the generation whose
// key sealed an entry. It is part of the AEAD associated data, so changing a
// policy field or reusing a ciphertext under another generation fails
// authentication.
func policyDigest(p Policy, generation uint64) [sha256.Size]byte {
	h := sha256.New()
	putLenPrefixed(h, []byte(p.DirectoryID))
	var buf [8]byte
	putUint64(buf[:], generation)
	h.Write(buf[:])
	h.Write(p.DirectoryNonce[:])
	h.Write([]byte{byte(p.CaseMode)})
	putUint64(buf[:], uint64(p.MaxRawNameBytes))
	h.Write(buf[:])
	putUint64(buf[:], uint64(p.MaxCanonicalBytes))
	h.Write(buf[:])
	var out [sha256.Size]byte
	copy(out[:], h.Sum(nil))
	return out
}

func putLenPrefixed(h interface{ Write([]byte) (int, error) }, b []byte) {
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(b)))
	h.Write(lenBuf[:])
	h.Write(b)
}

// DeriveNameKey computes
// HMAC-SHA256(masterHandle, "name"||directoryID||keyGeneration||directoryNonce).
func DeriveNameKey(masterHandle []byte, p Policy, generation uint64) []byte {
	mac := hmac.New(sha256.New, masterHandle)
	mac.Write([]byte("name"))
	putLenPrefixed(mac, []byte(p.DirectoryID))
	var buf [8]byte
	putUint64(buf[:], generation)
	mac.Write(buf[:])
	mac.Write(p.DirectoryNonce[:])
	return mac.Sum(nil)
}

func newGCM(nameKey []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(nameKey)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCMWithNonceSize(block, EntryNonceSize)
}

// associatedData binds the policy digest, canonical-name hash and the full
// inode identity (id plus generation).
func associatedData(p Policy, generation uint64, canonical string, inode InodeRef) []byte {
	digest := policyDigest(p, generation)
	canonicalHash := sha256.Sum256([]byte(canonical))
	var aad bytes.Buffer
	aad.Write(digest[:])
	aad.Write(canonicalHash[:])
	putLenPrefixed(&aad, []byte(inode.ID))
	var buf [8]byte
	putUint64(buf[:], inode.Generation)
	aad.Write(buf[:])
	return aad.Bytes()
}

func sealName(nameKey []byte, p Policy, generation uint64, canonical string, inode InodeRef, raw string) ([]byte, []byte, error) {
	gcm, err := newGCM(nameKey)
	if err != nil {
		return nil, nil, err
	}
	entryNonce := make([]byte, EntryNonceSize)
	if _, err := rand.Read(entryNonce); err != nil {
		return nil, nil, err
	}
	aad := associatedData(p, generation, canonical, inode)
	ciphertext := gcm.Seal(nil, entryNonce, []byte(raw), aad)
	return entryNonce, ciphertext, nil
}

func openName(nameKey []byte, p Policy, generation uint64, canonical string, inode InodeRef, entryNonce, ciphertext []byte) (string, error) {
	gcm, err := newGCM(nameKey)
	if err != nil {
		return "", err
	}
	if len(entryNonce) != EntryNonceSize {
		return "", fmt.Errorf("%w: bad entry nonce length", ErrBadRecord)
	}
	aad := associatedData(p, generation, canonical, inode)
	plaintext, err := gcm.Open(nil, entryNonce, ciphertext, aad)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrBadRecord, err)
	}
	return string(plaintext), nil
}
