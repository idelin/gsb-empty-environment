package tracevault

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
)

// deriveKey derives a 32-byte sub-key from the master key using HMAC-SHA256
// over a domain-separation label and length-prefixed parts.
func deriveKey(master []byte, label string, parts ...string) []byte {
	mac := hmac.New(sha256.New, master)
	mac.Write([]byte(label))
	var lenBuf [4]byte
	for _, p := range parts {
		binary.BigEndian.PutUint32(lenBuf[:], uint32(len(p)))
		mac.Write(lenBuf[:])
		mac.Write([]byte(p))
	}
	return mac.Sum(nil)
}

// encryptAESGCM encrypts plaintext with AES-256-GCM, prepending a random nonce.
func encryptAESGCM(key, plaintext, aad []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plaintext, aad), nil
}

// decryptAESGCM reverses encryptAESGCM.
func decryptAESGCM(key, ciphertext, aad []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(ciphertext) < gcm.NonceSize() {
		return nil, errors.New("tracevault: ciphertext too short")
	}
	nonce, ct := ciphertext[:gcm.NonceSize()], ciphertext[gcm.NonceSize():]
	return gcm.Open(nil, nonce, ct, aad)
}
