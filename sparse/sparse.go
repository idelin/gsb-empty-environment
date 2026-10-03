// Package sparse implements a memory-compact sparse set over the uint32
// key space. Keys are sharded into 16-bit containers that adaptively
// convert between sorted-array, bitmap and run-length encodings; Optimize
// re-picks the most compact encoding per container.
package sparse

import (
	"math/bits"
	"sort"
	"sync"
)

const (
	// ContainerBits is the number of low key bits covered by one container.
	ContainerBits = 16
	// ContainerSize is the number of keys per container.
	ContainerSize = 1 << ContainerBits

	arrayMax      = 4096 // array -> bitmap conversion threshold
	bitmapMinCard = 2048 // bitmap -> array hysteresis threshold

	sliceHeaderBytes = 24
	bitmapBytes      = ContainerSize / 8 // 8192
)

type interval struct {
	start  uint16
	length uint16 // number of keys minus one; 0xFFFF spans the whole container
}

// container is one 16-bit shard of the key space.
type container interface {
	get(k uint16) bool
	// add returns the (possibly converted) container and whether k was new.
	add(k uint16) (container, bool)
	// remove returns the (possibly converted) container and whether k existed.
	remove(k uint16) (container, bool)
	cardinality() int
	memBytes() int
	// optimize converts to the most compact representation of the same keys.
	optimize() container
	clone() container
	foreach(func(uint16))
}

// ---------------------------------------------------------------------------
// arrayContainer: sorted slice, best for low cardinality.
// ---------------------------------------------------------------------------

type arrayContainer struct{ keys []uint16 }

func (a *arrayContainer) search(k uint16) (int, bool) {
	i := sort.Search(len(a.keys), func(i int) bool { return a.keys[i] >= k })
	if i < len(a.keys) && a.keys[i] == k {
		return i, true
	}
	return i, false
}

func (a *arrayContainer) get(k uint16) bool { _, ok := a.search(k); return ok }

func (a *arrayContainer) add(k uint16) (container, bool) {
	i, ok := a.search(k)
	if ok {
		return a, false
	}
	a.keys = append(a.keys, 0)
	copy(a.keys[i+1:], a.keys[i:])
	a.keys[i] = k
	if len(a.keys) > arrayMax {
		return a.toBitmap(), true
	}
	return a, true
}

func (a *arrayContainer) remove(k uint16) (container, bool) {
	i, ok := a.search(k)
	if !ok {
		return a, false
	}
	a.keys = append(a.keys[:i], a.keys[i+1:]...)
	return a, true
}

func (a *arrayContainer) cardinality() int { return len(a.keys) }

func (a *arrayContainer) memBytes() int { return sliceHeaderBytes + 2*cap(a.keys) }

func (a *arrayContainer) clone() container {
	out := &arrayContainer{keys: make([]uint16, len(a.keys))}
	copy(out.keys, a.keys)
	return out
}

func (a *arrayContainer) foreach(f func(uint16)) {
	for _, k := range a.keys {
		f(k)
	}
}

func (a *arrayContainer) toBitmap() *bitmapContainer {
	b := &bitmapContainer{}
	for _, k := range a.keys {
		b.bits[k>>6] |= 1 << (k & 63)
	}
	b.card = len(a.keys)
	return b
}

func (a *arrayContainer) toRuns() *runContainer {
	r := &runContainer{}
	for _, k := range a.keys {
		r.appendKey(k)
	}
	return r
}

func (a *arrayContainer) optimize() container {
	runs := 1
	for i := 1; i < len(a.keys); i++ {
		if a.keys[i] != a.keys[i-1]+1 {
			runs++
		}
	}
	if len(a.keys) == 0 {
		runs = 0
	}
	runMem := sliceHeaderBytes + 4*runs
	arrMem := sliceHeaderBytes + 2*len(a.keys)
	switch {
	case runMem <= arrMem && runMem <= bitmapBytes:
		return a.toRuns()
	case arrMem <= bitmapBytes:
		return a
	default:
		return a.toBitmap()
	}
}

// ---------------------------------------------------------------------------
// bitmapContainer: fixed 8192-byte bitmap, best for dense shards.
// ---------------------------------------------------------------------------

type bitmapContainer struct {
	bits [ContainerSize / 64]uint64
	card int
}

func (b *bitmapContainer) get(k uint16) bool { return b.bits[k>>6]&(1<<(k&63)) != 0 }

func (b *bitmapContainer) add(k uint16) (container, bool) {
	mask := uint64(1) << (k & 63)
	if b.bits[k>>6]&mask != 0 {
		return b, false
	}
	b.bits[k>>6] |= mask
	b.card++
	return b, true
}

func (b *bitmapContainer) remove(k uint16) (container, bool) {
	mask := uint64(1) << (k & 63)
	if b.bits[k>>6]&mask == 0 {
		return b, false
	}
	b.bits[k>>6] &^= mask
	b.card--
	if b.card <= bitmapMinCard {
		return b.toArray(), true
	}
	return b, true
}

func (b *bitmapContainer) cardinality() int { return b.card }

func (b *bitmapContainer) memBytes() int { return bitmapBytes + 8 }

func (b *bitmapContainer) clone() container {
	out := &bitmapContainer{bits: b.bits, card: b.card}
	return out
}

func (b *bitmapContainer) foreach(f func(uint16)) {
	for w := 0; w < len(b.bits); w++ {
		word := b.bits[w]
		for word != 0 {
			t := word & -word
			f(uint16(w*64 + bits.TrailingZeros64(word)))
			word ^= t
		}
	}
}

func (b *bitmapContainer) toArray() *arrayContainer {
	a := &arrayContainer{keys: make([]uint16, 0, b.card)}
	b.foreach(func(k uint16) { a.keys = append(a.keys, k) })
	return a
}

func (b *bitmapContainer) optimize() container {
	return b.toArray().optimize()
}

// ---------------------------------------------------------------------------
// runContainer: sorted disjoint intervals, best for clustered keys.
// ---------------------------------------------------------------------------

type runContainer struct{ runs []interval }

func (r *runContainer) appendKey(k uint16) {
	n := len(r.runs)
	if n > 0 && uint32(r.runs[n-1].start)+uint32(r.runs[n-1].length)+1 == uint32(k) {
		r.runs[n-1].length++
		return
	}
	r.runs = append(r.runs, interval{start: k})
}

func (r *runContainer) get(k uint16) bool {
	i := sort.Search(len(r.runs), func(i int) bool {
		return uint32(r.runs[i].start)+uint32(r.runs[i].length) >= uint32(k)
	})
	return i < len(r.runs) && r.runs[i].start <= k
}

func (r *runContainer) add(k uint16) (container, bool) {
	if r.get(k) {
		return r, false
	}
	// Insert position: first run ending at or after k-1.
	i := sort.Search(len(r.runs), func(i int) bool {
		return uint32(r.runs[i].start)+uint32(r.runs[i].length)+1 >= uint32(k)
	})
	switch {
	case i < len(r.runs) && uint32(r.runs[i].start)+uint32(r.runs[i].length)+1 == uint32(k):
		// k extends run i on the right; maybe merges with run i+1.
		r.runs[i].length++
		if i+1 < len(r.runs) && uint32(r.runs[i].start)+uint32(r.runs[i].length)+1 == uint32(r.runs[i+1].start) {
			r.runs[i].length += r.runs[i+1].length + 1
			r.runs = append(r.runs[:i+1], r.runs[i+2:]...)
		}
	case i < len(r.runs) && uint32(r.runs[i].start) == uint32(k)+1:
		// k extends run i on the left.
		r.runs[i].start--
		r.runs[i].length++
	default:
		r.runs = append(r.runs, interval{})
		copy(r.runs[i+1:], r.runs[i:])
		r.runs[i] = interval{start: k}
	}
	return r, true
}

func (r *runContainer) remove(k uint16) (container, bool) {
	i := sort.Search(len(r.runs), func(i int) bool {
		return uint32(r.runs[i].start)+uint32(r.runs[i].length) >= uint32(k)
	})
	if i >= len(r.runs) || r.runs[i].start > k {
		return r, false
	}
	run := r.runs[i]
	end := uint32(run.start) + uint32(run.length)
	switch {
	case run.start == k && end == uint32(k):
		r.runs = append(r.runs[:i], r.runs[i+1:]...)
	case run.start == k:
		r.runs[i].start++
		r.runs[i].length--
	case end == uint32(k):
		r.runs[i].length--
	default:
		// Split the run in two.
		right := interval{start: k + 1, length: uint16(end - uint32(k) - 1)}
		r.runs[i].length = uint16(uint32(k) - uint32(run.start) - 1)
		r.runs = append(r.runs, interval{})
		copy(r.runs[i+2:], r.runs[i+1:])
		r.runs[i+1] = right
	}
	return r, true
}

func (r *runContainer) cardinality() int {
	total := 0
	for _, run := range r.runs {
		total += int(run.length) + 1
	}
	return total
}

func (r *runContainer) memBytes() int { return sliceHeaderBytes + 4*cap(r.runs) }

func (r *runContainer) clone() container {
	out := &runContainer{runs: make([]interval, len(r.runs))}
	copy(out.runs, r.runs)
	return out
}

func (r *runContainer) foreach(f func(uint16)) {
	for _, run := range r.runs {
		for k := uint32(run.start); k <= uint32(run.start)+uint32(run.length); k++ {
			f(uint16(k))
		}
	}
}

func (r *runContainer) optimize() container {
	a := &arrayContainer{}
	if r.cardinality() <= arrayMax {
		a.keys = make([]uint16, 0, r.cardinality())
		r.foreach(func(k uint16) { a.keys = append(a.keys, k) })
		return a.optimize()
	}
	b := &bitmapContainer{}
	r.foreach(func(k uint16) { b.bits[k>>6] |= 1 << (k & 63) })
	b.card = r.cardinality()
	return b.optimize()
}

// ---------------------------------------------------------------------------
// Set: sharded, concurrency-safe sparse set with memory accounting.
// ---------------------------------------------------------------------------

// Set is a sparse set of uint32 keys with adaptive per-shard containers.
// The zero value is ready to use.
type Set struct {
	mu   sync.RWMutex
	cs   map[uint32]container
	card int
}

func (s *Set) lazy() {
	if s.cs == nil {
		s.cs = make(map[uint32]container)
	}
}

// Add inserts key; it reports whether the key was not already present.
func (s *Set) Add(key uint32) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lazy()
	hi, lo := key>>ContainerBits, uint16(key)
	c := s.cs[hi]
	if c == nil {
		c = &arrayContainer{}
	}
	c, added := c.add(lo)
	if !added {
		return false
	}
	s.cs[hi] = c
	s.card++
	return true
}

// Remove deletes key; it reports whether the key was present.
func (s *Set) Remove(key uint32) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	hi, lo := key>>ContainerBits, uint16(key)
	c := s.cs[hi]
	if c == nil {
		return false
	}
	c, removed := c.remove(lo)
	if !removed {
		return false
	}
	if c.cardinality() == 0 {
		delete(s.cs, hi)
	} else {
		s.cs[hi] = c
	}
	s.card--
	return true
}

// Contains reports whether key is in the set.
func (s *Set) Contains(key uint32) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c := s.cs[key>>ContainerBits]
	return c != nil && c.get(uint16(key))
}

// Cardinality returns the number of keys in the set.
func (s *Set) Cardinality() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.card
}

// ContainerCount returns the number of live shards.
func (s *Set) ContainerCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.cs)
}

// MemBytes returns the exact accounted memory footprint in bytes.
func (s *Set) MemBytes() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	total := 64 // map + struct overhead baseline
	for hi, c := range s.cs {
		_ = hi
		total += 16 + c.memBytes() // map entry + container
	}
	return total
}

// Optimize converts every shard to its most compact encoding and releases
// spare capacity. It returns the number of bytes reclaimed.
func (s *Set) Optimize() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	before := 64
	for hi, c := range s.cs {
		_ = hi
		before += 16 + c.memBytes()
	}
	for hi, c := range s.cs {
		if c.cardinality() == 0 {
			delete(s.cs, hi)
			continue
		}
		s.cs[hi] = c.optimize()
	}
	after := 64
	for hi, c := range s.cs {
		_ = hi
		after += 16 + c.memBytes()
	}
	return before - after
}

// Clear removes all keys and all shards, returning memory to baseline.
func (s *Set) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cs = nil
	s.card = 0
}

// Clone returns a deep copy of the set.
func (s *Set) Clone() *Set {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := &Set{cs: make(map[uint32]container, len(s.cs)), card: s.card}
	for hi, c := range s.cs {
		out.cs[hi] = c.clone()
	}
	return out
}

// Range iterates keys in ascending order until f returns false.
func (s *Set) Range(f func(key uint32) bool) {
	s.mu.RLock()
	keys := make([]uint32, 0, len(s.cs))
	for hi := range s.cs {
		keys = append(keys, hi)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	cs := make([]container, 0, len(keys))
	for _, hi := range keys {
		cs = append(cs, s.cs[hi])
	}
	s.mu.RUnlock()
	for i, hi := range keys {
		stop := false
		cs[i].foreach(func(lo uint16) {
			if !stop && !f(hi<<ContainerBits|uint32(lo)) {
				stop = true
			}
		})
		if stop {
			return
		}
	}
}

// ToSlice returns all keys in ascending order.
func (s *Set) ToSlice() []uint32 {
	out := make([]uint32, 0, s.Cardinality())
	s.Range(func(k uint32) bool {
		out = append(out, k)
		return true
	})
	return out
}

// Equals reports whether both sets contain exactly the same keys.
func (s *Set) Equals(other *Set) bool {
	if s.Cardinality() != other.Cardinality() {
		return false
	}
	if s.ContainerCount() != other.ContainerCount() {
		return false
	}
	equal := true
	s.Range(func(k uint32) bool {
		if !other.Contains(k) {
			equal = false
			return false
		}
		return true
	})
	return equal
}
