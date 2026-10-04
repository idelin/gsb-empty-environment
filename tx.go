package dedup

import (
	"bytes"
	"fmt"
)

// Tx is a transaction against a Store. Exactly one transaction is open
// at a time: Begin takes the store lock and Commit/Rollback release it,
// so do not call other Store methods while a transaction is open.
//
// All changes are staged and applied atomically by Commit. If any
// validation fails (unknown block, reference count that would go
// negative), Commit returns an error and nothing is applied.
type Tx struct {
	store     *Store
	creates   []stagedCreate
	createIdx map[StrongDigest][]int
	sets      map[string]map[BlockKey]int
	dels      map[string]bool
	closed    bool
}

type stagedCreate struct {
	key  BlockKey
	data []byte
	weak WeakDigest
}

// Begin opens a transaction. It blocks until any open transaction ends.
func (s *Store) Begin() *Tx {
	s.mu.Lock()
	return &Tx{
		store:     s,
		createIdx: make(map[StrongDigest][]int),
		sets:      make(map[string]map[BlockKey]int),
		dels:      make(map[string]bool),
	}
}

func (t *Tx) mustOpen(op string) {
	if t.closed {
		panic(fmt.Sprintf("dedup: %s on closed transaction", op))
	}
}

// Put stages data for storage and returns its key. If an identical
// block already exists (committed or staged earlier in this tx) its key
// is returned and no new copy is made. Put alone does not reference the
// block; attach the key to a root with SetObject or GC may reclaim it.
func (t *Tx) Put(data []byte) BlockKey {
	t.mustOpen("Put")
	s := t.store
	weak := s.weakHash(data)
	strong := s.strongHash(data)
	if key, ok := s.findLocked(weak, strong, data); ok {
		return key
	}
	for _, ci := range t.createIdx[strong] {
		c := t.creates[ci]
		if len(c.data) == len(data) && bytes.Equal(c.data, data) {
			return c.key
		}
	}
	// New variant. This is also the collision-isolation path: if the
	// strong digest already exists but the content differs, the new
	// variant is appended and both copies are kept.
	idx := 0
	if fam := s.families[strong]; fam != nil {
		idx = len(fam.variants)
	}
	idx += len(t.createIdx[strong])
	key := BlockKey{Digest: strong, Variant: idx}
	clone := make([]byte, len(data))
	copy(clone, data)
	t.creates = append(t.creates, stagedCreate{key: key, data: clone, weak: weak})
	t.createIdx[strong] = append(t.createIdx[strong], len(t.creates)-1)
	return key
}

// SetObject stages root name to reference exactly the given keys,
// replacing its previous references. The reference count changes are
// computed and applied at Commit.
func (t *Tx) SetObject(name string, keys []BlockKey) {
	t.mustOpen("SetObject")
	m := make(map[BlockKey]int, len(keys))
	for _, k := range keys {
		m[k]++
	}
	t.sets[name] = m
	delete(t.dels, name)
}

// DeleteObject stages removal of a root and the release of its
// references.
func (t *Tx) DeleteObject(name string) {
	t.mustOpen("DeleteObject")
	delete(t.sets, name)
	t.dels[name] = true
}

// Commit validates and atomically applies every staged change. On any
// error nothing is applied and the transaction is rolled back.
func (t *Tx) Commit() error {
	if t.closed {
		return ErrTxClosed
	}
	t.closed = true
	s := t.store
	defer s.mu.Unlock()

	// Validate that every key referenced by a new root exists in the
	// committed store or is staged by this transaction.
	for name, keys := range t.sets {
		for k := range keys {
			if !t.knownLocked(k) {
				return &Error{Op: "commit set " + name, Key: k, Err: ErrUnknownBlock}
			}
		}
	}

	// Compute reference count deltas from the root changes.
	deltas := make(map[BlockKey]int)
	for name := range t.dels {
		for k, n := range s.objects[name] {
			deltas[k] -= n
		}
	}
	for name, keys := range t.sets {
		for k, n := range s.objects[name] {
			deltas[k] -= n
		}
		for k, n := range keys {
			deltas[k] += n
		}
	}

	// Validate every count before mutating anything, so a failure
	// leaves the store untouched (rollback).
	for k, d := range deltas {
		cur, ok := t.countLocked(k)
		if !ok {
			return &Error{Op: "commit", Key: k, Err: ErrUnknownBlock}
		}
		if cur+d < 0 {
			return &Error{Op: "commit", Key: k, Err: ErrNegativeRefCount}
		}
	}

	// Apply staged creates.
	for _, c := range t.creates {
		fam := s.families[c.key.Digest]
		if fam == nil {
			fam = &family{}
			s.families[c.key.Digest] = fam
		}
		if len(fam.variants) != c.key.Variant {
			return &Error{Op: "commit", Key: c.key, Err: fmt.Errorf("internal variant index mismatch")}
		}
		fam.variants = append(fam.variants, &variant{data: c.data, weak: c.weak})
		s.registerWeak(c.weak, c.key.Digest)
	}

	// Apply reference count deltas.
	for k, d := range deltas {
		if d != 0 {
			s.families[k.Digest].variants[k.Variant].refCount += d
		}
	}

	// Apply root changes.
	for name := range t.dels {
		delete(s.objects, name)
	}
	for name, keys := range t.sets {
		if len(keys) == 0 {
			delete(s.objects, name)
		} else {
			s.objects[name] = keys
		}
	}
	return nil
}

// Rollback discards every staged change and closes the transaction.
func (t *Tx) Rollback() {
	if t.closed {
		return
	}
	t.closed = true
	t.store.mu.Unlock()
}

// knownLocked reports whether key resolves to a committed block or a
// block staged by this transaction.
func (t *Tx) knownLocked(key BlockKey) bool {
	if t.store.variantLocked(key) != nil {
		return true
	}
	for _, ci := range t.createIdx[key.Digest] {
		if t.creates[ci].key == key {
			return true
		}
	}
	return false
}

// countLocked returns the current count of a known block; staged
// creates count as zero.
func (t *Tx) countLocked(key BlockKey) (int, bool) {
	if v := t.store.variantLocked(key); v != nil {
		return v.refCount, true
	}
	for _, ci := range t.createIdx[key.Digest] {
		if t.creates[ci].key == key {
			return 0, true
		}
	}
	return 0, false
}
