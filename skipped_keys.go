package netsec

import (
	"bytes"
	"encoding/hex"
	"sort"
	"strconv"
	"sync"
)

// SkippedKeyID identifies a retained message key. Retained keys are
// namespaced by the ratchet public key and the message sequence number so
// independent ratchet chains can never consume each other's keys.
type SkippedKeyID struct {
	RatchetPublicKey []byte
	Sequence         uint64
}

// NewSkippedKeyID builds a SkippedKeyID and copies the public key.
func NewSkippedKeyID(ratchetPublicKey []byte, sequence uint64) SkippedKeyID {
	return SkippedKeyID{
		RatchetPublicKey: cloneBytes(ratchetPublicKey),
		Sequence:         sequence,
	}
}

// String returns a stable, human-readable representation of the ID.
func (id SkippedKeyID) String() string {
	return hex.EncodeToString(id.RatchetPublicKey) + ":" + strconv.FormatUint(id.Sequence, 10)
}

type skippedMapKey struct {
	ratchetPublicKey string
	sequence         uint64
}

func newSkippedMapKey(ratchetPublicKey []byte, sequence uint64) skippedMapKey {
	return skippedMapKey{
		ratchetPublicKey: string(ratchetPublicKey),
		sequence:         sequence,
	}
}

// SkippedKeyStore is a concurrency-safe, one-shot store for message keys that
// were skipped while advancing a receive chain.
//
// A key is removed only after the decrypt callback succeeds. A failed AEAD
// authentication therefore neither consumes the key nor changes any receive
// chain state.
type SkippedKeyStore struct {
	mu     sync.Mutex
	keys   map[skippedMapKey][]byte
	counts map[string]int
}

// NewSkippedKeyStore creates an empty skipped-key store.
func NewSkippedKeyStore() *SkippedKeyStore {
	return &SkippedKeyStore{
		keys:   make(map[skippedMapKey][]byte),
		counts: make(map[string]int),
	}
}

// Len returns the total number of retained message keys.
func (s *SkippedKeyStore) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.keys)
}

// LenForRatchetPublicKey returns the number of retained message keys for the
// given ratchet public key.
func (s *SkippedKeyStore) LenForRatchetPublicKey(ratchetPublicKey []byte) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.counts[string(ratchetPublicKey)]
}

// Contains reports whether an unconsumed skipped key exists.
func (s *SkippedKeyStore) Contains(id SkippedKeyID) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.keys[newSkippedMapKey(id.RatchetPublicKey, id.Sequence)]
	return ok
}

// Get returns a copy of a retained message key without consuming it.
// Callers that need one-shot semantics should normally use a ReceiveChain,
// which removes the key only after successful authentication.
func (s *SkippedKeyStore) Get(id SkippedKeyID) ([]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.get(id)
}

// Take atomically returns and consumes a retained message key. It is useful
// for callers that have already authenticated the message by other means.
// ReceiveChain itself uses a stronger use-after-success operation so failed
// authentication does not consume the key.
func (s *SkippedKeyStore) Take(id SkippedKeyID) ([]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	key, ok := s.get(id)
	if !ok {
		return nil, false
	}
	s.delete(id)
	return key, true
}

// Consume is an alias for Take.
func (s *SkippedKeyStore) Consume(id SkippedKeyID) ([]byte, bool) {
	return s.Take(id)
}

// Put stores a skipped message key. It refuses to overwrite an existing key
// and enforces MaxSkippedKeysPerChain per ratchet public key.
func (s *SkippedKeyStore) Put(id SkippedKeyID, messageKey []byte) error {
	if len(messageKey) != MessageKeySize {
		return ErrInvalidMessageKey
	}
	return s.putBatch(id.RatchetPublicKey, []skippedKeyEntry{{
		sequence:   id.Sequence,
		messageKey: messageKey,
	}}, MaxSkippedKeysPerChain)
}

// Delete removes a retained message key and zeroes its contents.
func (s *SkippedKeyStore) Delete(id SkippedKeyID) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.delete(id)
}

// IDs returns the identifiers of all currently retained keys.
func (s *SkippedKeyStore) IDs() []SkippedKeyID {
	s.mu.Lock()
	defer s.mu.Unlock()

	ids := make([]SkippedKeyID, 0, len(s.keys))
	for key := range s.keys {
		ids = append(ids, SkippedKeyID{
			RatchetPublicKey: []byte(key.ratchetPublicKey),
			Sequence:         key.sequence,
		})
	}
	sort.Slice(ids, func(i, j int) bool {
		if !bytes.Equal(ids[i].RatchetPublicKey, ids[j].RatchetPublicKey) {
			return bytes.Compare(ids[i].RatchetPublicKey, ids[j].RatchetPublicKey) < 0
		}
		return ids[i].Sequence < ids[j].Sequence
	})
	return ids
}

type skippedKeyEntry struct {
	sequence   uint64
	messageKey []byte
}

func (s *SkippedKeyStore) count(ratchetPublicKey []byte) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.counts[string(ratchetPublicKey)]
}

func (s *SkippedKeyStore) get(id SkippedKeyID) ([]byte, bool) {
	key, ok := s.keys[newSkippedMapKey(id.RatchetPublicKey, id.Sequence)]
	if !ok {
		return nil, false
	}
	return cloneBytes(key), true
}

// putBatch atomically inserts a group of derived keys and enforces both the
// supplied per-chain limit and the hard global limit.
func (s *SkippedKeyStore) putBatch(ratchetPublicKey []byte, entries []skippedKeyEntry, maxSkipped int) error {
	if maxSkipped < 0 || maxSkipped > MaxSkippedKeysPerChain {
		return ErrInvalidMaxSkippedKeys
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	name := string(ratchetPublicKey)
	if s.counts[name]+len(entries) > maxSkipped {
		return ErrSkipLimitExceeded
	}

	seen := make(map[uint64]struct{}, len(entries))
	for _, entry := range entries {
		if _, duplicate := seen[entry.sequence]; duplicate {
			return ErrDuplicateMessage
		}
		seen[entry.sequence] = struct{}{}
		if _, exists := s.keys[newSkippedMapKey(ratchetPublicKey, entry.sequence)]; exists {
			return ErrDuplicateMessage
		}
	}

	for _, entry := range entries {
		key := newSkippedMapKey(ratchetPublicKey, entry.sequence)
		s.keys[key] = cloneBytes(entry.messageKey)
		s.counts[name]++
	}
	return nil
}

// use atomically looks up a retained key, invokes use, and deletes the key
// only when use succeeds. The store lock makes one-shot consumption safe even
// when multiple chain objects share a store.
func (s *SkippedKeyStore) use(id SkippedKeyID, use func(messageKey []byte) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	mapKey := newSkippedMapKey(id.RatchetPublicKey, id.Sequence)
	stored, ok := s.keys[mapKey]
	if !ok {
		return ErrDuplicateMessage
	}

	key := cloneBytes(stored)
	err := use(key)
	if err != nil {
		clearBytes(key)
		return err
	}

	clearBytes(stored)
	delete(s.keys, mapKey)
	s.counts[string(id.RatchetPublicKey)]--
	clearBytes(key)
	return nil
}

func (s *SkippedKeyStore) delete(id SkippedKeyID) bool {
	mapKey := newSkippedMapKey(id.RatchetPublicKey, id.Sequence)
	stored, ok := s.keys[mapKey]
	if !ok {
		return false
	}
	clearBytes(stored)
	delete(s.keys, mapKey)
	s.counts[string(id.RatchetPublicKey)]--
	return true
}

func cloneBytes(in []byte) []byte {
	if in == nil {
		return nil
	}
	out := make([]byte, len(in))
	copy(out, in)
	return out
}

func clearBytes(in []byte) {
	for i := range in {
		in[i] = 0
	}
}
