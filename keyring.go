package netsec

import (
	"sort"
	"sync"
)

const keySize = 32

// Keyring holds AES-256 cookie keys addressed by a monotonically increasing
// KDC key generation. Only the active generation is used to seal or open
// cookies. A generation can be installed ahead of activation, which lets an
// operator stage keys without making partially rolled-out frontends accept
// them early.
type Keyring struct {
	mu sync.RWMutex

	instance         string
	activeGeneration uint64
	maxGeneration    uint64
	keys             map[uint64][keySize]byte
}

// NewKeyring creates a keyring with an initially active generation.
func NewKeyring(instance string, generation uint64, key []byte) (*Keyring, error) {
	if instance == "" || len(instance) > 2048 {
		return nil, ErrInvalidConfiguration
	}
	if generation == 0 {
		return nil, ErrInvalidConfiguration
	}
	if len(key) != keySize {
		return nil, ErrInvalidKey
	}

	var copiedKey [keySize]byte
	copy(copiedKey[:], key)
	return &Keyring{
		instance:         instance,
		activeGeneration: generation,
		maxGeneration:    generation,
		keys:             map[uint64][keySize]byte{generation: copiedKey},
	}, nil
}

// Instance returns the KDC instance bound into the keyring.
func (k *Keyring) Instance() string {
	k.mu.RLock()
	defer k.mu.RUnlock()
	return k.instance
}

// ActiveGeneration returns the generation currently used for cookie sealing
// and opening.
func (k *Keyring) ActiveGeneration() uint64 {
	k.mu.RLock()
	defer k.mu.RUnlock()
	return k.activeGeneration
}

// InstallKey stages a future key generation. It never changes the active
// generation. A generation lower than or equal to the highest installed value
// is rejected as a rollback.
func (k *Keyring) InstallKey(generation uint64, key []byte) error {
	if generation == 0 {
		return ErrInvalidConfiguration
	}
	if len(key) != keySize {
		return ErrInvalidKey
	}

	var copiedKey [keySize]byte
	copy(copiedKey[:], key)

	k.mu.Lock()
	defer k.mu.Unlock()
	if generation <= k.maxGeneration {
		return ErrKeyGenerationRollback
	}
	k.keys[generation] = copiedKey
	k.maxGeneration = generation
	return nil
}

// Activate switches to a previously installed generation. Generations may only
// increase; attempting to activate the current or an older generation returns
// ErrKeyGenerationRollback.
func (k *Keyring) Activate(generation uint64) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if generation <= k.activeGeneration {
		return ErrKeyGenerationRollback
	}
	if _, ok := k.keys[generation]; !ok {
		return ErrKeyNotFound
	}
	k.activeGeneration = generation
	return nil
}

// Rotate installs and immediately activates a new key generation. Existing
// cookies become invalid because their generation no longer matches the active
// frontend generation.
func (k *Keyring) Rotate(generation uint64, key []byte) error {
	if generation == 0 {
		return ErrInvalidConfiguration
	}
	if len(key) != keySize {
		return ErrInvalidKey
	}

	var copiedKey [keySize]byte
	copy(copiedKey[:], key)

	k.mu.Lock()
	defer k.mu.Unlock()
	if generation <= k.maxGeneration || generation <= k.activeGeneration {
		return ErrKeyGenerationRollback
	}
	k.keys[generation] = copiedKey
	k.maxGeneration = generation
	k.activeGeneration = generation
	return nil
}

func (k *Keyring) activeKey() (uint64, [keySize]byte, bool) {
	k.mu.RLock()
	defer k.mu.RUnlock()
	key, ok := k.keys[k.activeGeneration]
	return k.activeGeneration, key, ok
}

// KeyGenerations returns a copy of the installed generations in ascending
// order. It is useful for operational diagnostics and tests, not for request
// handling.
func (k *Keyring) KeyGenerations() []uint64 {
	k.mu.RLock()
	defer k.mu.RUnlock()
	generations := make([]uint64, 0, len(k.keys))
	for generation := range k.keys {
		generations = append(generations, generation)
	}
	sort.Slice(generations, func(i, j int) bool { return generations[i] < generations[j] })
	return generations
}
