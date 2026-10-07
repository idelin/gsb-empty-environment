package encobj

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/subtle"
	"sync"
)

// keyRegistry holds the caller-supplied key ID -> key material mapping.
// The mapping is immutable: a key ID can never be rebound to different
// material, and the same material can never be registered under a second
// ID. Keys live only in memory; nothing key-related is ever written to the
// object directory or logs.
type keyRegistry struct {
	mu       sync.Mutex
	byID     map[string]*keyEntry
	activeID string
}

type keyEntry struct {
	id      string
	key     []byte
	aead    cipher.AEAD
	retired bool
	leases  int
}

func newKeyRegistry() *keyRegistry {
	return &keyRegistry{byID: make(map[string]*keyEntry)}
}

// RegisterKey adds key material under keyID. Registering the same ID with
// identical material is a no-op; different material under an existing ID, or
// existing material under a new ID, is rejected.
func (r *keyRegistry) RegisterKey(keyID string, key []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e, ok := r.byID[keyID]; ok {
		if subtle.ConstantTimeCompare(e.key, key) == 1 {
			return nil
		}
		return ErrKeyExists
	}
	for _, e := range r.byID {
		if bytes.Equal(e.key, key) {
			return ErrKeyMaterialReused
		}
	}
	aead, err := newAEAD(key)
	if err != nil {
		return err
	}
	kc := make([]byte, len(key))
	copy(kc, key)
	r.byID[keyID] = &keyEntry{id: keyID, key: kc, aead: aead}
	return nil
}

// SetActiveKey publishes keyID as the key for all new commits. The key must
// already be registered.
func (r *keyRegistry) SetActiveKey(keyID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.byID[keyID]
	if !ok {
		return ErrKeyNotFound
	}
	if e.retired {
		return ErrKeyRetired
	}
	r.activeID = keyID
	return nil
}

func (r *keyRegistry) active() (string, *keyEntry, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.activeID == "" {
		return "", nil, ErrKeyNotFound
	}
	e := r.byID[r.activeID]
	if e == nil || e.retired {
		return "", nil, ErrKeyRetired
	}
	return r.activeID, e, nil
}

func (r *keyRegistry) lookup(keyID string) (*keyEntry, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.byID[keyID]
	if !ok {
		return nil, ErrKeyNotFound
	}
	if e.retired {
		return nil, ErrKeyRetired
	}
	return e, nil
}

// acquire leases a set of key IDs (deduplicated). The returned release
// function must be called exactly once.
func (r *keyRegistry) acquire(keyIDs []string) (func(), error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	seen := map[string]bool{}
	var ids []string
	for _, id := range keyIDs {
		if seen[id] {
			continue
		}
		seen[id] = true
		e, ok := r.byID[id]
		if !ok {
			return nil, ErrKeyNotFound
		}
		if e.retired {
			return nil, ErrKeyRetired
		}
		ids = append(ids, id)
	}
	for _, id := range ids {
		r.byID[id].leases++
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			r.mu.Lock()
			defer r.mu.Unlock()
			for _, id := range ids {
				r.byID[id].leases--
			}
		})
	}, nil
}

// Retire removes keyID from service. It fails while the key is active or
// while any snapshot still leases it. After retirement the key can no
// longer encrypt or decrypt.
func (r *keyRegistry) Retire(keyID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.byID[keyID]
	if !ok {
		return ErrKeyNotFound
	}
	if r.activeID == keyID || e.leases > 0 {
		return ErrKeyInUse
	}
	e.retired = true
	// Best-effort scrub of key material.
	for i := range e.key {
		e.key[i] = 0
	}
	return nil
}

func newAEAD(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
