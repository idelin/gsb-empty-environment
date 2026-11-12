package netsec

import (
	"crypto/aes"
	stdcipher "crypto/cipher"
	"fmt"
)

const (
	payloadKeySize   = 32
	noncePrefixSize  = 4
	nonceCounterSize = 8
	nonceSize        = noncePrefixSize + nonceCounterSize
)

// Cipher is the injectable payload encryption primitive. The default
// implementation is AES-256-GCM with a 12-byte nonce. Implementations used by
// a Decryptor must be safe for concurrent use.
//
// Encrypt must authenticate additionalData and return ciphertext including the
// authentication tag. Decrypt must not return plaintext unless that tag is
// valid.
type Cipher interface {
	Encrypt(key, nonce, plaintext, additionalData []byte) ([]byte, error)
	Decrypt(key, nonce, ciphertext, additionalData []byte) ([]byte, error)
}

type aesGCMCipher struct{}

// NewAESGCM returns an AES-256-GCM payload Cipher.
func NewAESGCM() Cipher {
	return aesGCMCipher{}
}

func (aesGCMCipher) Encrypt(key, nonce, plaintext, additionalData []byte) ([]byte, error) {
	gcm, err := newGCM(key, nonce)
	if err != nil {
		return nil, err
	}
	return gcm.Seal(nil, nonce, plaintext, additionalData), nil
}

func (aesGCMCipher) Decrypt(key, nonce, ciphertext, additionalData []byte) ([]byte, error) {
	gcm, err := newGCM(key, nonce)
	if err != nil {
		return nil, err
	}
	plaintext, err := gcm.Open(nil, nonce, ciphertext, additionalData)
	if err != nil {
		return nil, fmt.Errorf("open AES-GCM: %w", err)
	}
	return plaintext, nil
}

func newGCM(key, nonce []byte) (stdcipher.AEAD, error) {
	if len(key) != payloadKeySize {
		return nil, fmt.Errorf("%w: data key must be %d bytes", ErrInvalidMessage, payloadKeySize)
	}
	if len(nonce) != nonceSize {
		return nil, fmt.Errorf("%w: nonce must be %d bytes", ErrInvalidMessage, nonceSize)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create AES block: %w", err)
	}
	gcm, err := stdcipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create AES-GCM: %w", err)
	}
	return gcm, nil
}
