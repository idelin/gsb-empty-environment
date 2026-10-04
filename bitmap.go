package cbitmap

import (
	"sort"
	"sync"
	"time"
)

// Bitmap is a compressed bitmap over the uint32 domain. It is safe for
// concurrent use.
type Bitmap struct {
	mu   sync.RWMutex
	keys []uint16    // sorted high 16 bits
	cts  []container // parallel to keys
	met  *Metrics
}

// Option configures a Bitmap.
type Option func(*Bitmap)

// WithMetrics attaches a Metrics collector to the bitmap.
func WithMetrics(m *Metrics) Option {
	return func(b *Bitmap) { b.met = m }
}

// New creates an empty Bitmap.
func New(opts ...Option) *Bitmap {
	b := &Bitmap{}
	for _, o := range opts {
		o(b)
	}
	return b
}

func (b *Bitmap) findKey(k uint16) (int, bool) {
	i := sort.Search(len(b.keys), func(i int) bool { return b.keys[i] >= k })
	if i < len(b.keys) && b.keys[i] == k {
		return i, true
	}
	return i, false
}

func (b *Bitmap) insertContainerAt(i int, k uint16, c container) {
	b.keys = append(b.keys, 0)
	copy(b.keys[i+1:], b.keys[i:])
	b.keys[i] = k
	b.cts = append(b.cts, nil)
	copy(b.cts[i+1:], b.cts[i:])
	b.cts[i] = c
}

// Add inserts a single value.
func (b *Bitmap) Add(x uint32) {
	b.mu.Lock()
	defer b.mu.Unlock()
	k := uint16(x >> 16)
	i, found := b.findKey(k)
	if !found {
		b.insertContainerAt(i, k, &arrayContainer{data: []uint16{uint16(x)}})
		if b.met != nil {
			b.met.recordInserts(1)
		}
		return
	}
	old := b.cts[i]
	nc, added := old.add(uint16(x))
	if added {
		b.cts[i] = nc
		if b.met != nil {
			b.met.recordInserts(1)
			b.met.recordConversionIfChanged(old, nc)
		}
	}
}

// AddRange inserts all values in [lo, hi). It returns an error (and records
// the ErrInvalidRange error code) if the range is invalid.
func (b *Bitmap) AddRange(lo, hi uint64) error {
	if hi > 1<<32 || lo > hi {
		if b.met != nil {
			b.met.recordError(ErrInvalidRange)
		}
		return &Error{Code: ErrInvalidRange, Msg: "invalid range"}
	}
	if lo == hi {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	firstKey := uint32(lo >> 16)
	lastKey := uint32((hi - 1) >> 16)
	for k := firstKey; ; k++ {
		localStart := uint16(0)
		if k == firstKey {
			localStart = uint16(lo)
		}
		localLast := uint16(0xffff)
		if k == lastKey {
			localLast = uint16(hi - 1)
		}
		i, found := b.findKey(uint16(k))
		switch {
		case !found && localStart == 0 && localLast == 0xffff:
			// A fully covered container is cheapest as a single run.
			b.insertContainerAt(i, uint16(k), &runContainer{
				runs: []interval{{0, 0xffff}},
				card: containerWidth,
			})
		case !found:
			var c container = &arrayContainer{}
			c = c.addRange(localStart, localLast)
			b.insertContainerAt(i, uint16(k), b.maybeRunOptimize(c))
		default:
			old := b.cts[i]
			nc := old.addRange(localStart, localLast)
			if b.met != nil {
				b.met.recordConversionIfChanged(old, nc)
			}
			b.cts[i] = b.maybeRunOptimize(nc)
		}
		if k == lastKey {
			break
		}
	}
	if b.met != nil {
		b.met.recordInserts(int64(hi - lo))
	}
	return nil
}

// Remove deletes a single value, returning true if it was present.
func (b *Bitmap) Remove(x uint32) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	k := uint16(x >> 16)
	i, found := b.findKey(k)
	if !found {
		return false
	}
	old := b.cts[i]
	nc, removed := old.remove(uint16(x))
	if !removed {
		return false
	}
	if nc.cardinality() == 0 {
		b.keys = append(b.keys[:i], b.keys[i+1:]...)
		b.cts = append(b.cts[:i], b.cts[i+1:]...)
	} else {
		b.cts[i] = nc
		if b.met != nil {
			b.met.recordConversionIfChanged(old, nc)
		}
	}
	if b.met != nil {
		b.met.recordRemoves(1)
	}
	return true
}

// Contains reports whether x is present.
func (b *Bitmap) Contains(x uint32) bool {
	b.mu.RLock()
	k := uint16(x >> 16)
	i, found := b.findKey(k)
	hit := false
	if found {
		hit = b.cts[i].contains(uint16(x))
	}
	b.mu.RUnlock()
	if b.met != nil {
		b.met.recordLookup(hit)
	}
	return hit
}

// Cardinality returns the number of values in the bitmap.
func (b *Bitmap) Cardinality() uint64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	var n uint64
	for _, c := range b.cts {
		n += uint64(c.cardinality())
	}
	return n
}

// IsEmpty reports whether the bitmap has no values.
func (b *Bitmap) IsEmpty() bool { return b.Cardinality() == 0 }

// Optimize converts every container to its most compact representation.
func (b *Bitmap) Optimize() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i := range b.cts {
		c := b.cts[i]
		if bc, ok := c.(*bitmapContainer); ok && bc.card <= arrayMaxSize {
			nc := bc.toArray()
			if b.met != nil {
				b.met.recordConversion(convArray)
			}
			b.cts[i] = nc
			c = nc
		}
		b.cts[i] = b.maybeRunOptimize(c)
	}
}

// maybeRunOptimize converts c to a run container when that is smaller.
func (b *Bitmap) maybeRunOptimize(c container) container {
	if _, ok := c.(*runContainer); ok {
		return c
	}
	if c.numRuns()*4+4 < c.sizeInBytes() {
		if b.met != nil {
			b.met.recordConversion(convRun)
		}
		return c.toRun()
	}
	return c
}

// Clone returns a deep copy of the bitmap (sharing the Metrics collector).
func (b *Bitmap) Clone() *Bitmap {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := &Bitmap{met: b.met, keys: make([]uint16, len(b.keys)), cts: make([]container, len(b.cts))}
	copy(out.keys, b.keys)
	for i, c := range b.cts {
		out.cts[i] = c.clone()
	}
	return out
}

// Equals reports whether both bitmaps contain exactly the same values.
func (b *Bitmap) Equals(o *Bitmap) bool {
	if o == nil {
		return false
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	o.mu.RLock()
	defer o.mu.RUnlock()
	if len(b.keys) != len(o.keys) {
		return false
	}
	for i := range b.keys {
		if b.keys[i] != o.keys[i] {
			return false
		}
		a := b.cts[i].appendTo(uint32(b.keys[i])<<16, nil)
		c := o.cts[i].appendTo(uint32(o.keys[i])<<16, nil)
		if len(a) != len(c) {
			return false
		}
		for j := range a {
			if a[j] != c[j] {
				return false
			}
		}
	}
	return true
}

// ToArray returns all values in ascending order.
func (b *Bitmap) ToArray() []uint32 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]uint32, 0, 1024)
	for i, k := range b.keys {
		out = b.cts[i].appendTo(uint32(k)<<16, out)
	}
	return out
}

// ContainerKinds reports how many containers of each kind the bitmap holds.
func (b *Bitmap) ContainerKinds() map[string]int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	m := map[string]int{"array": 0, "bitmap": 0, "run": 0}
	for _, c := range b.cts {
		m[containerKind(c)]++
	}
	return m
}

// ---------------------------------------------------------------------------
// Boolean set operations
// ---------------------------------------------------------------------------

type opKind int

const (
	opAnd opKind = iota
	opOr
	opXor
)

// And returns the intersection of b and o. A nil operand records the
// ErrNilBitmap error code and yields an empty bitmap.
func (b *Bitmap) And(o *Bitmap) *Bitmap {
	if o == nil {
		if b.met != nil {
			b.met.recordError(ErrNilBitmap)
		}
		return New(WithMetrics(b.met))
	}
	if o == b {
		return b.Clone()
	}
	return b.binaryOp(o, opAnd, containerAnd)
}

// Or returns the union of b and o.
func (b *Bitmap) Or(o *Bitmap) *Bitmap {
	if o == nil {
		if b.met != nil {
			b.met.recordError(ErrNilBitmap)
		}
		return b.Clone()
	}
	if o == b {
		return b.Clone()
	}
	return b.binaryOp(o, opOr, containerOr)
}

// Xor returns the symmetric difference of b and o.
func (b *Bitmap) Xor(o *Bitmap) *Bitmap {
	if o == nil {
		if b.met != nil {
			b.met.recordError(ErrNilBitmap)
		}
		return b.Clone()
	}
	if o == b {
		return New(WithMetrics(b.met))
	}
	return b.binaryOp(o, opXor, containerXor)
}

func (b *Bitmap) binaryOp(o *Bitmap, kind opKind, f func(container, container) container) *Bitmap {
	start := time.Now()
	b.mu.RLock()
	o.mu.RLock()
	out := &Bitmap{met: b.met}
	i, j := 0, 0
	for i < len(b.keys) || j < len(o.keys) {
		switch {
		case i < len(b.keys) && j < len(o.keys) && b.keys[i] == o.keys[j]:
			c := f(b.cts[i], o.cts[j])
			if c.cardinality() > 0 {
				out.keys = append(out.keys, b.keys[i])
				out.cts = append(out.cts, c)
			}
			i++
			j++
		case j >= len(o.keys) || (i < len(b.keys) && b.keys[i] < o.keys[j]):
			if kind != opAnd {
				out.keys = append(out.keys, b.keys[i])
				out.cts = append(out.cts, b.cts[i].clone())
			}
			i++
		default:
			if kind != opAnd {
				out.keys = append(out.keys, o.keys[j])
				out.cts = append(out.cts, o.cts[j].clone())
			}
			j++
		}
	}
	b.mu.RUnlock()
	o.mu.RUnlock()
	if b.met != nil {
		b.met.recordOp(kind, time.Since(start))
	}
	return out
}

// AndMany returns the intersection of b with all the given bitmaps.
func (b *Bitmap) AndMany(others ...*Bitmap) *Bitmap {
	acc := b.Clone()
	for _, o := range others {
		acc = acc.And(o)
	}
	return acc
}

// OrMany returns the union of b with all the given bitmaps.
func (b *Bitmap) OrMany(others ...*Bitmap) *Bitmap {
	acc := b.Clone()
	for _, o := range others {
		acc = acc.Or(o)
	}
	return acc
}

// XorMany returns the symmetric difference of b with all the given bitmaps.
func (b *Bitmap) XorMany(others ...*Bitmap) *Bitmap {
	acc := b.Clone()
	for _, o := range others {
		acc = acc.Xor(o)
	}
	return acc
}
