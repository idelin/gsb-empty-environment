// Package keymutex provides per-key mutual exclusion that stays correct
// while the underlying lock table is resharded (expanded) at runtime.
//
// The mutex is organised as a striped registry of active keys. Expand
// replaces the shard table with a larger one and migrates every live
// entry — including its FIFO wait queue — so a holder that acquired a key
// under an old generation still blocks new acquirers of the same key in
// the new generation. Different keys never block each other: waiting is
// always per key.
//
// Cancellation only affects waiting. A cancelled context aborts an
// Acquire that is still queued, but never revokes a token that has
// already been granted.
//
// Every granted Token is bound to the table generation that was active
// when the acquisition completed. Release validates the token against
// the current holder record and panics on a stale, foreign, or double
// release.
package keymutex

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
)

// Token is the capability for a held key. It is bound to the generation
// that was active when the acquisition completed and must be handed back
// to Release exactly once.
type Token struct {
	key string
	gen uint64
	seq uint64
}

// Key returns the locked key.
func (t Token) Key() string { return t.key }

// Generation returns the table generation the token was acquired under.
func (t Token) Generation() uint64 { return t.gen }

// waiter is one queued acquisition attempt.
type waiter struct {
	ch     chan Token // buffered (cap 1); the grant is delivered here
	queued bool       // cleared by the releaser when the grant is made
	prev   *waiter
	next   *waiter
}

// entry tracks the current holder of one active key and its FIFO queue.
type entry struct {
	holder Token
	head   *waiter
	tail   *waiter
}

func (e *entry) push(w *waiter) {
	w.prev = e.tail
	if e.tail != nil {
		e.tail.next = w
	} else {
		e.head = w
	}
	e.tail = w
}

func (e *entry) remove(w *waiter) {
	if w.prev != nil {
		w.prev.next = w.next
	} else {
		e.head = w.next
	}
	if w.next != nil {
		w.next.prev = w.prev
	} else {
		e.tail = w.prev
	}
	w.prev, w.next = nil, nil
}

func (e *entry) pop() *waiter {
	w := e.head
	if w != nil {
		e.remove(w)
	}
	return w
}

// shard is one stripe of the registry.
type shard struct {
	mu      sync.Mutex
	entries map[string]*entry
}

// table is one generation of the striped registry.
type table struct {
	gen    uint64
	shards []shard
}

func newTable(gen uint64, n int) *table {
	t := &table{gen: gen, shards: make([]shard, n)}
	for i := range t.shards {
		t.shards[i].entries = make(map[string]*entry)
	}
	return t
}

func (t *table) shardFor(key string) *shard {
	return &t.shards[fnv64a(key)%uint64(len(t.shards))]
}

// Mutex is a reshardable key-space mutex. The zero value is not usable;
// construct it with New.
type Mutex struct {
	// resizeMu guards the table pointer. Acquirers, releasers and
	// cancellers hold it for reading only while they latch their shard;
	// Expand holds it for writing while it latches every shard of the
	// old table and migrates the entries. Lock order is always
	// resizeMu -> shard.mu, never the reverse.
	resizeMu sync.RWMutex
	tbl      *table
	seq      uint64 // atomic; unique token sequence numbers
}

// New returns a Mutex whose registry starts with the given number of
// shards (generation 1).
func New(shards int) *Mutex {
	if shards <= 0 {
		panic(fmt.Sprintf("keymutex: New with non-positive shard count %d", shards))
	}
	return &Mutex{tbl: newTable(1, shards)}
}

// Generation returns the current table generation.
func (m *Mutex) Generation() uint64 {
	m.resizeMu.RLock()
	defer m.resizeMu.RUnlock()
	return m.tbl.gen
}

// Shards returns the current shard count.
func (m *Mutex) Shards() int {
	m.resizeMu.RLock()
	defer m.resizeMu.RUnlock()
	return len(m.tbl.shards)
}

// Expand reshards the registry to the given shard count, starting a new
// generation, and returns the new generation number. Live entries —
// holders and their wait queues — are migrated, so keys held under older
// generations remain exclusively held. Expanding to the current shard
// count is a no-op and returns the current generation.
func (m *Mutex) Expand(shards int) uint64 {
	if shards <= 0 {
		panic(fmt.Sprintf("keymutex: Expand with non-positive shard count %d", shards))
	}
	m.resizeMu.Lock()
	defer m.resizeMu.Unlock()
	old := m.tbl
	if len(old.shards) == shards {
		return old.gen
	}
	for i := range old.shards {
		old.shards[i].mu.Lock()
	}
	nt := newTable(old.gen+1, shards)
	for i := range old.shards {
		for key, e := range old.shards[i].entries {
			nt.shardFor(key).entries[key] = e
		}
	}
	m.tbl = nt
	for i := range old.shards {
		old.shards[i].mu.Unlock()
	}
	return nt.gen
}

// latchShard locks the shard responsible for key in the current table
// generation and returns the table and shard. The resize read lock is
// released before returning, so an in-flight Expand can proceed as soon
// as the shard is latched; the shard itself stays valid because old
// tables are kept alive by the garbage collector.
func (m *Mutex) latchShard(key string) (*table, *shard) {
	m.resizeMu.RLock()
	t := m.tbl
	sh := t.shardFor(key)
	sh.mu.Lock()
	m.resizeMu.RUnlock()
	return t, sh
}

// Acquire blocks until the key is exclusively held by the caller and
// returns the token that must later be passed to Release. If the key is
// free it is granted immediately, even if ctx is already cancelled:
// cancellation only affects waiting. While waiting, Acquire honours
// ctx and returns ctx.Err() if the wait is aborted; a grant that races
// with cancellation still wins and is returned as a valid token.
func (m *Mutex) Acquire(ctx context.Context, key string) (Token, error) {
	t, sh := m.latchShard(key)
	e := sh.entries[key]
	if e == nil {
		tok := Token{key: key, gen: t.gen, seq: atomic.AddUint64(&m.seq, 1)}
		sh.entries[key] = &entry{holder: tok}
		sh.mu.Unlock()
		return tok, nil
	}
	w := &waiter{ch: make(chan Token, 1), queued: true}
	e.push(w)
	sh.mu.Unlock()

	select {
	case tok := <-w.ch:
		return tok, nil
	case <-ctx.Done():
	}

	// Cancellation path: re-latch through the *current* table — an
	// Expand may have migrated the entry (and this waiter) to a new
	// shard since we queued.
	_, sh = m.latchShard(key)
	if w.queued {
		// The entry cannot have vanished while a waiter is queued.
		sh.entries[key].remove(w)
		sh.mu.Unlock()
		return Token{}, ctx.Err()
	}
	sh.mu.Unlock()
	// The grant raced with the cancellation and already won.
	return <-w.ch, nil
}

// Lock is Acquire with a background context: it waits indefinitely.
func (m *Mutex) Lock(key string) Token {
	tok, err := m.Acquire(context.Background(), key)
	if err != nil {
		panic("keymutex: background context cancelled")
	}
	return tok
}

// Release frees the key held by tok. The next queued waiter, if any, is
// granted a fresh token bound to the current generation. Release panics
// if tok is not the token currently holding its key (zero, stale,
// foreign, or already released).
func (m *Mutex) Release(tok Token) {
	t, sh := m.latchShard(tok.key)
	e := sh.entries[tok.key]
	if e == nil || e.holder != tok {
		sh.mu.Unlock()
		panic(fmt.Sprintf("keymutex: release of invalid token for key %q", tok.key))
	}
	if w := e.pop(); w != nil {
		nt := Token{key: tok.key, gen: t.gen, seq: atomic.AddUint64(&m.seq, 1)}
		e.holder = nt
		w.queued = false
		w.ch <- nt
		sh.mu.Unlock()
		return
	}
	delete(sh.entries, tok.key)
	sh.mu.Unlock()
}

// queued reports how many waiters are queued on key. It is intended for
// tests and diagnostics.
func (m *Mutex) queued(key string) int {
	_, sh := m.latchShard(key)
	defer sh.mu.Unlock()
	n := 0
	if e := sh.entries[key]; e != nil {
		for w := e.head; w != nil; w = w.next {
			n++
		}
	}
	return n
}

func fnv64a(s string) uint64 {
	const (
		offset64 = 14695981039346656037
		prime64  = 1099511628211
	)
	h := uint64(offset64)
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= prime64
	}
	return h
}
