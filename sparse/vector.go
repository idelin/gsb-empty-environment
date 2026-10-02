// Package sparse provides a memory-adaptive container for massive sparse
// int64 vectors indexed by uint32. It switches between a dense slice and a
// sparse map representation based on measured density, with hysteresis to
// avoid thrashing, and supports varint/RLE compressed snapshots.
package sparse

import (
	"encoding/binary"
	"errors"
	"sort"
	"sync"
)

const (
	// sparseEntryBytes estimates the amortized bytes of one map entry
	// (key + value + bucket overhead) in Go's runtime map.
	sparseEntryBytes = 32
	// denseValueBytes is the size of one dense slot.
	denseValueBytes = 8
	// maxDenseSpan caps dense conversion so a single outlier index cannot
	// force a gigantic allocation.
	maxDenseSpan = 1 << 24
	// hysteresis: convert dense->sparse only when sparse is at least 2x
	// cheaper, and sparse->dense only when dense is strictly cheaper.
	sparseToDenseFactor = 1.0
	denseToSparseFactor = 0.5
)

// Vector is an adaptive sparse vector. The zero value is ready to use, but
// prefer New for clarity. Safe for concurrent use.
type Vector struct {
	mu     sync.RWMutex
	dense  bool
	base   uint32           // dense: index of dvals[0]
	dvals  []int64          // dense storage
	svals  map[uint32]int64 // sparse storage
	count  int              // number of explicitly set (non-zero) entries
	maxIdx uint32
	hasIdx bool
}

// New returns an empty adaptive vector.
func New() *Vector {
	return &Vector{svals: make(map[uint32]int64)}
}

// Len returns the number of explicitly set entries.
func (v *Vector) Len() int {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.count
}

// IsDense reports the current storage mode (observability/test aid).
func (v *Vector) IsDense() bool {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.dense
}

// MemEstimate returns the estimated bytes used by the current
// representation, used by tests to assert compactness.
func (v *Vector) MemEstimate() int {
	v.mu.RLock()
	defer v.mu.RUnlock()
	if v.dense {
		return len(v.dvals) * denseValueBytes
	}
	return v.count * sparseEntryBytes
}

// Get returns the value at idx (0 if unset).
func (v *Vector) Get(idx uint32) int64 {
	v.mu.RLock()
	defer v.mu.RUnlock()
	if v.dense {
		off := int64(idx) - int64(v.base)
		if off < 0 || off >= int64(len(v.dvals)) {
			return 0
		}
		return v.dvals[off]
	}
	return v.svals[idx]
}

// Set assigns val at idx. Setting 0 deletes the entry.
func (v *Vector) Set(idx uint32, val int64) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if val == 0 {
		v.deleteLocked(idx)
	} else {
		v.setLocked(idx, val)
	}
	v.maybeConvertLocked()
}

func (v *Vector) setLocked(idx uint32, val int64) {
	if v.dense {
		off := int64(idx) - int64(v.base)
		if off >= 0 && off < int64(len(v.dvals)) {
			if v.dvals[off] == 0 {
				v.count++
			}
			v.dvals[off] = val
		} else {
			// Outside the dense window: grow or fall back to sparse.
			v.growDenseLocked(idx)
			if v.dense {
				off = int64(idx) - int64(v.base)
				if v.dvals[off] == 0 {
					v.count++
				}
				v.dvals[off] = val
			} else {
				if _, ok := v.svals[idx]; !ok {
					v.count++
				}
				v.svals[idx] = val
			}
		}
	} else {
		if _, ok := v.svals[idx]; !ok {
			v.count++
		}
		v.svals[idx] = val
	}
	if !v.hasIdx || idx > v.maxIdx {
		v.maxIdx = idx
		v.hasIdx = true
	}
}

func (v *Vector) deleteLocked(idx uint32) {
	if v.dense {
		off := int64(idx) - int64(v.base)
		if off >= 0 && off < int64(len(v.dvals)) && v.dvals[off] != 0 {
			v.dvals[off] = 0
			v.count--
		}
	} else {
		if _, ok := v.svals[idx]; ok {
			delete(v.svals, idx)
			v.count--
		}
	}
}

// growDenseLocked extends the dense window to include idx.
func (v *Vector) growDenseLocked(idx uint32) {
	newBase := v.base
	if idx < v.base {
		newBase = idx
	}
	newEnd := idx + 1
	if old := v.base + uint32(len(v.dvals)); old > newEnd {
		newEnd = old
	}
	span := int64(newEnd) - int64(newBase)
	if span > maxDenseSpan {
		// Refuse the huge allocation: downgrade to sparse instead.
		v.toSparseLocked()
		return
	}
	if newBase == v.base && span <= int64(cap(v.dvals)) {
		// Amortized growth: the backing array already has room; reslice in
		// place. Tail slots are always zero (fresh capacity or zeroed by
		// deletes), so window semantics are preserved.
		v.dvals = v.dvals[:span]
		return
	}
	// Geometric growth keeps repeated appends O(1) amortized.
	growSpan := span
	if dbl := 2 * int64(len(v.dvals)); growSpan < dbl && newBase == v.base {
		growSpan = dbl
	}
	if growSpan > maxDenseSpan {
		growSpan = span
	}
	nd := make([]int64, span, growSpan)
	copy(nd[int64(v.base)-int64(newBase):], v.dvals)
	v.base, v.dvals = newBase, nd
}

// maybeConvertLocked applies the adaptive conversion policy.
func (v *Vector) maybeConvertLocked() {
	if v.count == 0 {
		if v.dense {
			v.toSparseLocked()
		}
		return
	}
	span := int64(v.maxIdx) + 1
	if v.dense {
		span = int64(len(v.dvals))
		denseMem := span * denseValueBytes
		sparseMem := int64(v.count) * sparseEntryBytes
		if float64(sparseMem) < float64(denseMem)*denseToSparseFactor {
			v.toSparseLocked()
		}
		return
	}
	// sparse mode
	if span > maxDenseSpan {
		return
	}
	denseMem := span * denseValueBytes
	sparseMem := int64(v.count) * sparseEntryBytes
	if float64(denseMem) <= float64(sparseMem)*sparseToDenseFactor {
		v.toDenseLocked()
	}
}

func (v *Vector) toDenseLocked() {
	// Rebase around actual data to avoid a zero-anchored huge span.
	lo, hi := uint32(0), uint32(0)
	first := true
	for idx := range v.svals {
		if first || idx < lo {
			lo = idx
		}
		if first || idx > hi {
			hi = idx
		}
		first = false
	}
	if first {
		return
	}
	nd := make([]int64, int64(hi)-int64(lo)+1)
	for idx, val := range v.svals {
		nd[idx-lo] = val
	}
	v.dense = true
	v.base = lo
	v.dvals = nd
	v.svals = nil
}

func (v *Vector) toSparseLocked() {
	m := make(map[uint32]int64, v.count)
	for i, val := range v.dvals {
		if val != 0 {
			m[v.base+uint32(i)] = val
		}
	}
	v.dense = false
	v.dvals = nil
	v.svals = m
}

// Range iterates over all non-zero entries in ascending index order.
// The callback must not call back into the Vector.
func (v *Vector) Range(fn func(idx uint32, val int64) bool) {
	v.mu.RLock()
	entries := make([]struct {
		idx uint32
		val int64
	}, 0, v.count)
	if v.dense {
		for i, val := range v.dvals {
			if val != 0 {
				entries = append(entries, struct {
					idx uint32
					val int64
				}{v.base + uint32(i), val})
			}
		}
	} else {
		for idx, val := range v.svals {
			entries = append(entries, struct {
				idx uint32
				val int64
			}{idx, val})
		}
	}
	v.mu.RUnlock()
	sort.Slice(entries, func(i, j int) bool { return entries[i].idx < entries[j].idx })
	for _, e := range entries {
		if !fn(e.idx, e.val) {
			return
		}
	}
}

// --- Compressed snapshots -------------------------------------------------

const (
	encSparseVarint byte = 1 // delta-varint indices + zigzag values
	encRLE          byte = 2 // (run-length, value) pairs over dense window
)

// Compress serializes the vector into a compact byte form, choosing the
// smaller of delta-varint and RLE encodings.
func (v *Vector) Compress() []byte {
	v.mu.RLock()
	defer v.mu.RUnlock()
	// Collect sorted non-zero entries.
	type ent struct {
		idx uint32
		val int64
	}
	entries := make([]ent, 0, v.count)
	if v.dense {
		for i, val := range v.dvals {
			if val != 0 {
				entries = append(entries, ent{v.base + uint32(i), val})
			}
		}
	} else {
		for idx, val := range v.svals {
			entries = append(entries, ent{idx, val})
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].idx < entries[j].idx })

	// Candidate 1: delta-varint.
	var varintBuf []byte
	tmp := make([]byte, binary.MaxVarintLen64)
	varintBuf = append(varintBuf, encSparseVarint)
	n := binary.PutUvarint(tmp, uint64(len(entries)))
	varintBuf = append(varintBuf, tmp[:n]...)
	prev := uint64(0)
	for _, e := range entries {
		n = binary.PutUvarint(tmp, uint64(e.idx)-prev)
		varintBuf = append(varintBuf, tmp[:n]...)
		prev = uint64(e.idx)
		n = binary.PutVarint(tmp, e.val)
		varintBuf = append(varintBuf, tmp[:n]...)
	}

	// Candidate 2: RLE over the dense window [lo, hi].
	var rleBuf []byte
	if len(entries) > 0 {
		lo, hi := entries[0].idx, entries[len(entries)-1].idx
		span := uint64(hi) - uint64(lo) + 1
		if span <= maxDenseSpan {
			rleBuf = append(rleBuf, encRLE)
			n = binary.PutUvarint(tmp, uint64(lo))
			rleBuf = append(rleBuf, tmp[:n]...)
			n = binary.PutUvarint(tmp, span)
			rleBuf = append(rleBuf, tmp[:n]...)
			runs := encodeRLE(v.gatherWindow(lo, hi), tmp)
			rleBuf = append(rleBuf, runs...)
		}
	}
	if rleBuf != nil && len(rleBuf) < len(varintBuf) {
		return rleBuf
	}
	return varintBuf
}

// gatherWindow returns the values in [lo, hi] (caller holds the lock).
func (v *Vector) gatherWindow(lo, hi uint32) []int64 {
	out := make([]int64, int64(hi)-int64(lo)+1)
	if v.dense {
		for i := range out {
			idx := lo + uint32(i)
			off := int64(idx) - int64(v.base)
			if off >= 0 && off < int64(len(v.dvals)) {
				out[i] = v.dvals[off]
			}
		}
	} else {
		for i := range out {
			out[i] = v.svals[lo+uint32(i)]
		}
	}
	return out
}

// encodeRLE emits (count, value) varint pairs; zero runs use negative-free
// encoding: count then zigzag value.
func encodeRLE(vals []int64, tmp []byte) []byte {
	var out []byte
	i := 0
	for i < len(vals) {
		j := i + 1
		for j < len(vals) && vals[j] == vals[i] {
			j++
		}
		n := binary.PutUvarint(tmp, uint64(j-i))
		out = append(out, tmp[:n]...)
		n = binary.PutVarint(tmp, vals[i])
		out = append(out, tmp[:n]...)
		i = j
	}
	return out
}

// ErrBadEncoding is returned by Decompress for malformed input.
var ErrBadEncoding = errors.New("sparse: malformed compressed vector")

// Decompress restores a vector produced by Compress.
func Decompress(data []byte) (*Vector, error) {
	if len(data) < 1 {
		return nil, ErrBadEncoding
	}
	v := New()
	switch data[0] {
	case encSparseVarint:
		data = data[1:]
		count, n := binary.Uvarint(data)
		if n <= 0 {
			return nil, ErrBadEncoding
		}
		data = data[n:]
		prev := uint64(0)
		for i := uint64(0); i < count; i++ {
			delta, n := binary.Uvarint(data)
			if n <= 0 {
				return nil, ErrBadEncoding
			}
			data = data[n:]
			val, n := binary.Varint(data)
			if n <= 0 {
				return nil, ErrBadEncoding
			}
			data = data[n:]
			prev += delta
			if prev > 0xFFFFFFFF {
				return nil, ErrBadEncoding
			}
			v.Set(uint32(prev), val)
		}
		return v, nil
	case encRLE:
		data = data[1:]
		lo, n := binary.Uvarint(data)
		if n <= 0 {
			return nil, ErrBadEncoding
		}
		data = data[n:]
		span, n := binary.Uvarint(data)
		if n <= 0 || span > maxDenseSpan {
			return nil, ErrBadEncoding
		}
		data = data[n:]
		idx := lo
		end := lo + span
		for idx < end {
			run, n := binary.Uvarint(data)
			if n <= 0 {
				return nil, ErrBadEncoding
			}
			data = data[n:]
			val, n := binary.Varint(data)
			if n <= 0 {
				return nil, ErrBadEncoding
			}
			data = data[n:]
			if idx+run > end {
				return nil, ErrBadEncoding
			}
			if val != 0 {
				for k := uint64(0); k < run; k++ {
					v.Set(uint32(idx+k), val)
				}
			}
			idx += run
		}
		return v, nil
	}
	return nil, ErrBadEncoding
}
