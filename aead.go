package netsec

import (
	"crypto/aes"
	"crypto/cipher"
)

// OpenAESGCM authenticates and decrypts ciphertext with a 32-byte message key
// using AES-256-GCM. The ciphertext is the output of SealAESGCM, including the
// 16-byte authentication tag.
func OpenAESGCM(messageKey, nonce, ciphertext, additionalData []byte) ([]byte, error) {
	if len(messageKey) != MessageKeySize {
		return nil, ErrInvalidMessageKey
	}
	if len(nonce) != 12 {
		return nil, ErrInvalidNonce
	}

	block, err := aes.NewCipher(messageKey)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(ciphertext) < aead.Overhead() {
		return nil, ErrInvalidCiphertext
	}

	return aead.Open(nil, nonce, ciphertext, additionalData)
}

// SealAESGCM encrypts plaintext with a 32-byte message key using AES-256-GCM.
func SealAESGCM(messageKey, nonce, plaintext, additionalData []byte) ([]byte, error) {
	if len(messageKey) != MessageKeySize {
		return nil, ErrInvalidMessageKey
	}
	if len(nonce) != 12 {
		return nil, ErrInvalidNonce
	}

	block, err := aes.NewCipher(messageKey)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return aead.Seal(nil, nonce, plaintext, additionalData), nil
}
