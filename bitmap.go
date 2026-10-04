// Package roaring implements a compressed bitmap with lock-free reads and
// fine-grained, CAS-based write coordination (no global mutex).
//
// The 32-bit key space is split into 2^16 containers indexed by the high
// 16 bits. Each container automatically picks the most compact of three
// representations: sorted array, 65536-bit bitmap, or run-length encoded
// intervals. Container slots are installed with CAS and every published
// container is immutable, so Contains/And/Or/Xor readers never take locks.
// Writers serialize per container with a CAS spinlock and publish updates
// with a single atomic pointer store (copy-on-write).
package roaring

import (
	"runtime"
	"sort"
	"sync/atomic"
	"unsafe"
)

const numContainers = 1 << 16

// holder boxes a container behind a single word so it can be published
// and read atomically.
type holder struct {
	c container
}

// slot is a lazily allocated per-container cell.
type slot struct {
	lock int32          // CAS spinlock, writers only
	ref  unsafe.Pointer // *holder, immutable once published
}

// Bitmap is a compressed bitmap over the uint32 key space. The zero value
// is ready to use and safe for concurrent Add/Remove/Contains.
type Bitmap struct {
	slots [numContainers]unsafe.Pointer // *slot
}

// New returns an empty Bitmap.
func New() *Bitmap { return &Bitmap{} }

func (b *Bitmap) getOrCreateSlot(hi uint32) *slot {
	if p := atomic.LoadPointer(&b.slots[hi]); p != nil {
		return (*slot)(p)
	}
	s := &slot{}
	if atomic.CompareAndSwapPointer(&b.slots[hi], nil, unsafe.Pointer(s)) {
		return s
	}
	return (*slot)(atomic.LoadPointer(&b.slots[hi]))
}

func (s *slot) lockSlow() {
	for !atomic.CompareAndSwapInt32(&s.lock, 0, 1) {
		runtime.Gosched()
	}
}

func (s *slot) unlock() {
	atomic.StoreInt32(&s.lock, 0)
}

func loadContainer(b *Bitmap, k int) container {
	s := (*slot)(atomic.LoadPointer(&b.slots[k]))
	if s == nil {
		return nil
	}
	h := (*holder)(atomic.LoadPointer(&s.ref))
	if h == nil {
		return nil
	}
	return h.c
}

// setContainer installs c at container index k. Callers must guarantee
// single-writer access to k (used by bulk set operations).
func (b *Bitmap) setContainer(k int, c container) {
	s := b.getOrCreateSlot(uint32(k))
	atomic.StorePointer(&s.ref, unsafe.Pointer(&holder{c}))
}

// Add inserts x. It reports whether x was not already present.
// Safe for concurrent use with all other methods.
func (b *Bitmap) Add(x uint32) bool {
	hi, lo := x>>16, uint16(x)
	s := b.getOrCreateSlot(hi)
	s.lockSlow()
	defer s.unlock()
	h := (*holder)(atomic.LoadPointer(&s.ref))
	if h == nil {
		ac := &arrayContainer{}
		c, _ := ac.add(lo)
		atomic.StorePointer(&s.ref, unsafe.Pointer(&holder{c}))
		return true
	}
	if h.c.contains(lo) {
		return false
	}
	nc := h.c.clone()
	nc, _ = nc.add(lo)
	atomic.StorePointer(&s.ref, unsafe.Pointer(&holder{normalize(nc)}))
	return true
}

// AddMany inserts a batch of values, amortizing the copy-on-write cost to
// one clone per touched container. The batch is sorted first so that all
// values landing in the same container are applied in a single critical
// section.
func (b *Bitmap) AddMany(xs []uint32) {
	if len(xs) == 0 {
		return
	}
	sorted := make([]uint32, len(xs))
	copy(sorted, xs)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	for i := 0; i < len(sorted); {
		hi := sorted[i] >> 16
		j := i + 1
		for j < len(sorted) && sorted[j]>>16 == hi {
			j++
		}
		b.addManyToContainer(hi, sorted[i:j])
		i = j
	}
}

func (b *Bitmap) addManyToContainer(hi uint32, los []uint32) {
	s := b.getOrCreateSlot(hi)
	s.lockSlow()
	defer s.unlock()
	var c container
	if h := (*holder)(atomic.LoadPointer(&s.ref)); h != nil {
		c = h.c.clone()
	} else {
		c = &arrayContainer{}
	}
	for _, x := range los {
		c, _ = c.add(uint16(x))
	}
	atomic.StorePointer(&s.ref, unsafe.Pointer(&holder{normalize(c)}))
}

// AddRange inserts every value in the inclusive range [lo, hi].
func (b *Bitmap) AddRange(lo, hi uint32) {
	if hi < lo {
		return
	}
	loKey, hiKey := lo>>16, hi>>16
	for k := loKey; k <= hiKey; k++ {
		start := uint16(0)
		if k == loKey {
			start = uint16(lo)
		}
		end := uint16(0xFFFF)
		if k == hiKey {
			end = uint16(hi)
		}
		s := b.getOrCreateSlot(k)
		s.lockSlow()
		var c container
		if h := (*holder)(atomic.LoadPointer(&s.ref)); h != nil {
			c = h.c.clone()
		} else {
			c = &arrayContainer{}
		}
		c = normalize(c.addRange(start, end))
		atomic.StorePointer(&s.ref, unsafe.Pointer(&holder{c}))
		s.unlock()
	}
}

// Remove deletes x and reports whether it was present.
func (b *Bitmap) Remove(x uint32) bool {
	hi, lo := x>>16, uint16(x)
	p := atomic.LoadPointer(&b.slots[hi])
	if p == nil {
		return false
	}
	s := (*slot)(p)
	s.lockSlow()
	defer s.unlock()
	h := (*holder)(atomic.LoadPointer(&s.ref))
	if h == nil || !h.c.contains(lo) {
		return false
	}
	nc := h.c.clone()
	nc, _ = nc.remove(lo)
	nc = normalize(nc)
	if nc == nil {
		atomic.StorePointer(&s.ref, nil)
	} else {
		atomic.StorePointer(&s.ref, unsafe.Pointer(&holder{nc}))
	}
	return true
}

// Contains reports whether x is present. It is fully lock-free.
func (b *Bitmap) Contains(x uint32) bool {
	p := atomic.LoadPointer(&b.slots[x>>16])
	if p == nil {
		return false
	}
	h := (*holder)(atomic.LoadPointer(&((*slot)(p)).ref))
	return h != nil && h.c.contains(uint16(x))
}

// Cardinality returns the number of values in the bitmap. Under concurrent
// writes the result is a linearizable-ish snapshot per container, not a
// globally consistent point in time.
func (b *Bitmap) Cardinality() int64 {
	var n int64
	for k := 0; k < numContainers; k++ {
		if c := loadContainer(b, k); c != nil {
			n += int64(c.cardinality())
		}
	}
	return n
}

// IsEmpty reports whether the bitmap has no values.
func (b *Bitmap) IsEmpty() bool {
	for k := 0; k < numContainers; k++ {
		if loadContainer(b, k) != nil {
			return false
		}
	}
	return true
}

// Optimize rewrites every container into its most compact representation,
// typically converting dense contiguous regions into run containers.
func (b *Bitmap) Optimize() {
	for k := 0; k < numContainers; k++ {
		p := atomic.LoadPointer(&b.slots[k])
		if p == nil {
			continue
		}
		s := (*slot)(p)
		s.lockSlow()
		if h := (*holder)(atomic.LoadPointer(&s.ref)); h != nil {
			if nc := normalize(h.c); nc != h.c {
				atomic.StorePointer(&s.ref, unsafe.Pointer(&holder{nc}))
			}
		}
		s.unlock()
	}
}

// Clone returns a new Bitmap sharing the (immutable) containers of b.
// Subsequent writes to either bitmap copy-on-write, so they never alias.
func (b *Bitmap) Clone() *Bitmap {
	out := &Bitmap{}
	for k := 0; k < numContainers; k++ {
		if c := loadContainer(b, k); c != nil {
			out.setContainer(k, c)
		}
	}
	return out
}

// Stats summarizes the container composition of the bitmap.
type Stats struct {
	NumContainers int
	NumArray      int
	NumBitmap     int
	NumRun        int
	Cardinality   int64
}

// Stats computes the container composition of the bitmap.
func (b *Bitmap) Stats() Stats {
	var st Stats
	for k := 0; k < numContainers; k++ {
		c := loadContainer(b, k)
		if c == nil {
			continue
		}
		st.NumContainers++
		switch c.ctype() {
		case typeArray:
			st.NumArray++
		case typeBitmap:
			st.NumBitmap++
		case typeRun:
			st.NumRun++
		}
		st.Cardinality += int64(c.cardinality())
	}
	return st
}

// ToArray returns all values in ascending order. Intended for tests and
// small bitmaps.
func (b *Bitmap) ToArray() []uint32 {
	out := make([]uint32, 0)
	for k := 0; k < numContainers; k++ {
		c := loadContainer(b, k)
		if c == nil {
			continue
		}
		ac := c
		if _, ok := ac.(*arrayContainer); !ok {
			ac = nonRun(ac)
			if _, ok := ac.(*arrayContainer); !ok {
				ac = ac.toBitmap().toArray()
			}
		}
		for _, v := range ac.(*arrayContainer).data {
			out = append(out, uint32(k)<<16|uint32(v))
		}
	}
	return out
}

type opKind int

const (
	opAnd opKind = iota
	opOr
	opXor
)

func applyOp(kind opKind, ca, cb container) container {
	switch kind {
	case opAnd:
		return containerAnd(ca, cb)
	case opOr:
		return containerOr(ca, cb)
	case opXor:
		return containerXor(ca, cb)
	}
	return nil
}

func computeSlot(a, b, out *Bitmap, k int, kind opKind) {
	ca := loadContainer(a, k)
	cb := loadContainer(b, k)
	var rc container
	switch kind {
	case opAnd:
		if ca == nil || cb == nil {
			return
		}
		rc = containerAnd(ca, cb)
	case opOr, opXor:
		if ca == nil {
			rc = cb
		} else if cb == nil {
			rc = ca
		} else if kind == opOr {
			rc = containerOr(ca, cb)
		} else {
			rc = containerXor(ca, cb)
		}
	}
	if rc == nil {
		return
	}
	out.setContainer(k, rc)
}

func binaryOp(a, b *Bitmap, kind opKind) *Bitmap {
	out := &Bitmap{}
	for k := 0; k < numContainers; k++ {
		computeSlot(a, b, out, k, kind)
	}
	return out
}

// And returns the intersection of a and b.
func (a *Bitmap) And(b *Bitmap) *Bitmap { return binaryOp(a, b, opAnd) }

// Or returns the union of a and b.
func (a *Bitmap) Or(b *Bitmap) *Bitmap { return binaryOp(a, b, opOr) }

// Xor returns the symmetric difference of a and b.
func (a *Bitmap) Xor(b *Bitmap) *Bitmap { return binaryOp(a, b, opXor) }

// AndCardinality returns the size of the intersection without
// materializing it.
func (a *Bitmap) AndCardinality(b *Bitmap) int64 {
	var n int64
	for k := 0; k < numContainers; k++ {
		ca := loadContainer(a, k)
		cb := loadContainer(b, k)
		if ca == nil || cb == nil {
			continue
		}
		if rc := containerAnd(ca, cb); rc != nil {
			n += int64(rc.cardinality())
		}
	}
	return n
}
