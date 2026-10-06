package dedup

import "sync"

// ledger is the reference account book for the store. Every Acquire
// adds one credit for a key, every Release settles it. The eviction
// pass consults the ledger: a key with a positive count is pinned and
// must not be evicted, so cleanup can never invalidate a result that a
// later operation is still referencing.
type ledger struct {
	mu     sync.Mutex
	counts map[string]int
}

func newLedger() *ledger {
	return &ledger{counts: make(map[string]int)}
}

func (l *ledger) acquire(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.counts[key]++
}

func (l *ledger) release(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.counts[key]--
}

func (l *ledger) count(key string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.counts[key]
}

// snapshot returns a copy of all counts, used by Store.Verify.
func (l *ledger) snapshot() map[string]int {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make(map[string]int, len(l.counts))
	for k, n := range l.counts {
		out[k] = n
	}
	return out
}

// Ref is an active reference to a stored result. Release is idempotent.
type Ref struct {
	store *Store
	key   string
	once  sync.Once
}

// Value returns the referenced result. It returns nil after the Ref
// has been released or if the result no longer exists.
func (r *Ref) Value() interface{} {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	if e, ok := r.store.entries[r.key]; ok {
		return e.value
	}
	return nil
}

// Release settles this reference in the ledger. It is safe to call
// multiple times; only the first call has an effect.
func (r *Ref) Release() {
	r.once.Do(func() {
		r.store.ledger.release(r.key)
	})
}
