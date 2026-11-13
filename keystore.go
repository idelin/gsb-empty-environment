package netsec

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"sync"
)

// KeyHandle is an opaque reference to key material held inside a KeyStore.
// Callers never see raw key bytes; all operations happen inside the store,
// modelling an HSM-style root key handle.
type KeyHandle uint64

const rootKeyLen = 16

// DerivationInputs is the full input tuple of a session key derivation.
// Any successful session must have a unique tuple; the joinEpoch component
// keeps tuples unique even when an attacker replays an old JoinAccept
// against a rolled-back terminal.
type DerivationInputs struct {
	Label     string
	JoinEUI   [8]byte
	DevEUI    [8]byte
	JoinNonce uint32
	DevNonce  uint16
	SessionID [16]byte
	JoinEpoch uint64
}

// Bytes returns the canonical encoding of the derivation inputs.
func (d DerivationInputs) Bytes() []byte {
	out := make([]byte, 0, len(d.Label)+8+8+4+2+16+8)
	out = append(out, d.Label...)
	out = append(out, d.JoinEUI[:]...)
	out = append(out, d.DevEUI[:]...)
	var tmp [8]byte
	binary.LittleEndian.PutUint32(tmp[:4], d.JoinNonce)
	out = append(out, tmp[:4]...)
	binary.LittleEndian.PutUint16(tmp[:2], d.DevNonce)
	out = append(out, tmp[:2]...)
	out = append(out, d.SessionID[:]...)
	binary.LittleEndian.PutUint64(tmp[:8], d.JoinEpoch)
	out = append(out, tmp[:8]...)
	return out
}

// KeyStore holds key material and performs all cryptographic operations
// on behalf of callers, which only ever see KeyHandles.
type KeyStore struct {
	mu   sync.Mutex
	next KeyHandle
	keys map[KeyHandle][]byte

	// OnDerive, when set, is invoked with the inputs of every session key
	// derivation. Tests use it to assert derivation inputs never repeat.
	OnDerive func(DerivationInputs)
}

func NewKeyStore() *KeyStore {
	return &KeyStore{next: 1, keys: make(map[KeyHandle][]byte)}
}

// ImportRootKey stores a 128-bit root key and returns its handle.
func (ks *KeyStore) ImportRootKey(key []byte) (KeyHandle, error) {
	if len(key) != rootKeyLen {
		return 0, ErrInvalidKey
	}
	ks.mu.Lock()
	defer ks.mu.Unlock()
	h := ks.next
	ks.next++
	ks.keys[h] = append([]byte(nil), key...)
	return h, nil
}

func (ks *KeyStore) key(h KeyHandle) ([]byte, error) {
	ks.mu.Lock()
	defer ks.mu.Unlock()
	k, ok := ks.keys[h]
	if !ok {
		return nil, ErrUnknownKeyHandle
	}
	return k, nil
}

// KeyBytes exposes a copy of the key bytes for test assertions only.
func (ks *KeyStore) KeyBytes(h KeyHandle) ([]byte, error) {
	k, err := ks.key(h)
	if err != nil {
		return nil, err
	}
	return append([]byte(nil), k...), nil
}

// MAC computes a 4-byte truncated HMAC-SHA-256 over data.
func (ks *KeyStore) MAC(h KeyHandle, data []byte) ([4]byte, error) {
	var out [4]byte
	k, err := ks.key(h)
	if err != nil {
		return out, err
	}
	m := hmac.New(sha256.New, k)
	m.Write(data)
	copy(out[:], m.Sum(nil))
	return out, nil
}

// MICCallback returns a callback computing the 4-byte MIC of canonical
// message bytes under the given handle.
func (ks *KeyStore) MICCallback(h KeyHandle) MICCallback {
	return func(canonical []byte) ([4]byte, error) {
		return ks.MAC(h, canonical)
	}
}

// Encrypt seals plaintext with AES-128-CTR under a random IV, returned as
// IV || ciphertext. Authentication is provided separately by a MIC
// callback over the ciphertext (encrypt-then-MAC).
func (ks *KeyStore) Encrypt(h KeyHandle, plaintext []byte) ([]byte, error) {
	k, err := ks.key(h)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(k)
	if err != nil {
		return nil, err
	}
	out := make([]byte, aes.BlockSize+len(plaintext))
	if _, err := rand.Read(out[:aes.BlockSize]); err != nil {
		return nil, err
	}
	cipher.NewCTR(block, out[:aes.BlockSize]).XORKeyStream(out[aes.BlockSize:], plaintext)
	return out, nil
}

// Decrypt opens a ciphertext produced by Encrypt.
func (ks *KeyStore) Decrypt(h KeyHandle, ct []byte) ([]byte, error) {
	k, err := ks.key(h)
	if err != nil {
		return nil, err
	}
	if len(ct) < aes.BlockSize {
		return nil, ErrMalformedMessage
	}
	block, err := aes.NewCipher(k)
	if err != nil {
		return nil, err
	}
	out := make([]byte, len(ct)-aes.BlockSize)
	cipher.NewCTR(block, ct[:aes.BlockSize]).XORKeyStream(out, ct[aes.BlockSize:])
	return out, nil
}

// DeriveSessionKey derives a 128-bit session key from the root key and the
// derivation inputs, stores it, and returns its handle.
func (ks *KeyStore) DeriveSessionKey(h KeyHandle, in DerivationInputs) (KeyHandle, error) {
	k, err := ks.key(h)
	if err != nil {
		return 0, err
	}
	m := hmac.New(sha256.New, k)
	m.Write(in.Bytes())
	derived := m.Sum(nil)[:rootKeyLen]

	ks.mu.Lock()
	nh := ks.next
	ks.next++
	ks.keys[nh] = derived
	hook := ks.OnDerive
	ks.mu.Unlock()

	if hook != nil {
		hook(in)
	}
	return nh, nil
}
