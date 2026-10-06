// Package keylock provides per-key mutual exclusion that survives shard
// count expansion while old lock holders are still active.
//
// The locker is organized as a chain of generations. Each generation owns a
// shard table keyed by hash(key). Expanding the shard count appends a new,
// empty generation instead of migrating existing entries, so a lock entry
// never moves once created. Acquisition starts at the current generation and
// walks the ancestor chain, therefore the same key always resolves to the
// same entry (and the same semaphore) no matter which generation the holder
// acquired it in. Different keys use independent entries and proceed in
// parallel.
//
// Cancellation only affects waiting: a context that is cancelled after the
// semaphore was obtained never revokes the lock. A Token is bound at
// acquisition time to the generation that owns the entry; Release is routed
// through that binding and is unaffected by later expansions.
package keylock

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
)

// Locker hands out per-key mutual-exclusion tokens.
type Locker struct {
	expandMu sync.Mutex // serializes Expand
	cur      atomic.Pointer[generation]
}

// generation is one shard-table configuration. Entries are created in the
// generation that is current when the key is first locked and stay there
// until their refcount drops to zero; expansions never move them.
type generation struct {
	id     uint64
	shards []shard
	prev   *generation // older generation, nil for the first
}

type shard struct {
	mu      sync.Mutex
	entries map[string]*entry
}

// entry is the mutual-exclusion state for one key. refs counts holders plus
// waiters and is guarded by the mutex of the shard that owns the entry.
type entry struct {
	sem  chan struct{} // capacity 1, used as a mutex
	refs int
}

// Token proves ownership of the lock for one key. It is bound at acquisition
// time to the generation that owns the underlying entry.
type Token struct {
	key   string
	e     *entry
	owner *shard // shard of the owning generation, for release routing
	gen   uint64 // id of the owning generation
	used  atomic.Bool
}

// New returns a Locker whose first generation has shardCount shards.
func New(shardCount int) *Locker {
	if shardCount < 1 {
		panic("keylock: New requires shardCount >= 1")
	}
	l := &Locker{}
	l.cur.Store(newGeneration(0, shardCount, nil))
	return l
}

func newGeneration(id uint64, shardCount int, prev *generation) *generation {
	g := &generation{id: id, shards: make([]shard, shardCount), prev: prev}
	for i := range g.shards {
		g.shards[i].entries = make(map[string]*entry)
	}
	return g
}

// Generation reports the id of the current generation (0 for the initial
// one, incremented by every Expand).
func (l *Locker) Generation() uint64 {
	return l.cur.Load().id
}

// ShardCount reports the shard count of the current generation.
func (l *Locker) ShardCount() int {
	return len(l.cur.Load().shards)
}

// Expand replaces the current generation with one that has shardCount
// shards. Existing entries stay in their owning generation, so holders and
// waiters from older generations are undisturbed; new acquisitions resolve
// them through the ancestor chain.
func (l *Locker) Expand(shardCount int) {
	l.expandMu.Lock()
	defer l.expandMu.Unlock()
	cur := l.cur.Load()
	if shardCount <= len(cur.shards) {
		panic(fmt.Sprintf("keylock: Expand requires shardCount > %d, got %d", len(cur.shards), shardCount))
	}
	l.cur.Store(newGeneration(cur.id+1, shardCount, cur))
}

// Acquire blocks until the lock for key is held or ctx is done while
// waiting. Cancellation never revokes an already-obtained lock.
func (l *Locker) Acquire(ctx context.Context, key string) (*Token, error) {
	g := l.cur.Load()
	sh := g.shardFor(key)

	// Find or create the entry. The current generation's shard lock is held
	// across the ancestor walk and the create so that a key can never end up
	// with two live entries: any other live entry for this key lives in an
	// ancestor of the generation we create in, and the walk observes it.
	sh.mu.Lock()
	e := sh.entries[key]
	owner, ownerGen := sh, g.id
	if e == nil {
		for p := g.prev; p != nil && e == nil; p = p.prev {
			ps := p.shardFor(key)
			ps.mu.Lock()
			if pe := ps.entries[key]; pe != nil {
				pe.refs++
				e, owner, ownerGen = pe, ps, p.id
			}
			ps.mu.Unlock()
		}
		if e == nil {
			e = &entry{sem: make(chan struct{}, 1), refs: 1}
			sh.entries[key] = e
		}
	} else {
		e.refs++
	}
	sh.mu.Unlock()

	select {
	case e.sem <- struct{}{}:
		t := &Token{key: key, e: e, owner: owner, gen: ownerGen}
		return t, nil
	case <-ctx.Done():
		// Cancelled while waiting: drop our reference only.
		owner.mu.Lock()
		e.refs--
		if e.refs == 0 {
			delete(owner.entries, key)
		}
		owner.mu.Unlock()
		return nil, ctx.Err()
	}
}

// Generation reports the id of the generation the token was bound to at
// acquisition time.
func (t *Token) Generation() uint64 {
	return t.gen
}

// Release relinquishes the lock. It is routed through the generation bound
// at acquisition time, so expansions after acquisition do not affect it.
// Releasing a token twice panics.
func (t *Token) Release() {
	if !t.used.CompareAndSwap(false, true) {
		panic("keylock: token released twice")
	}
	<-t.e.sem
	t.owner.mu.Lock()
	t.e.refs--
	if t.e.refs == 0 {
		delete(t.owner.entries, t.key)
	}
	t.owner.mu.Unlock()
}

func (g *generation) shardFor(key string) *shard {
	return &g.shards[hashKey(key)%uint32(len(g.shards))]
}

// ErrShardCount is returned by CheckExpand for invalid shard counts; kept for
// callers that prefer validating before Expand panics.
var ErrShardCount = errors.New("keylock: shard count must increase")

// CheckExpand reports whether Expand(shardCount) would succeed.
func (l *Locker) CheckExpand(shardCount int) error {
	if shardCount <= l.ShardCount() {
		return ErrShardCount
	}
	return nil
}

func hashKey(key string) uint32 {
	h := uint32(2166136261)
	for i := 0; i < len(key); i++ {
		h ^= uint32(key[i])
		h *= 16777619
	}
	return h
}
