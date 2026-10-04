// Package bitmap implements a compressed bitmap (Roaring-bitmap style)
// with automatic container selection (array / bitmap / run-length-encoded)
// and a shard-based conflict arbiter that guarantees atomicity and
// linearizability of all operations under multi-goroutine concurrency.
//
// Concurrency model (the "arbiter"):
//   - The 16-bit container key space is striped over 256 shards, each with
//     its own sync.RWMutex. Point operations (Add/Remove/Contains) touch
//     exactly one shard, so unrelated keys never contend.
//   - Every mutation bumps an atomic version counter, and every shard that
//     has ever received a container is marked in an atomic occupancy mask
//     (set-only, marked before the insert becomes visible).
//   - Batch operations (And/Or/Xor/AndMany/OrMany) first try an
//     optimistic, seqlock-style pass: snapshot versions and occupancy
//     masks, read-lock only the occupied shards (in the global order:
//     bitmap id, then shard index), compute, and validate that no version
//     changed. An unchanged version proves every shard was constant for
//     the whole window, so the result reflects one atomic point in time:
//     the operation is linearizable.
//   - If validation repeatedly fails under write contention, batch
//     operations fall back to read-locking ALL shards of ALL operands in
//     the same global order. Since every goroutine acquires locks in that
//     single order and writers never hold more than one lock, deadlock is
//     impossible by construction and the fallback always terminates.
package bitmap

import (
	"sync"
	"sync/atomic"
)

const numShards = 256

var bitmapIDCounter uint64

type shard struct {
	mu         sync.RWMutex
	containers map[uint16]container // key: high 16 bits of the uint32 id; lazily created
}

// Bitmap is a compressed, concurrency-safe set of uint32 values.
type Bitmap struct {
	id      uint64
	shards  [numShards]*shard
	version uint64    // atomic; incremented on every successful mutation
	mask    [4]uint64 // atomic; set-only bits marking non-empty shards
}

// New returns an empty Bitmap.
func New() *Bitmap {
	b := &Bitmap{id: atomic.AddUint64(&bitmapIDCounter, 1)}
	for i := 0; i < numShards; i++ {
		b.shards[i] = &shard{}
	}
	return b
}

func shardOf(hi uint16) int { return int(hi >> 8) }

// markShard records that shard i may hold containers. It is set-only and
// must be called before the corresponding insert becomes visible, so that
// batch readers observing an unset bit can safely treat the shard as
// empty (any racing insert will bump the version and force a retry).
func (b *Bitmap) markShard(i int) {
	w := i >> 6
	bit := uint64(1) << (uint(i) & 63)
	if atomic.LoadUint64(&b.mask[w])&bit == 0 {
		atomic.OrUint64(&b.mask[w], bit)
	}
}

// putContainer stores c in shard i and marks the occupancy mask. Used
// when building result bitmaps (not yet shared, but kept uniform).
func (b *Bitmap) putContainer(i int, hi uint16, c container) {
	if b.shards[i].containers == nil {
		b.shards[i].containers = make(map[uint16]container)
	}
	b.shards[i].containers[hi] = c
	atomic.OrUint64(&b.mask[i>>6], uint64(1)<<(uint(i)&63))
}

// Add inserts id, returning true if it was not already present.
func (b *Bitmap) Add(id uint32) bool {
	hi := uint16(id >> 16)
	lo := uint16(id)
	b.markShard(shardOf(hi))
	s := b.shards[shardOf(hi)]
	s.mu.Lock()
	if s.containers == nil {
		s.containers = make(map[uint16]container)
	}
	c := s.containers[hi]
	if c == nil {
		c = &arrayContainer{}
	}
	nc, added := c.add(lo)
	if added {
		s.containers[hi] = nc
		atomic.AddUint64(&b.version, 1)
	}
	s.mu.Unlock()
	return added
}

// AddRange inserts every id in [lo, hi). Values above 1<<32 are clamped.
// Dense ranges are stored as run-length-encoded containers automatically.
func (b *Bitmap) AddRange(lo, hi uint64) {
	if lo >= hi {
		return
	}
	if hi > 1<<32 {
		hi = 1 << 32
	}
	if lo >= hi {
		return
	}
	first := uint32(lo >> 16)
	last := uint32((hi - 1) >> 16)
	for k := first; k <= last; k++ {
		start := uint16(0)
		end := uint16(0xFFFF)
		if k == first {
			start = uint16(lo)
		}
		if k == last {
			end = uint16(hi - 1)
		}
		b.addInterval(uint16(k), start, end)
	}
}

func (b *Bitmap) addInterval(hi uint16, start, end uint16) {
	b.markShard(shardOf(hi))
	s := b.shards[shardOf(hi)]
	s.mu.Lock()
	if s.containers == nil {
		s.containers = make(map[uint16]container)
	}
	c := s.containers[hi]
	if c == nil {
		c = &runContainer{}
	}
	s.containers[hi] = containerAddRange(c, start, end)
	atomic.AddUint64(&b.version, 1)
	s.mu.Unlock()
}

// Remove deletes id, returning true if it was present.
func (b *Bitmap) Remove(id uint32) bool {
	hi := uint16(id >> 16)
	lo := uint16(id)
	s := b.shards[shardOf(hi)]
	s.mu.Lock()
	c := s.containers[hi]
	if c == nil {
		s.mu.Unlock()
		return false
	}
	nc, removed := c.remove(lo)
	if removed {
		if nc.cardinality() == 0 {
			delete(s.containers, hi)
		} else {
			s.containers[hi] = nc
		}
		atomic.AddUint64(&b.version, 1)
	}
	s.mu.Unlock()
	return removed
}

// Contains reports whether id is present.
func (b *Bitmap) Contains(id uint32) bool {
	hi := uint16(id >> 16)
	lo := uint16(id)
	s := b.shards[shardOf(hi)]
	s.mu.RLock()
	c := s.containers[hi]
	ok := c != nil && c.contains(lo)
	s.mu.RUnlock()
	return ok
}

// Cardinality returns the number of elements in the bitmap.
func (b *Bitmap) Cardinality() uint64 {
	var n uint64
	for i := 0; i < numShards; i++ {
		s := b.shards[i]
		s.mu.RLock()
		for _, c := range s.containers {
			n += uint64(c.cardinality())
		}
		s.mu.RUnlock()
	}
	return n
}

// IsEmpty reports whether the bitmap has no elements.
func (b *Bitmap) IsEmpty() bool {
	for i := 0; i < numShards; i++ {
		s := b.shards[i]
		s.mu.RLock()
		empty := len(s.containers) == 0
		s.mu.RUnlock()
		if !empty {
			return false
		}
	}
	return true
}

// Version returns a monotonically increasing mutation counter. Any two
// different values guarantee at least one mutation happened in between.
func (b *Bitmap) Version() uint64 {
	return atomic.LoadUint64(&b.version)
}

// Stats counts the containers of each representation. Used by tests and
// diagnostics to verify automatic container selection.
func (b *Bitmap) Stats() (arrays, bitmaps, runs int) {
	for i := 0; i < numShards; i++ {
		s := b.shards[i]
		s.mu.RLock()
		for _, c := range s.containers {
			switch c.(type) {
			case *arrayContainer:
				arrays++
			case *bitmapContainer:
				bitmaps++
			case *runContainer:
				runs++
			}
		}
		s.mu.RUnlock()
	}
	return
}

// lockBitmaps acquires read locks on every shard of every given bitmap in
// the single global order (bitmap id ascending, shard index ascending) and
// returns an unlock function. This is the conflict arbiter: because all
// goroutines contend for the same locks in the same order, batch operations
// are atomic, linearizable, and deadlock-free.
func lockBitmaps(bs ...*Bitmap) func() {
	sorted := make([]*Bitmap, 0, len(bs))
	seen := make(map[uint64]struct{}, len(bs))
	for _, b := range bs {
		if _, dup := seen[b.id]; !dup {
			seen[b.id] = struct{}{}
			sorted = append(sorted, b)
		}
	}
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[j].id < sorted[i].id {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}
	for _, b := range sorted {
		for i := 0; i < numShards; i++ {
			b.shards[i].mu.RLock()
		}
	}
	return func() {
		for _, b := range sorted {
			for i := numShards - 1; i >= 0; i-- {
				b.shards[i].mu.RUnlock()
			}
		}
	}
}
