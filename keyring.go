package netsec

import (
	"crypto/rand"
	"fmt"
	"sync"
)

const ticketKeySize = 32

// Keyring holds exactly one active ticket-encryption key. Rotation replaces
// the key and advances the generation; tickets sealed under older generations
// are rejected rather than decrypted with a retired key.
type Keyring struct {
	mu         sync.RWMutex
	generation uint64
	key        [ticketKeySize]byte
}

// NewKeyring creates a keyring with generation 1.
func NewKeyring(key []byte) (*Keyring, error) {
	if len(key) != ticketKeySize {
		return nil, fmt.Errorf("%w: ticket key must be %d bytes", ErrInvalidArgument, ticketKeySize)
	}
	k := &Keyring{generation: 1}
	copy(k.key[:], key)
	return k, nil
}

// NewRandomKeyring creates a keyring from crypto/rand.
func NewRandomKeyring() (*Keyring, error) {
	var key [ticketKeySize]byte
	if _, err := rand.Read(key[:]); err != nil {
		return nil, fmt.Errorf("netsec: generate ticket key: %w", err)
	}
	return NewKeyring(key[:])
}

// Rotate installs a new active key and advances the generation. The old key is
// overwritten before the new key is installed. A rotation invalidates every
// outstanding ticket.
func (k *Keyring) Rotate(key []byte) (uint64, error) {
	if k == nil {
		return 0, fmt.Errorf("%w: nil keyring", ErrInvalidArgument)
	}
	if len(key) != ticketKeySize {
		return 0, fmt.Errorf("%w: ticket key must be %d bytes", ErrInvalidArgument, ticketKeySize)
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.generation == ^uint64(0) {
		return 0, fmt.Errorf("%w: ticket key generation exhausted", ErrInvalidArgument)
	}
	for i := range k.key {
		k.key[i] = 0
	}
	copy(k.key[:], key)
	k.generation++
	return k.generation, nil
}

// Generation returns the active key generation.
func (k *Keyring) Generation() uint64 {
	if k == nil {
		return 0
	}
	k.mu.RLock()
	defer k.mu.RUnlock()
	return k.generation
}

func (k *Keyring) snapshot() (uint64, [ticketKeySize]byte) {
	k.mu.RLock()
	defer k.mu.RUnlock()
	return k.generation, k.key
}
