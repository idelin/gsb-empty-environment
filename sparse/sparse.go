// Package sparse provides a memory-adaptive container for massive sparse
// integer sets. It automatically converts between three backends based on
// the observed data distribution:
//
//   - array:  sorted slice, optimal for very sparse data
//   - rle:    run-length encoding, optimal for clustered/sequential data
//   - bitmap: dense bit vector, optimal for high-density data
//
// Serialized form uses delta + varint compression for minimal wire size.
// All operations are safe for concurrent use.
package sparse

import (
	"errors"
	"fmt"
	"sort"
	"sync"

	"protokit/wire"
)

// Backend identifies the active storage representation.
type Backend int

const (
	BackendArray Backend = iota
	BackendRLE
	BackendBitmap
)

func (b Backend) String() string {
	switch b {
	case BackendArray:
		return "array"
	case BackendRLE:
		return "rle"
	case BackendBitmap:
		return "bitmap"
	}
	return "unknown"
}

// run is one inclusive run [Start, Start+Len).
type run struct {
	Start uint32
	Len   uint32
}

const (
	// arrayMaxSize bounds the array backend before conversion is considered.
	arrayMaxSize = 256
	// bitmapDensity is the cardinality/span ratio above which a bitmap wins.
	bitmapDensity = 0.25
	// rleRunFactor: convert to RLE when runs compress cardinality by >= 4x.
	rleRunFactor = 4
)

// Set is an adaptive, concurrency-safe set of uint32 values.
type Set struct {
	mu      sync.RWMutex
	backend Backend

	array  []uint32 // BackendArray: sorted, deduplicated
	runs   []run    // BackendRLE: sorted, non-overlapping, non-adjacent
	bitmap []uint64 // BackendBitmap: bit i set iff value i present
	card   int
}

// New returns an empty Set.
func New() *Set { return &Set{backend: BackendArray} }

// Backend returns the currently active storage backend.
func (s *Set) Backend() Backend {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.backend
}

// Len returns the number of values in the set.
func (s *Set) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.card
}

// Contains reports whether v is in the set.
func (s *Set) Contains(v uint32) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.contains(v)
}

func (s *Set) contains(v uint32) bool {
	switch s.backend {
	case BackendArray:
		i := sort.Search(len(s.array), func(i int) bool { return s.array[i] >= v })
		return i < len(s.array) && s.array[i] == v
	case BackendRLE:
		i := sort.Search(len(s.runs), func(i int) bool { return s.runs[i].Start+s.runs[i].Len > v })
		return i < len(s.runs) && s.runs[i].Start <= v
	case BackendBitmap:
		w := int(v >> 6)
		return w < len(s.bitmap) && s.bitmap[w]&(1<<(v&63)) != 0
	}
	return false
}

// Add inserts v, converting the backend if the distribution warrants it.
func (s *Set) Add(v uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.contains(v) {
		return
	}
	s.card++
	switch s.backend {
	case BackendArray:
		i := sort.Search(len(s.array), func(i int) bool { return s.array[i] >= v })
		s.array = append(s.array, 0)
		copy(s.array[i+1:], s.array[i:])
		s.array[i] = v
	case BackendRLE:
		s.runs = runsAdd(s.runs, v)
	case BackendBitmap:
		w := int(v >> 6)
		for len(s.bitmap) <= w {
			s.bitmap = append(s.bitmap, 0)
		}
		s.bitmap[w] |= 1 << (v & 63)
	}
	s.maybeConvert()
}

// runsAdd inserts v into a sorted run list, merging adjacent runs.
func runsAdd(rs []run, v uint32) []run {
	// First run whose exclusive end is strictly past v; a run ending exactly
	// at v does not cover v and must be extendable via the previous-run path.
	i := sort.Search(len(rs), func(i int) bool { return rs[i].Start+rs[i].Len > v })
	if i < len(rs) && rs[i].Start <= v {
		return rs // already covered (caller checked, defensive)
	}
	// Extend the previous run forward?
	if i > 0 && rs[i-1].Start+rs[i-1].Len == v {
		rs[i-1].Len++
		// Merge with the next run if now adjacent.
		if i < len(rs) && rs[i-1].Start+rs[i-1].Len == rs[i].Start {
			rs[i-1].Len += rs[i].Len
			rs = append(rs[:i], rs[i+1:]...)
		}
		return rs
	}
	// Extend the next run backward?
	if i < len(rs) && rs[i].Start == v+1 {
		rs[i].Start--
		rs[i].Len++
		return rs
	}
	// Insert a new singleton run.
	rs = append(rs, run{})
	copy(rs[i+1:], rs[i:])
	rs[i] = run{Start: v, Len: 1}
	return rs
}

// maybeConvert evaluates density and switches backends when beneficial.
// Callers must hold the write lock.
func (s *Set) maybeConvert() {
	switch s.backend {
	case BackendArray:
		if len(s.array) <= arrayMaxSize {
			return
		}
		s.convertFromValues(s.array)
	case BackendRLE:
		// RLE -> bitmap if density is high.
		span := uint64(0)
		if len(s.runs) > 0 {
			last := s.runs[len(s.runs)-1]
			span = uint64(last.Start) + uint64(last.Len) - uint64(s.runs[0].Start) + 1
		}
		if span > 0 && float64(s.card)/float64(span) > bitmapDensity {
			s.toBitmap()
		}
	case BackendBitmap:
		// Bitmaps are stable; no downgrade on insert-only workloads.
	}
}

// convertFromValues picks the best backend for a full value list.
func (s *Set) convertFromValues(vals []uint32) {
	if len(vals) == 0 {
		return
	}
	span := uint64(vals[len(vals)-1]) - uint64(vals[0]) + 1
	density := float64(len(vals)) / float64(span)
	if density > bitmapDensity {
		s.toBitmap()
		return
	}
	// Count runs the values would form.
	nRuns := 1
	for i := 1; i < len(vals); i++ {
		if vals[i] != vals[i-1]+1 {
			nRuns++
		}
	}
	if len(vals)/nRuns >= rleRunFactor {
		s.toRLE()
		return
	}
	// Stay as array; just let it grow beyond the soft threshold.
}

func (s *Set) values() []uint32 {
	out := make([]uint32, 0, s.card)
	switch s.backend {
	case BackendArray:
		out = append(out, s.array...)
	case BackendRLE:
		for _, r := range s.runs {
			for v := r.Start; v < r.Start+r.Len; v++ {
				out = append(out, v)
			}
		}
	case BackendBitmap:
		for w, word := range s.bitmap {
			for word != 0 {
				b := word & (-word)
				out = append(out, uint32(w*64)+uint32(bitsTrailingZeros(b)))
				word &^= b
			}
		}
	}
	return out
}

func bitsTrailingZeros(v uint64) int {
	n := 0
	for v&1 == 0 {
		v >>= 1
		n++
	}
	return n
}

func (s *Set) toBitmap() {
	vals := s.values()
	max := vals[len(vals)-1]
	bm := make([]uint64, int(max>>6)+1)
	for _, v := range vals {
		bm[v>>6] |= 1 << (v & 63)
	}
	s.bitmap, s.array, s.runs = bm, nil, nil
	s.backend = BackendBitmap
}

func (s *Set) toRLE() {
	vals := s.values()
	rs := make([]run, 0, len(vals)/rleRunFactor)
	for _, v := range vals {
		rs = runsAdd(rs, v)
	}
	s.runs, s.array, s.bitmap = rs, nil, nil
	s.backend = BackendRLE
}

// Range calls fn for each value in ascending order; fn may return false to stop.
func (s *Set) Range(fn func(v uint32) bool) {
	s.mu.RLock()
	vals := s.values()
	s.mu.RUnlock()
	for _, v := range vals {
		if !fn(v) {
			return
		}
	}
}

// Stats reports memory usage of the active backend in bytes.
func (s *Set) Stats() (backend Backend, bytes int, cardinality int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	switch s.backend {
	case BackendArray:
		bytes = len(s.array) * 4
	case BackendRLE:
		bytes = len(s.runs) * 8
	case BackendBitmap:
		bytes = len(s.bitmap) * 8
	}
	return s.backend, bytes, s.card
}

// ---------- Serialization: delta + varint compression ----------

var errBadEncoding = errors.New("sparse: corrupt encoding")

// Marshal encodes the set as delta-compressed varints preceded by a
// varint cardinality. Values are sorted, so deltas are small and varints
// stay compact regardless of the active backend.
func (s *Set) Marshal() []byte {
	s.mu.RLock()
	vals := s.values()
	s.mu.RUnlock()
	buf := make([]byte, 0, len(vals)*2+5)
	buf = wire.AppendVarint(buf, uint64(len(vals)))
	var prev uint32
	for i, v := range vals {
		delta := v
		if i > 0 {
			delta = v - prev - 1
		}
		buf = wire.AppendVarint(buf, uint64(delta))
		prev = v
	}
	return buf
}

// Unmarshal decodes a buffer produced by Marshal, replacing the set.
func (s *Set) Unmarshal(buf []byte) error {
	r := wire.NewReader(buf)
	n, err := r.ReadVarint()
	if err != nil {
		return err
	}
	if n > uint64(len(buf)) { // each value needs >= 1 byte
		return errBadEncoding
	}
	vals := make([]uint32, 0, n)
	var prev uint32
	for i := uint64(0); i < n; i++ {
		d, err := r.ReadVarint()
		if err != nil {
			return err
		}
		var v uint32
		if i == 0 {
			if d > uint64(^uint32(0)) {
				return errBadEncoding
			}
			v = uint32(d)
		} else {
			next := uint64(prev) + d + 1
			if next > uint64(^uint32(0)) {
				return errBadEncoding
			}
			v = uint32(next)
		}
		vals = append(vals, v)
		prev = v
	}
	if r.Remaining() != 0 {
		return errBadEncoding
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.array, s.runs, s.bitmap = vals, nil, nil
	s.backend = BackendArray
	s.card = len(vals)
	s.convertFromValues(vals)
	return nil
}

// ---------- Snapshot: leak-free deep copy ----------

// Clone returns an independent deep copy; the two sets share no memory.
func (s *Set) Clone() *Set {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c := &Set{backend: s.backend, card: s.card}
	c.array = append([]uint32(nil), s.array...)
	c.runs = append([]run(nil), s.runs...)
	c.bitmap = append([]uint64(nil), s.bitmap...)
	return c
}

// Equal reports whether two sets contain exactly the same values.
func (s *Set) Equal(o *Set) bool {
	s.mu.RLock()
	a := s.values()
	s.mu.RUnlock()
	o.mu.RLock()
	b := o.values()
	o.mu.RUnlock()
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// String renders a compact human-readable summary.
func (s *Set) String() string {
	backend, bytesUsed, card := s.Stats()
	return fmt.Sprintf("sparse.Set{backend=%s card=%d mem=%dB}", backend, card, bytesUsed)
}

var _ fmt.Stringer = (*Set)(nil)
