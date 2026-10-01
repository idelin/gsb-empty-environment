// Package strdict implements a versioned, frozen shared-string dictionary
// for object batches, plus a streaming encoder/decoder that mixes dictionary
// references with inline strings under a strict byte budget.
//
// Serialization scope (deliberately narrow): a stream is a flat sequence of
// string records. Records are either INLINE (length-prefixed UTF-8 bytes),
// REF (uvarint index into the session's current dictionary version), or
// SWITCH (uvarint version that re-pins the session to a newer frozen
// dictionary generation). There are no nested structures, no type tags beyond
// the record opcodes, and decoding never executes host code: it only copies
// bytes and looks up table entries. Lengths are always validated against the
// remaining byte budget before any allocation grows.
package strdict

import (
	"errors"
	"fmt"
	"hash/fnv"
	"sync"
)

// HashFunc hashes a string for bucket placement. Collisions are expected and
// handled by full string comparison within a bucket; tests may inject a
// degenerate HashFunc to force collisions.
type HashFunc func(string) uint64

// DefaultHash is FNV-1a 64.
func DefaultHash(s string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return h.Sum64()
}

// Builder accumulates strings for one dictionary generation. The empty string
// is a legitimate pooled entry. Builders are not safe for concurrent use.
type Builder struct {
	hash    HashFunc
	entries []string
	buckets map[uint64][]int // hash -> candidate indices, resolved by full compare
}

// NewBuilder returns a Builder using DefaultHash.
func NewBuilder() *Builder { return NewBuilderWithHash(DefaultHash) }

// NewBuilderWithHash returns a Builder using a custom hash (for collision tests).
func NewBuilderWithHash(h HashFunc) *Builder {
	return &Builder{hash: h, buckets: make(map[uint64][]int)}
}

// Add interns s and returns its stable index within this generation.
func (b *Builder) Add(s string) int {
	sum := b.hash(s)
	for _, i := range b.buckets[sum] {
		if b.entries[i] == s {
			return i
		}
	}
	idx := len(b.entries)
	b.entries = append(b.entries, s)
	b.buckets[sum] = append(b.buckets[sum], idx)
	return idx
}

// Len reports the number of interned entries.
func (b *Builder) Len() int { return len(b.entries) }

// Dictionary is an immutable, frozen generation. Safe for concurrent reads.
type Dictionary struct {
	version uint64
	entries []string
	buckets map[uint64][]int
	bytes   int64 // sum of len(entry) over all entries; charged to decode budgets
}

func freeze(version uint64, b *Builder) *Dictionary {
	d := &Dictionary{version: version, entries: b.entries, buckets: b.buckets}
	for _, e := range b.entries {
		d.bytes += int64(len(e))
	}
	return d
}

// Version returns the frozen generation number.
func (d *Dictionary) Version() uint64 { return d.version }

// Bytes returns the total payload bytes of all entries.
func (d *Dictionary) Bytes() int64 { return d.bytes }

// Len returns the number of entries.
func (d *Dictionary) Len() int { return len(d.entries) }

// Lookup resolves an index. Unknown indices are never treated as the empty
// string: ok is false and callers must surface an error.
func (d *Dictionary) Lookup(i int) (s string, ok bool) {
	if i < 0 || i >= len(d.entries) {
		return "", false
	}
	return d.entries[i], true
}

// IndexOf finds the index of s, resolving hash collisions by full compare.
func (d *Dictionary) IndexOf(hash HashFunc, s string) (int, bool) {
	for _, i := range d.buckets[hash(s)] {
		if d.entries[i] == s {
			return i, true
		}
	}
	return -1, false
}

// Registry publishes frozen dictionary generations and tracks read leases so
// entries are only reclaimed when no reader can still observe them.
type Registry struct {
	mu     sync.Mutex
	next   uint64
	byVers map[uint64]*generation
}

type generation struct {
	dict      *Dictionary
	leases    int
	cancelled bool // batch-cancelled: new leases fail, existing leases go invalid
	reclaimed bool // removed from publication
}

// NewRegistry returns an empty registry; the first published version is 1.
func NewRegistry() *Registry {
	return &Registry{byVers: make(map[uint64]*generation)}
}

// Publish freezes b as the next generation and returns it.
func (r *Registry) Publish(b *Builder) *Dictionary {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.next++
	d := freeze(r.next, b)
	r.byVers[d.version] = &generation{dict: d}
	return d
}

// Acquire pins a read lease on a frozen version. It fails for unknown,
// cancelled, or reclaimed versions.
func (r *Registry) Acquire(version uint64) (*Lease, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	g, ok := r.byVers[version]
	if !ok || g.reclaimed {
		return nil, fmt.Errorf("strdict: dictionary version %d not published", version)
	}
	if g.cancelled {
		return nil, fmt.Errorf("strdict: dictionary version %d is cancelled", version)
	}
	g.leases++
	return &Lease{reg: r, gen: g}, nil
}

// Reclaim removes a version from publication. It refuses while any read lease
// is active, so in-flight decoders never lose their table.
func (r *Registry) Reclaim(version uint64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	g, ok := r.byVers[version]
	if !ok || g.reclaimed {
		return fmt.Errorf("strdict: dictionary version %d not published", version)
	}
	if g.leases > 0 {
		return fmt.Errorf("strdict: version %d has %d active read lease(s)", version, g.leases)
	}
	g.reclaimed = true
	delete(r.byVers, version)
	return nil
}

// Cancel marks one version cancelled: new leases fail and existing leases
// become invalid. Reclamation still requires all leases to be released.
func (r *Registry) Cancel(version uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if g, ok := r.byVers[version]; ok {
		g.cancelled = true
	}
}

// CancelAll batch-cancels every published version.
func (r *Registry) CancelAll() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, g := range r.byVers {
		g.cancelled = true
	}
}

// ErrLeaseInvalid is returned when a lease is used after release or after its
// generation was batch-cancelled.
var ErrLeaseInvalid = errors.New("strdict: read lease is no longer valid")

// Lease is an active read hold on one frozen generation.
type Lease struct {
	reg      *Registry
	gen      *generation
	released bool
	mu       sync.Mutex
}

// Dict returns the pinned dictionary, or ErrLeaseInvalid.
func (l *Lease) Dict() (*Dictionary, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.released {
		return nil, ErrLeaseInvalid
	}
	l.reg.mu.Lock()
	cancelled := l.gen.cancelled
	l.reg.mu.Unlock()
	if cancelled {
		return nil, ErrLeaseInvalid
	}
	return l.gen.dict, nil
}

// Lookup resolves an index through the lease.
func (l *Lease) Lookup(i int) (string, error) {
	d, err := l.Dict()
	if err != nil {
		return "", err
	}
	s, ok := d.Lookup(i)
	if !ok {
		return "", fmt.Errorf("strdict: unknown index %d in dictionary version %d", i, d.version)
	}
	return s, nil
}

// Release drops the read hold. Idempotent.
func (l *Lease) Release() {
	l.mu.Lock()
	if l.released {
		l.mu.Unlock()
		return
	}
	l.released = true
	l.mu.Unlock()
	l.reg.mu.Lock()
	l.gen.leases--
	l.reg.mu.Unlock()
}
