// Package sparse provides a memory-compact container for massive sparse
// int64 data indexed by uint64.
//
// The Vector adaptively converts between two representations:
//
//   - sparse: sorted key/value arrays (optionally delta+varint compressed
//     into a packed byte stream via Compact);
//   - dense:  value array + presence bitmap over the key span.
//
// An optimizer estimates the memory footprint of both representations after
// every mutation and converts (with hysteresis) to the cheaper one, giving
// near-optimal space utilisation for highly skewed data distributions.
//
// All methods are safe for concurrent use. The package spawns no goroutines.
package sparse

import (
	"math/bits"
	"sort"
	"sync"
)

// hysteresis: switch representation only when the alternative is at least
// 20% cheaper, to avoid flapping near the break-even point.
const hysteresisNum, hysteresisDen = 4, 5 // 0.8

// maxDenseSpan caps the key span a dense container may cover (2^26 slots
// ~= 512 MiB of values). Sets that would grow the dense container beyond
// this span force a conversion back to the sparse representation first,
// which also makes uint64 span overflow unreachable.
const maxDenseSpan = 1 << 26

type container interface {
	get(i uint64) (int64, bool)
	set(i uint64, v int64)
	del(i uint64)
	count() int
	span() (lo, hi uint64, ok bool)
	bytes() int
	forEach(fn func(k uint64, v int64) bool)
}

// ---------------------------------------------------------------------------
// Sparse container: bucketed sorted arrays, optionally packed (compressed).
//
// Keys are sharded into buckets of 2^bucketShift consecutive indices; each
// bucket keeps sorted key/value arrays so inserts are O(bucket) and lookups
// O(log bucket). Buckets themselves live in a hash map, giving amortised
// O(1) inserts even for hundreds of millions of sparse entries.
// ---------------------------------------------------------------------------

const bucketShift = 8 // 256 consecutive keys per bucket

type bucket struct {
	keys []uint64 // sorted ascending, relative offsets within the bucket
	vals []int64
}

func (b *bucket) search(off uint64) (int, bool) {
	p := sort.Search(len(b.keys), func(k int) bool { return b.keys[k] >= off })
	if p < len(b.keys) && b.keys[p] == off {
		return p, true
	}
	return p, false
}

type sparseContainer struct {
	buckets map[uint64]*bucket // bucket id (key >> bucketShift) -> bucket
	minB    uint64
	maxB    uint64
	n       int
	packed  []byte // delta+varint stream; when non-nil buckets are empty
}

func newSparseContainer() *sparseContainer {
	return &sparseContainer{buckets: make(map[uint64]*bucket)}
}

func (s *sparseContainer) unpacked() bool { return s.packed == nil }

func (s *sparseContainer) ensureUnpacked() {
	if s.packed == nil {
		return
	}
	keys, vals := decompress(s.packed)
	s.packed = nil
	s.buckets = make(map[uint64]*bucket)
	s.n = 0
	for i, k := range keys {
		s.set(k, vals[i])
	}
}

func (s *sparseContainer) count() int {
	if s.packed != nil {
		n, _ := packedCount(s.packed)
		return n
	}
	return s.n
}

func (s *sparseContainer) get(i uint64) (int64, bool) {
	if s.packed != nil {
		return packedGet(s.packed, i)
	}
	b, ok := s.buckets[i>>bucketShift]
	if !ok {
		return 0, false
	}
	if p, ok := b.search(i & (1<<bucketShift - 1)); ok {
		return b.vals[p], true
	}
	return 0, false
}

func (s *sparseContainer) set(i uint64, v int64) {
	s.ensureUnpacked()
	id, off := i>>bucketShift, i&(1<<bucketShift-1)
	b, ok := s.buckets[id]
	if !ok {
		b = &bucket{}
		s.buckets[id] = b
		if s.n == 0 || id < s.minB {
			s.minB = id
		}
		if s.n == 0 || id > s.maxB {
			s.maxB = id
		}
	}
	p, found := b.search(off)
	if found {
		b.vals[p] = v
		return
	}
	b.keys = append(b.keys, 0)
	b.vals = append(b.vals, 0)
	copy(b.keys[p+1:], b.keys[p:])
	copy(b.vals[p+1:], b.vals[p:])
	b.keys[p], b.vals[p] = off, v
	s.n++
}

func (s *sparseContainer) del(i uint64) {
	s.ensureUnpacked()
	id, off := i>>bucketShift, i&(1<<bucketShift-1)
	b, ok := s.buckets[id]
	if !ok {
		return
	}
	p, found := b.search(off)
	if !found {
		return
	}
	b.keys = append(b.keys[:p], b.keys[p+1:]...)
	b.vals = append(b.vals[:p], b.vals[p+1:]...)
	s.n--
	if len(b.keys) == 0 {
		delete(s.buckets, id)
		if s.n > 0 && (id == s.minB || id == s.maxB) {
			s.recomputeBounds()
		}
	}
}

func (s *sparseContainer) recomputeBounds() {
	first := true
	for id := range s.buckets {
		if first || id < s.minB {
			s.minB = id
		}
		if first || id > s.maxB {
			s.maxB = id
		}
		first = false
	}
}

func (s *sparseContainer) span() (lo, hi uint64, ok bool) {
	if s.packed != nil {
		return packedSpan(s.packed)
	}
	if s.n == 0 {
		return 0, 0, false
	}
	loB, hiB := s.buckets[s.minB], s.buckets[s.maxB]
	lo = s.minB<<bucketShift | loB.keys[0]
	hi = s.maxB<<bucketShift | hiB.keys[len(hiB.keys)-1]
	return lo, hi, true
}

func (s *sparseContainer) bytes() int {
	const overhead = 96 // struct + map header
	if s.packed != nil {
		return overhead + cap(s.packed)
	}
	total := overhead + len(s.buckets)*64 // map entries + bucket headers
	for _, b := range s.buckets {
		total += 8*cap(b.keys) + 8*cap(b.vals)
	}
	return total
}

func (s *sparseContainer) forEach(fn func(k uint64, v int64) bool) {
	if s.packed != nil {
		packedForEach(s.packed, fn)
		return
	}
	ids := make([]uint64, 0, len(s.buckets))
	for id := range s.buckets {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		b := s.buckets[id]
		base := id << bucketShift
		for i := range b.keys {
			if !fn(base|b.keys[i], b.vals[i]) {
				return
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Dense container: value array + presence bitmap over [base, base+len).
// ---------------------------------------------------------------------------

type denseContainer struct {
	base uint64
	vals []int64
	bits []uint64
	npop int
}

func (d *denseContainer) count() int { return d.npop }

func (d *denseContainer) get(i uint64) (int64, bool) {
	off := i - d.base
	if i < d.base || off >= uint64(len(d.vals)) {
		return 0, false
	}
	if d.bits[off/64]&(1<<(off%64)) == 0 {
		return 0, false
	}
	return d.vals[off], true
}

func (d *denseContainer) growTo(i uint64) {
	if len(d.vals) == 0 {
		d.base = i
		d.vals = make([]int64, 1)
		d.bits = make([]uint64, 1)
		return
	}
	lo, hi := d.base, d.base+uint64(len(d.vals))-1
	if i >= lo && i <= hi {
		return
	}
	nlo, nhi := lo, hi
	if i < nlo {
		nlo = i
	}
	if i > nhi {
		nhi = i
	}
	nlen := nhi - nlo + 1
	nvals := make([]int64, nlen)
	nbits := make([]uint64, (nlen+63)/64)
	shift := lo - nlo
	copy(nvals[shift:], d.vals)
	for k := uint64(0); k < uint64(len(d.vals)); k++ {
		if d.bits[k/64]&(1<<(k%64)) != 0 {
			pos := k + shift
			nbits[pos/64] |= 1 << (pos % 64)
		}
	}
	d.base, d.vals, d.bits = nlo, nvals, nbits
}

func (d *denseContainer) set(i uint64, v int64) {
	d.growTo(i)
	off := i - d.base
	if d.bits[off/64]&(1<<(off%64)) == 0 {
		d.npop++
	}
	d.bits[off/64] |= 1 << (off % 64)
	d.vals[off] = v
}

func (d *denseContainer) del(i uint64) {
	off := i - d.base
	if i < d.base || off >= uint64(len(d.vals)) {
		return
	}
	if d.bits[off/64]&(1<<(off%64)) != 0 {
		d.npop--
		d.bits[off/64] &^= 1 << (off % 64)
	}
}

func (d *denseContainer) span() (lo, hi uint64, ok bool) {
	if d.npop == 0 {
		return 0, 0, false
	}
	for w := 0; w < len(d.bits); w++ {
		if d.bits[w] != 0 {
			lo = d.base + uint64(w)*64 + uint64(bits.TrailingZeros64(d.bits[w]))
			break
		}
	}
	for w := len(d.bits) - 1; w >= 0; w-- {
		if d.bits[w] != 0 {
			hi = d.base + uint64(w)*64 + uint64(63-bits.LeadingZeros64(d.bits[w]))
			break
		}
	}
	return lo, hi, true
}

func (d *denseContainer) bytes() int {
	const overhead = 64
	return overhead + 8*cap(d.vals) + 8*cap(d.bits)
}

func (d *denseContainer) forEach(fn func(k uint64, v int64) bool) {
	for off := uint64(0); off < uint64(len(d.vals)); off++ {
		if d.bits[off/64]&(1<<(off%64)) != 0 {
			if !fn(d.base+off, d.vals[off]) {
				return
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Vector: adaptive container with memory optimizer.
// ---------------------------------------------------------------------------

// Vector is a sparse int64 vector with adaptive, memory-optimal storage.
type Vector struct {
	mu    sync.RWMutex
	c     container
	dense bool
}

// New returns an empty Vector.
func New() *Vector { return &Vector{c: newSparseContainer()} }

// Get returns the value at index i.
func (v *Vector) Get(i uint64) (int64, bool) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.c.get(i)
}

// Set stores val at index i and re-runs the memory optimizer.
func (v *Vector) Set(i uint64, val int64) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if d, ok := v.c.(*denseContainer); ok && len(d.vals) > 0 {
		lo := d.base
		hi := d.base + uint64(len(d.vals)) - 1
		if i < lo || i > hi {
			nlo, nhi := lo, hi
			if i < nlo {
				nlo = i
			}
			if i > nhi {
				nhi = i
			}
			if nhi-nlo >= maxDenseSpan {
				v.convertLocked(false)
			}
		}
	}
	v.c.set(i, val)
	v.optimizeLocked()
}

// Delete removes index i and re-runs the memory optimizer.
func (v *Vector) Delete(i uint64) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.c.del(i)
	v.optimizeLocked()
}

// Count returns the number of stored entries.
func (v *Vector) Count() int {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.c.count()
}

// Bytes estimates the current memory footprint in bytes.
func (v *Vector) Bytes() int {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.c.bytes()
}

// Mode describes the active representation: "sparse", "sparse-packed" or "dense".
func (v *Vector) Mode() string {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.modeLocked()
}

func (v *Vector) modeLocked() string {
	if v.dense {
		return "dense"
	}
	if s, ok := v.c.(*sparseContainer); ok && !s.unpacked() {
		return "sparse-packed"
	}
	return "sparse"
}

// Density returns count/span in [0,1]; 0 for an empty vector.
func (v *Vector) Density() float64 {
	v.mu.RLock()
	defer v.mu.RUnlock()
	lo, hi, ok := v.c.span()
	if !ok {
		return 0
	}
	span := spanSize(lo, hi)
	return float64(v.c.count()) / span
}

// ForEach iterates entries in ascending key order; returning false stops.
func (v *Vector) ForEach(fn func(k uint64, val int64) bool) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	v.c.forEach(fn)
}

// Compact forces the most compact representation: converts to sparse if
// needed and compresses the entry stream with delta+varint encoding.
func (v *Vector) Compact() {
	v.mu.Lock()
	defer v.mu.Unlock()
	var keys []uint64
	var vals []int64
	v.c.forEach(func(k uint64, val int64) bool {
		keys = append(keys, k)
		vals = append(vals, val)
		return true
	})
	s := newSparseContainer()
	s.packed = compress(keys, vals)
	v.c, v.dense = s, false
}

// spanSize returns hi-lo+1 as a float, safe against uint64 overflow.
func spanSize(lo, hi uint64) float64 {
	if hi-lo == ^uint64(0) {
		return 1.8446744073709552e19 // 2^64
	}
	return float64(hi-lo) + 1
}

// optimizeLocked converts between representations when the alternative is
// meaningfully cheaper (estimated bytes, with hysteresis).
func (v *Vector) optimizeLocked() {
	count := v.c.count()
	if count == 0 {
		if v.dense {
			v.c, v.dense = newSparseContainer(), false
		}
		return
	}
	lo, hi, ok := v.c.span()
	if !ok {
		return
	}
	span := spanSize(lo, hi)
	denseEst := span*8 + span/8 // values + bitmap
	var sparseEst float64
	if s, ok2 := v.c.(*sparseContainer); ok2 && !s.unpacked() {
		sparseEst = float64(len(s.packed))
	} else {
		sparseEst = float64(count) * 16
	}

	if !v.dense && denseEst*hysteresisDen < sparseEst*hysteresisNum {
		v.convertLocked(true)
	} else if v.dense && sparseEst*hysteresisDen < denseEst*hysteresisNum {
		v.convertLocked(false)
	}
}

func (v *Vector) convertLocked(toDense bool) {
	if toDense {
		d := &denseContainer{}
		v.c.forEach(func(k uint64, val int64) bool {
			d.set(k, val)
			return true
		})
		v.c, v.dense = d, true
		return
	}
	s := newSparseContainer()
	v.c.forEach(func(k uint64, val int64) bool {
		s.set(k, val)
		return true
	})
	v.c, v.dense = s, false
}

// SortKeys is a helper kept for tests: returns keys in ascending order.
func (v *Vector) SortKeys() []uint64 {
	v.mu.RLock()
	defer v.mu.RUnlock()
	var out []uint64
	v.c.forEach(func(k uint64, _ int64) bool {
		out = append(out, k)
		return true
	})
	return out
}
