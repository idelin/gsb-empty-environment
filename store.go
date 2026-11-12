package netsec

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// BlobStore is the durability boundary for destruction coordination.
//
// Implementations must provide atomic single-key writes and key-prefix
// enumeration. The coordinator never mutates stored objects in place: every
// state transition is expressed as a fresh Put, which keeps crash recovery
// dependent only on the last durable revision.
type BlobStore interface {
	// Get returns the object stored at key. It returns ErrNotFound when the
	// key is absent.
	Get(ctx context.Context, key string) ([]byte, error)
	// Put atomically stores data at key, creating or replacing the object.
	Put(ctx context.Context, key string, data []byte) error
	// Delete removes the object at key. Deleting an absent key is a no-op.
	Delete(ctx context.Context, key string) error
	// List returns every stored key beginning with prefix, in sorted order.
	List(ctx context.Context, prefix string) ([]string, error)
}

// MemoryStore is an in-process BlobStore used as the default store and as the
// durable backing state for fault-injecting test stores.
type MemoryStore struct {
	mu   sync.RWMutex
	data map[string][]byte
}

// NewMemoryStore returns an empty in-memory store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{data: make(map[string][]byte)}
}

// Get implements BlobStore.
func (s *MemoryStore) Get(_ context.Context, key string) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	data, ok := s.data[key]
	if !ok {
		return nil, ErrNotFound
	}
	return append([]byte(nil), data...), nil
}

// Put implements BlobStore.
func (s *MemoryStore) Put(_ context.Context, key string, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[key] = append([]byte(nil), data...)
	return nil
}

// Delete implements BlobStore.
func (s *MemoryStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, key)
	return nil
}

// List implements BlobStore.
func (s *MemoryStore) List(_ context.Context, prefix string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	keys := make([]string, 0, len(s.data))
	for key := range s.data {
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys, nil
}

// Snapshot returns a detached copy of every stored object.
func (s *MemoryStore) Snapshot() map[string][]byte {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string][]byte, len(s.data))
	for key, data := range s.data {
		out[key] = append([]byte(nil), data...)
	}
	return out
}

// Restore replaces the full store contents with snapshot.
func (s *MemoryStore) Restore(snapshot map[string][]byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data = make(map[string][]byte, len(snapshot))
	for key, data := range snapshot {
		s.data[key] = append([]byte(nil), data...)
	}
}

func putJSON(ctx context.Context, store BlobStore, key string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("netsec: encode %s: %w", key, err)
	}
	if err := store.Put(ctx, key, data); err != nil {
		return err
	}
	return nil
}

func getJSON[T any](ctx context.Context, store BlobStore, key string) (*T, error) {
	data, err := store.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	var value T
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, fmt.Errorf("netsec: decode %s: %w", key, err)
	}
	return &value, nil
}

// newID returns a 128-bit random hex identifier.
func newID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("netsec: generate identifier: %w", err)
	}
	return hex.EncodeToString(raw[:]), nil
}

const (
	keyPrefixNames      = "names/"
	keyPrefixVolumes    = "volumes/"
	keyPrefixLeases     = "leases/"
	keyPrefixDestroy    = "destroy/"
	keySuffixDestroyRec = "/record"
	// nameTombstone replaces the generation pointer once destruction
	// completes. A tombstone is distinguishable from both a live name binding
	// and an unused name, so reuse can start a fresh generation.
	nameTombstone = "destroyed"
)

func nameKey(name string) string { return keyPrefixNames + name }

func manifestKey(id VolumeID) string { return keyPrefixVolumes + string(id) }

func leasePrefix(id VolumeID) string { return keyPrefixLeases + string(id) + "/" }

func leaseKey(id VolumeID, leaseID string) string {
	return leasePrefix(id) + leaseID
}

func destructionKey(id VolumeID) string {
	return keyPrefixDestroy + string(id) + keySuffixDestroyRec
}
