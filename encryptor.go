package netsec

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
)

// Encryptor seals and opens block payloads with the key of a given generation.
// Implementations must be safe for concurrent use.
type Encryptor interface {
	// Encrypt seals plaintext with the key of gen and returns the ciphertext.
	Encrypt(ctx context.Context, gen KeyGen, plaintext []byte) ([]byte, error)
	// Decrypt opens ciphertext produced under gen and returns the plaintext.
	Decrypt(ctx context.Context, gen KeyGen, ciphertext []byte) ([]byte, error)
}

// AESGCMEncryptor is an Encryptor backed by AES-256-GCM. Each encryption uses
// a fresh random nonce prepended to the GCM output. Key material is held in
// memory and can be explicitly removed once a generation has been destroyed.
type AESGCMEncryptor struct {
	keys map[KeyGen][]byte
	aead map[KeyGen]cipher.AEAD
}

// NewAESGCMEncryptor builds an encryptor provisioned with gen 0 key material.
// Additional generations are introduced with Rotate.
func NewAESGCMEncryptor(initialKey []byte) (*AESGCMEncryptor, error) {
	e := &AESGCMEncryptor{
		keys: make(map[KeyGen][]byte),
		aead: make(map[KeyGen]cipher.AEAD),
	}
	if err := e.add(0, initialKey); err != nil {
		return nil, err
	}
	return e, nil
}

// Rotate provisions key material for a new generation.
func (e *AESGCMEncryptor) Rotate(gen KeyGen, key []byte) error {
	return e.add(gen, key)
}

func (e *AESGCMEncryptor) add(gen KeyGen, key []byte) error {
	if len(key) != 32 {
		return fmt.Errorf("netsec: AES-256 key must be 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	clone := append([]byte(nil), key...)
	e.keys[gen] = clone
	e.aead[gen] = aead
	return nil
}

// RemoveKey zeroizes and drops the key material for gen. After this call the
// generation can no longer be opened or sealed on this host.
func (e *AESGCMEncryptor) RemoveKey(gen KeyGen) {
	if key, ok := e.keys[gen]; ok {
		for i := range key {
			key[i] = 0
		}
	}
	delete(e.keys, gen)
	delete(e.aead, gen)
}

// HasKey reports whether key material for gen is still resident.
func (e *AESGCMEncryptor) HasKey(gen KeyGen) bool {
	_, ok := e.aead[gen]
	return ok
}

func (e *AESGCMEncryptor) Encrypt(ctx context.Context, gen KeyGen, plaintext []byte) ([]byte, error) {
	aead, ok := e.aead[gen]
	if !ok {
		return nil, fmt.Errorf("%w: gen %d", ErrKeyDestroyed, gen)
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	sealed := aead.Seal(nonce, nonce, plaintext, nil)
	return sealed, nil
}

func (e *AESGCMEncryptor) Decrypt(ctx context.Context, gen KeyGen, payload []byte) ([]byte, error) {
	aead, ok := e.aead[gen]
	if !ok {
		return nil, fmt.Errorf("%w: gen %d", ErrKeyDestroyed, gen)
	}
	ns := aead.NonceSize()
	if len(payload) < ns {
		return nil, fmt.Errorf("netsec: ciphertext shorter than nonce")
	}
	nonce, body := payload[:ns], payload[ns:]
	return aead.Open(nil, nonce, body, nil)
}
