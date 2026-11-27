package noise

import (
	"crypto/cipher"
	"encoding/binary"
	"fmt"
	"math"
)

// CipherState encrypts and decrypts messages with a 64-bit monotonically
// increasing nonce. A zero-key state passes plaintext through unchanged and is
// used before the first MixKey in a handshake pattern.
type CipherState struct {
	key       [keySize]byte
	aead      cipher.AEAD
	nonce     uint64
	hasKey    bool
	destroyed bool
}

// NewCipherState creates a CipherState. Passing nil creates a state without a
// key. A non-nil key must be exactly 32 bytes.
func NewCipherState(key []byte) (*CipherState, error) {
	c := &CipherState{}
	if key != nil {
		if err := c.InitializeKey(key); err != nil {
			return nil, err
		}
	}
	return c, nil
}

// InitializeKey installs a 32-byte key and resets the nonce to zero.
func (c *CipherState) InitializeKey(key []byte) error {
	if c == nil || c.destroyed {
		return ErrStateDestroyed
	}
	if len(key) != keySize {
		return fmt.Errorf("%w: cipher key must be %d bytes", ErrInvalidKey, keySize)
	}
	aead, err := NewChaCha20Poly1305(key)
	if err != nil {
		return err
	}
	destroyAEAD(c.aead)
	copy(c.key[:], key)
	c.aead = aead
	c.nonce = 0
	c.hasKey = true
	return nil
}

// HasKey reports whether the state has a non-empty key.
func (c *CipherState) HasKey() bool {
	return c != nil && c.hasKey && !c.destroyed
}

// Nonce returns the next nonce that will be used by an encrypted operation.
func (c *CipherState) Nonce() uint64 {
	if c == nil {
		return 0
	}
	return c.nonce
}

// EncryptWithAd encrypts plaintext under additionalData. When no key is set,
// plaintext is returned unchanged. The nonce value MaxUint64 is never used for
// normal encryption; reaching it causes a hard rejection before any output.
func (c *CipherState) EncryptWithAd(additionalData, plaintext []byte) ([]byte, error) {
	if c == nil || c.destroyed {
		return nil, ErrStateDestroyed
	}
	if !c.hasKey {
		return append([]byte(nil), plaintext...), nil
	}
	if c.nonce == math.MaxUint64 {
		return nil, ErrNonceExhausted
	}
	nonce := nonceBytes(c.nonce)
	out := c.aead.Seal(nil, nonce[:], plaintext, additionalData)
	c.nonce++
	return out, nil
}

// DecryptWithAd authenticates and decrypts ciphertext under additionalData.
// When no key is set, ciphertext is returned unchanged.
func (c *CipherState) DecryptWithAd(additionalData, ciphertext []byte) ([]byte, error) {
	if c == nil || c.destroyed {
		return nil, ErrStateDestroyed
	}
	if !c.hasKey {
		return append([]byte(nil), ciphertext...), nil
	}
	if c.nonce == math.MaxUint64 {
		return nil, ErrNonceExhausted
	}
	nonce := nonceBytes(c.nonce)
	out, err := c.aead.Open(nil, nonce[:], ciphertext, additionalData)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrAuthenticationFailed, err)
	}
	c.nonce++
	return out, nil
}

// Rekey replaces the key with the first 32 bytes of ChaCha20-Poly1305
// encryption of 32 zero bytes under the reserved nonce 2^64-1, then resets the
// message nonce to zero.
func (c *CipherState) Rekey() error {
	if c == nil || c.destroyed {
		return ErrStateDestroyed
	}
	if !c.hasKey {
		return ErrInvalidConfig
	}
	nonce := nonceBytes(math.MaxUint64)
	zeroPlaintext := make([]byte, keySize)
	sealed := c.aead.Seal(nil, nonce[:], zeroPlaintext, nil)
	if len(sealed) < keySize {
		zero(sealed)
		return ErrInvalidConfig
	}
	aead, err := NewChaCha20Poly1305(sealed[:keySize])
	if err != nil {
		zero(sealed)
		return err
	}
	destroyAEAD(c.aead)
	copy(c.key[:], sealed[:keySize])
	c.aead = aead
	c.nonce = 0
	zero(zeroPlaintext)
	zero(sealed)
	return nil
}

// Destroy clears key material and disables the state.
func (c *CipherState) Destroy() {
	if c == nil {
		return
	}
	destroyAEAD(c.aead)
	zero(c.key[:])
	c.aead = nil
	c.nonce = 0
	c.hasKey = false
	c.destroyed = true
}

func destroyAEAD(aead cipher.AEAD) {
	if d, ok := aead.(interface{ Destroy() }); ok {
		d.Destroy()
	}
}

func nonceBytes(n uint64) [nonceSize]byte {
	var nonce [nonceSize]byte
	binary.LittleEndian.PutUint64(nonce[4:], n)
	return nonce
}
