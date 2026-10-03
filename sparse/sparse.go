// Package sparse implements a memory-adaptive container for massive
// sparse uint64 data, plus a compact varint-delta compressed snapshot
// format. The container switches between a hash-map representation
// (sparse regime) and a dense slice + occupancy bitmap (dense regime)
// with hysteresis, so space usage stays near-optimal for the current
// data distribution. All operations are safe for concurrent use.
package sparse

import (
	"errors"
	"sort"
	"sync"

	"wireopt/wire"
)

const (
	// maxDenseSpan caps the dense representation so a single
	// outlier index cannot force a gigantic allocation.
	maxDenseSpan = 1 << 20

	// sparseEntryCost approximates the bytes one live entry costs
	// inside a Go map (key, value, bucket overhead).
	sparseEntryCost = 48
)

// ErrCorrupt is returned when a compressed snapshot is malformed.
var ErrCorrupt = errors.New("sparse: corrupt compressed snapshot")

// Array is a memory-adaptive sparse array of uint64 values indexed
// by uint32. The zero value is ready to use.
type Array struct {
	mu      sync.RWMutex
	sparse  map[uint32]uint64 // sparse representation
	dense   []uint64          // dense values (offset by base)
	bits    []uint64          // dense occupancy bitmap
	base    uint32            // index of dense[0]
	count   int
	min     uint32
	max     uint32
	isDense bool
}

// New returns an empty adaptive array.
func New() *Array { return &Array{} }

// Len returns the number of live entries.
func (a *Array) Len() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.count
}

// Dense reports whether the container currently uses the dense
// representation (exposed for observability and tests).
func (a *Array) Dense() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.isDense
}

// Span returns max-min+1, or 0 when empty.
func (a *Array) Span() uint64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.count == 0 {
		return 0
	}
	return uint64(a.max) - uint64(a.min) + 1
}

// MemBytes estimates the bytes used by the current representation.
func (a *Array) MemBytes() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.memBytesLocked()
}

func (a *Array) memBytesLocked() int {
	if a.isDense {
		return len(a.dense)*8 + len(a.bits)*8 + 64
	}
	return a.count*sparseEntryCost + 64
}

// Get returns the value at idx and whether it is present.
func (a *Array) Get(idx uint32) (uint64, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.isDense {
		off := int64(idx) - int64(a.base)
		if off < 0 || off >= int64(len(a.dense)) {
			return 0, false
		}
		if a.bits[off>>6]&(1<<uint(off&63)) == 0 {
			return 0, false
		}
		return a.dense[off], true
	}
	v, ok := a.sparse[idx]
	return v, ok
}

// Set inserts or replaces the value at idx, converting the
// representation when the data distribution crosses a threshold.
func (a *Array) Set(idx uint32, v uint64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.isDense {
		a.setDense(idx, v)
	} else {
		if a.sparse == nil {
			a.sparse = make(map[uint32]uint64)
		}
		if _, ok := a.sparse[idx]; !ok {
			a.count++
			a.growBounds(idx)
		}
		a.sparse[idx] = v
	}
	a.maybeConvert()
}

func (a *Array) setDense(idx uint32, v uint64) {
	off := int64(idx) - int64(a.base)
	if off < 0 || off >= int64(len(a.dense)) {
		// Outside the dense window: grow it in place when the
		// resulting span stays dense-worthy, else fall back.
		newMin, newMax := a.min, a.max
		if idx < newMin {
			newMin = idx
		}
		if idx > newMax {
			newMax = idx
		}
		needed := uint64(newMax) - uint64(newMin) + 1
		newSpan := needed
		if doubled := uint64(len(a.dense)) * 2; doubled > newSpan {
			newSpan = doubled // geometric growth avoids O(n^2) copies
		}
		if newSpan > maxDenseSpan {
			newSpan = needed
		}
		if uint64(newMin)+newSpan > 1<<32 {
			newSpan = 1<<32 - uint64(newMin) // never wrap the index space
		}
		if newSpan <= maxDenseSpan && uint64(a.count+1)*2 >= newSpan {
			a.growDenseLocked(newMin, newSpan)
			a.setDense(idx, v)
			return
		}
		a.toSparseLocked()
		a.sparse[idx] = v
		a.count++
		a.growBounds(idx)
		return
	}
	if a.bits[off>>6]&(1<<uint(off&63)) == 0 {
		a.bits[off>>6] |= 1 << uint(off&63)
		a.count++
		a.growBounds(idx)
	}
	a.dense[off] = v
}

// growDenseLocked re-allocates the dense window over [newMin,
// newMin+newSpan), preserving existing entries.
func (a *Array) growDenseLocked(newMin uint32, newSpan uint64) {
	dense := make([]uint64, newSpan)
	bits := make([]uint64, (newSpan+63)/64)
	shift := int64(a.base) - int64(newMin)
	for off := range a.dense {
		if a.bits[off>>6]&(1<<uint(off&63)) != 0 {
			noff := int64(off) + shift
			dense[noff] = a.dense[off]
			bits[noff>>6] |= 1 << uint(noff&63)
		}
	}
	a.dense = dense
	a.bits = bits
	a.base = newMin
}

// Delete removes idx, reporting whether it was present.
func (a *Array) Delete(idx uint32) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	var ok bool
	if a.isDense {
		off := int64(idx) - int64(a.base)
		if off >= 0 && off < int64(len(a.dense)) &&
			a.bits[off>>6]&(1<<uint(off&63)) != 0 {
			a.bits[off>>6] &^= 1 << uint(off&63)
			a.dense[off] = 0
			a.count--
			ok = true
		}
	} else {
		if _, ok = a.sparse[idx]; ok {
			delete(a.sparse, idx)
			a.count--
		}
	}
	if ok {
		a.recomputeBounds()
		a.maybeConvert()
	}
	return ok
}

func (a *Array) growBounds(idx uint32) {
	if a.count == 1 {
		a.min, a.max = idx, idx
		return
	}
	if idx < a.min {
		a.min = idx
	}
	if idx > a.max {
		a.max = idx
	}
}

func (a *Array) recomputeBounds() {
	if a.count == 0 {
		a.min, a.max = 0, 0
		return
	}
	if a.isDense {
		for int64(a.min)-int64(a.base) >= 0 &&
			a.bits[(int64(a.min)-int64(a.base))>>6]&(1<<uint((int64(a.min)-int64(a.base))&63)) == 0 {
			a.min++
		}
		for int64(a.max)-int64(a.base) < int64(len(a.dense)) &&
			a.bits[(int64(a.max)-int64(a.base))>>6]&(1<<uint((int64(a.max)-int64(a.base))&63)) == 0 {
			a.max--
		}
		return
	}
	// Recompute exactly only when the extremes were evicted.
	if _, ok := a.sparse[a.min]; ok {
		if _, ok := a.sparse[a.max]; ok {
			return
		}
	}
	first := true
	for k := range a.sparse {
		if first || k < a.min {
			a.min = k
		}
		if first || k > a.max {
			a.max = k
		}
		first = false
	}
}

// maybeConvert applies the adaptive conversion policy with
// hysteresis: densify at density >= 1/2, sparsify at density < 1/4.
func (a *Array) maybeConvert() {
	if a.count == 0 {
		if a.isDense {
			a.toSparseLocked()
		}
		return
	}
	span := uint64(a.max) - uint64(a.min) + 1
	if !a.isDense {
		if span <= maxDenseSpan && uint64(a.count)*2 >= span {
			a.toDenseLocked()
		}
		return
	}
	denseSpan := uint64(len(a.dense))
	if uint64(a.count)*4 < denseSpan || denseSpan > maxDenseSpan {
		a.toSparseLocked()
	}
}

func (a *Array) toDenseLocked() {
	span := uint64(a.max) - uint64(a.min) + 1
	a.dense = make([]uint64, span)
	a.bits = make([]uint64, (span+63)/64)
	a.base = a.min
	for k, v := range a.sparse {
		off := k - a.base
		a.dense[off] = v
		a.bits[off>>6] |= 1 << uint(off&63)
	}
	a.sparse = nil
	a.isDense = true
}

func (a *Array) toSparseLocked() {
	m := make(map[uint32]uint64, a.count)
	for off, v := range a.dense {
		if a.bits[off>>6]&(1<<uint(off&63)) != 0 {
			m[a.base+uint32(off)] = v
		}
	}
	a.sparse = m
	a.dense = nil
	a.bits = nil
	a.isDense = false
}

// Range iterates entries in ascending index order until fn returns
// false. fn must not call back into the Array.
func (a *Array) Range(fn func(idx uint32, v uint64) bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.isDense {
		for off, v := range a.dense {
			if a.bits[off>>6]&(1<<uint(off&63)) != 0 {
				if !fn(a.base+uint32(off), v) {
					return
				}
			}
		}
		return
	}
	keys := make([]uint32, 0, len(a.sparse))
	for k := range a.sparse {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	for _, k := range keys {
		if !fn(k, a.sparse[k]) {
			return
		}
	}
}

// Compress encodes the array as:
//
//	varint count
//	count * (varint indexDelta, varint zigzag(value))
//
// exploiting the locality of sparse indices and small values for
// near-optimal space usage on skewed distributions.
func (a *Array) Compress() []byte {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := wire.AppendVarint(nil, uint64(a.count))
	var prev uint32
	first := true
	appendEntry := func(idx uint32, v uint64) bool {
		var delta uint64
		if first {
			delta = uint64(idx)
			first = false
		} else {
			delta = uint64(idx - prev)
		}
		prev = idx
		out = wire.AppendVarint(out, delta)
		out = wire.AppendVarint(out, wire.ZigzagEncode(int64(v)))
		return true
	}
	if a.isDense {
		for off, v := range a.dense {
			if a.bits[off>>6]&(1<<uint(off&63)) != 0 {
				appendEntry(a.base+uint32(off), v)
			}
		}
		return out
	}
	keys := make([]uint32, 0, len(a.sparse))
	for k := range a.sparse {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	for _, k := range keys {
		appendEntry(k, a.sparse[k])
	}
	return out
}

// Decompress rebuilds an Array from Compress output, validating every
// varint and the declared count so corrupt input fails safely.
func Decompress(buf []byte) (*Array, error) {
	count, n, err := wire.ConsumeVarint(buf)
	if err != nil {
		return nil, err
	}
	buf = buf[n:]
	if count > uint64(len(buf)) { // each entry needs >= 2 bytes
		return nil, ErrCorrupt
	}
	a := New()
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sparse = make(map[uint32]uint64, count)
	var prev uint32
	for i := uint64(0); i < count; i++ {
		delta, n, err := wire.ConsumeVarint(buf)
		if err != nil {
			return nil, err
		}
		buf = buf[n:]
		zv, n, err := wire.ConsumeVarint(buf)
		if err != nil {
			return nil, err
		}
		buf = buf[n:]
		var idx uint32
		if i == 0 {
			if delta > uint64(^uint32(0)) {
				return nil, ErrCorrupt
			}
			idx = uint32(delta)
		} else {
			next := uint64(prev) + delta
			if next > uint64(^uint32(0)) {
				return nil, ErrCorrupt
			}
			idx = uint32(next)
		}
		if _, dup := a.sparse[idx]; dup {
			return nil, ErrCorrupt
		}
		a.sparse[idx] = uint64(wire.ZigzagDecode(zv))
		prev = idx
	}
	if len(buf) != 0 {
		return nil, ErrCorrupt
	}
	a.count = int(count)
	if a.count > 0 {
		first := true
		for k := range a.sparse {
			if first || k < a.min {
				a.min = k
			}
			if first || k > a.max {
				a.max = k
			}
			first = false
		}
	}
	a.maybeConvert()
	return a, nil
}
