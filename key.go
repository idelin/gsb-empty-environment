package netsec

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"fmt"
	"sync"
)

// Round claims are process-wide. Round numbers must never be reused with the
// same raw AES key, including through separate Key handles.
var roundClaims = struct {
	sync.Mutex
	byKey map[[sha256.Size]byte]map[uint32]struct{}
}{
	byKey: make(map[[sha256.Size]byte]map[uint32]struct{}),
}

// Key is an AES-GCM key plus the round-reuse guard. Construct it once for each
// raw key and share the handle between encoders and decoders.
type Key struct {
	id  [sha256.Size]byte
	mu  sync.Mutex
	gcm cipher.AEAD
}

// NewKey constructs an AES-GCM key from 16, 24, or 32 raw key bytes.
func NewKey(material []byte) (*Key, error) {
	switch len(material) {
	case 16, 24, 32:
	default:
		return nil, fmt.Errorf("%w: AES key length %d", ErrInvalidKey, len(material))
	}
	material = append([]byte(nil), material...)

	block, err := aes.NewCipher(material)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidKey, err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidKey, err)
	}
	if gcm.NonceSize() != NonceSize {
		return nil, fmt.Errorf("%w: GCM nonce size %d", ErrInvalidKey, gcm.NonceSize())
	}

	return &Key{
		id:  sha256.Sum256(material),
		gcm: gcm,
	}, nil
}

func (k *Key) claimRound(round uint32) error {
	if k == nil || k.gcm == nil {
		return fmt.Errorf("%w: nil key", ErrInvalidKey)
	}

	roundClaims.Lock()
	defer roundClaims.Unlock()

	rounds := roundClaims.byKey[k.id]
	if rounds == nil {
		rounds = make(map[uint32]struct{})
		roundClaims.byKey[k.id] = rounds
	}
	if _, exists := rounds[round]; exists {
		return fmt.Errorf("%w: round %d", ErrRoundReuse, round)
	}
	rounds[round] = struct{}{}
	return nil
}

func (k *Key) seal(nonce, plaintext, aad []byte) ([]byte, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.gcm.Seal(nil, nonce, plaintext, aad), nil
}

func (k *Key) open(nonce, ciphertext, aad []byte) ([]byte, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.gcm.Open(nil, nonce, ciphertext, aad)
}
