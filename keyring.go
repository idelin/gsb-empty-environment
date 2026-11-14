package netsec

import (
	"context"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"sort"
	"sync"
)

// KeyProvider supplies generation-scoped key material. Each key must be kept
// outside the team-readable workspace; production deployments should wrap a
// KMS or secret manager rather than a workspace file.
type KeyProvider interface {
	// CurrentGeneration returns the generation used for new writes.
	CurrentGeneration(ctx context.Context) (uint64, error)
	// Key returns the secret for generation. The material must have at least
	// 256 bits of entropy.
	Key(ctx context.Context, generation uint64) ([]byte, error)
}

// RotatingKeyProvider creates a new active generation on demand. The previous
// generation must remain available until all artifacts have been re-encrypted.
type RotatingKeyProvider interface {
	KeyProvider
	Rotate(ctx context.Context) (uint64, error)
}

// GenerationSelector is an optional KeyProvider capability. RotateTo uses it
// to make an externally created generation active for subsequent writes.
type GenerationSelector interface {
	SetCurrent(generation uint64) error
}

// MemoryKeyring is an in-memory KeyProvider intended for tests and for
// services that load key material from a process-level secret source. It never
// writes key material to disk.
type MemoryKeyring struct {
	mu          sync.RWMutex
	keys        map[uint64][]byte
	current     uint64
	rand        io.Reader
	initialized bool
}

// NewMemoryKeyring derives generation 1 from secret. The same high-entropy
// secret must be supplied after a restart or the state cannot be read.
func NewMemoryKeyring(secret []byte) (*MemoryKeyring, error) {
	return NewMemoryKeyringWithReader(secret, rand.Reader)
}

// NewMemoryKeyringWithReader is NewMemoryKeyring with an injectable random
// source for deterministic tests.
func NewMemoryKeyringWithReader(secret []byte, reader io.Reader) (*MemoryKeyring, error) {
	if len(secret) == 0 {
		return nil, fmt.Errorf("%w: empty key secret", ErrInvalidConfig)
	}
	if reader == nil {
		reader = rand.Reader
	}
	derived, err := hkdf.Key(sha256.New, secret, nil, "netsec/state/v1/master-key", 32)
	if err != nil {
		return nil, err
	}
	k := &MemoryKeyring{
		keys:    make(map[uint64][]byte),
		current: 1,
		rand:    reader,
	}
	k.keys[1] = derived
	k.initialized = true
	return k, nil
}

// AddGeneration installs a caller-supplied key for generation. This supports a
// KMS-backed key loader in addition to local rotation.
func (k *MemoryKeyring) AddGeneration(generation uint64, material []byte) error {
	if generation == 0 {
		return fmt.Errorf("%w: generation 0 is reserved", ErrKeyGeneration)
	}
	if len(material) == 0 {
		return fmt.Errorf("%w: empty key material", ErrKeyGeneration)
	}
	derived, err := hkdf.Key(sha256.New, material, nil, fmt.Sprintf("netsec/state/v1/generation/%d", generation), 32)
	if err != nil {
		return err
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if !k.initialized {
		k.initialized = true
	}
	k.keys[generation] = derived
	if k.current == 0 {
		k.current = generation
	}
	return nil
}

// SetCurrent selects the active generation.
func (k *MemoryKeyring) SetCurrent(generation uint64) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if _, ok := k.keys[generation]; !ok {
		return fmt.Errorf("%w: %d", ErrKeyGeneration, generation)
	}
	k.current = generation
	return nil
}

// CurrentGeneration implements KeyProvider.
func (k *MemoryKeyring) CurrentGeneration(context.Context) (uint64, error) {
	k.mu.RLock()
	defer k.mu.RUnlock()
	if !k.initialized || k.current == 0 {
		return 0, ErrKeyGeneration
	}
	return k.current, nil
}

// Key implements KeyProvider. A copy is returned so callers cannot mutate the
// keyring's stored key material.
func (k *MemoryKeyring) Key(_ context.Context, generation uint64) ([]byte, error) {
	k.mu.RLock()
	defer k.mu.RUnlock()
	material, ok := k.keys[generation]
	if !ok {
		return nil, fmt.Errorf("%w: %d", ErrKeyGeneration, generation)
	}
	out := make([]byte, len(material))
	copy(out, material)
	return out, nil
}

// Rotate implements RotatingKeyProvider. It creates a fresh 256-bit key in a
// new generation and makes that generation active. The generated key exists
// only in memory; production deployments must use a provider that persists
// generations in a KMS or secret manager.
func (k *MemoryKeyring) Rotate(context.Context) (uint64, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if !k.initialized || k.current == 0 {
		return 0, ErrKeyGeneration
	}
	if k.rand == nil {
		k.rand = rand.Reader
	}
	material := make([]byte, 32)
	if _, err := io.ReadFull(k.rand, material); err != nil {
		return 0, fmt.Errorf("netsec: generate key: %w", err)
	}
	next := k.current + 1
	k.keys[next] = material
	k.current = next
	return next, nil
}

// RemoveGeneration drops key material. Call only after all artifacts have
// been re-encrypted under a newer generation; otherwise reads fail closed.
func (k *MemoryKeyring) RemoveGeneration(generation uint64) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if generation == k.current {
		return fmt.Errorf("%w: cannot remove current generation %d", ErrKeyGeneration, generation)
	}
	delete(k.keys, generation)
	return nil
}

// PruneBefore removes every generation older than generation and returns the
// generations that were removed.
func (k *MemoryKeyring) PruneBefore(generation uint64) []uint64 {
	k.mu.Lock()
	defer k.mu.Unlock()
	var removed []uint64
	for generationKey := range k.keys {
		if generationKey < generation && generationKey != k.current {
			delete(k.keys, generationKey)
			removed = append(removed, generationKey)
		}
	}
	sort.Slice(removed, func(i, j int) bool { return removed[i] < removed[j] })
	return removed
}

// Generations returns the installed generations in ascending order.
func (k *MemoryKeyring) Generations() []uint64 {
	k.mu.RLock()
	defer k.mu.RUnlock()
	out := make([]uint64, 0, len(k.keys))
	for generation := range k.keys {
		out = append(out, generation)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// SecretFingerprint returns a short, non-reversible identifier for a key
// generation. It is useful for audit logs and never reveals key material.
func (k *MemoryKeyring) SecretFingerprint(generation uint64) (string, error) {
	material, err := k.Key(context.Background(), generation)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(append([]byte("netsec/state/v1/fingerprint:"), material...))
	return hex.EncodeToString(sum[:8]), nil
}
