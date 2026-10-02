package cbitmap

import (
	"runtime"
	"sync"
	"sync/atomic"
)

const (
	topSize  = 256 // high 8 bits of the 16-bit container key
	leafSize = 256 // low 8 bits of the 16-bit container key
)

// leafTable is a lazily allocated second-level table of container slots.
type leafTable [leafSize]atomic.Pointer[container]

// Bitmap is a compressed bitmap over the uint32 domain. It is safe for
// concurrent use: every container slot is an atomic pointer updated with
// compare-and-swap, containers are immutable once published, and readers
// are completely lock-free. There is no global mutex anywhere.
type Bitmap struct {
	top [topSize]atomic.Pointer[leafTable]
}

// New creates an empty Bitmap.
func New() *Bitmap { return &Bitmap{} }

// slotFor returns the slot for container key hi (high 16 bits of a value),
// optionally creating the leaf table on demand via CAS.
func (b *Bitmap) slotFor(hi uint32, create bool) *atomic.Pointer[container] {
	t := &b.top[hi>>8]
	l := t.Load()
	if l == nil {
		if !create {
			return nil
		}
		nl := new(leafTable)
		if t.CompareAndSwap(nil, nl) {
			l = nl
		} else {
			l = t.Load() // another goroutine won the race
		}
	}
	return &l[hi&0xff]
}

// containerAt returns the current container for key hi, or nil.
func (b *Bitmap) containerAt(hi uint32) *container {
	slot := b.slotFor(hi, false)
	if slot == nil {
		return nil
	}
	return slot.Load()
}

// Add inserts id into the bitmap. Concurrent Adds are coordinated purely
// by CAS retries on the affected container slot.
func (b *Bitmap) Add(id uint32) {
	slot := b.slotFor(id>>16, true)
	lo := uint16(id)
	for {
		old := slot.Load()
		var work container
		if old == nil {
			work = newArrayContainer()
		} else {
			work = (*old).clone()
		}
		nc, changed := work.add(lo)
		if !changed {
			return // already present
		}
		if slot.CompareAndSwap(old, &nc) {
			return
		}
		// Lost the CAS race: reload and retry.
	}
}

// AddRange inserts every id in [start, end] (inclusive). Dense ranges are
// stored as run containers, so this is O(number of 64K blocks touched).
func (b *Bitmap) AddRange(start, end uint32) {
	if start > end {
		return
	}
	for hi := start >> 16; ; hi++ {
		lo := uint16(0)
		up := uint16(0xffff)
		if hi == start>>16 {
			lo = uint16(start)
		}
		if hi == end>>16 {
			up = uint16(end)
		}
		b.mergeContainer(hi, &runContainer{runs: []interval{{lo, up}}})
		if hi == end>>16 {
			return
		}
	}
}

// mergeContainer atomically unions c into the slot for key hi.
func (b *Bitmap) mergeContainer(hi uint32, c container) {
	slot := b.slotFor(hi, true)
	for {
		old := slot.Load()
		var nc container
		if old == nil {
			nc = c
		} else {
			nc = orContainers((*old).clone(), c)
		}
		if slot.CompareAndSwap(old, &nc) {
			return
		}
	}
}

// Contains reports whether id is present. Lock-free read path.
func (b *Bitmap) Contains(id uint32) bool {
	c := b.containerAt(id >> 16)
	if c == nil {
		return false
	}
	return (*c).contains(uint16(id))
}

// Cardinality returns the number of set bits.
func (b *Bitmap) Cardinality() uint64 {
	var n uint64
	b.eachContainer(func(_ uint32, c container) {
		n += uint64(c.cardinality())
	})
	return n
}

// IsEmpty reports whether the bitmap has no bits set.
func (b *Bitmap) IsEmpty() bool {
	empty := true
	b.eachContainer(func(_ uint32, c container) {
		if c.cardinality() > 0 {
			empty = false
		}
	})
	return empty
}

// eachContainer iterates a consistent-per-slot snapshot of containers.
func (b *Bitmap) eachContainer(fn func(hi uint32, c container)) {
	for t := 0; t < topSize; t++ {
		l := b.top[t].Load()
		if l == nil {
			continue
		}
		for s := 0; s < leafSize; s++ {
			if c := l[s].Load(); c != nil {
				fn(uint32(t<<8|s), *c)
			}
		}
	}
}

// keys returns the sorted container keys present in the bitmap.
func (b *Bitmap) keys() []uint32 {
	var out []uint32
	b.eachContainer(func(hi uint32, _ container) {
		out = append(out, hi)
	})
	return out
}

// Stats describes the container composition of the bitmap.
type Stats struct {
	Containers  int
	Array       int
	Bitmap      int
	Run         int
	Cardinality uint64
}

// Stats returns the current container composition.
func (b *Bitmap) Stats() Stats {
	var s Stats
	b.eachContainer(func(_ uint32, c container) {
		s.Containers++
		switch c.ctype() {
		case typeArray:
			s.Array++
		case typeBitmap:
			s.Bitmap++
		case typeRun:
			s.Run++
		}
		s.Cardinality += uint64(c.cardinality())
	})
	return s
}

// ---------------------------------------------------------------------------
// Bitmap-level boolean operations (parallel, work distributed via the
// lock-free Queue; no mutex on the data path).
// ---------------------------------------------------------------------------

// And returns the intersection of b and o.
func (b *Bitmap) And(o *Bitmap) *Bitmap {
	return bitmapOp(b, o, func(x, y *container) container {
		if x == nil || y == nil {
			return nil
		}
		return andContainers(*x, *y)
	})
}

// Or returns the union of b and o.
func (b *Bitmap) Or(o *Bitmap) *Bitmap {
	return bitmapOp(b, o, func(x, y *container) container {
		switch {
		case x == nil && y == nil:
			return nil
		case x == nil:
			return (*y).clone()
		case y == nil:
			return (*x).clone()
		}
		return orContainers(*x, *y)
	})
}

// Xor returns the symmetric difference of b and o.
func (b *Bitmap) Xor(o *Bitmap) *Bitmap {
	return bitmapOp(b, o, func(x, y *container) container {
		switch {
		case x == nil && y == nil:
			return nil
		case x == nil:
			return (*y).clone()
		case y == nil:
			return (*x).clone()
		}
		return xorContainers(*x, *y)
	})
}

// bitmapOp applies op to every container key of the union of both bitmaps,
// distributing per-key work through the lock-free queue to a worker pool.
// The result bitmap is private during construction, so workers store
// results directly into distinct slots without any locking.
func bitmapOp(a, b *Bitmap, op func(x, y *container) container) *Bitmap {
	keySet := make(map[uint32]struct{}, 64)
	for _, k := range a.keys() {
		keySet[k] = struct{}{}
	}
	for _, k := range b.keys() {
		keySet[k] = struct{}{}
	}
	res := New()
	if len(keySet) == 0 {
		return res
	}

	q := NewQueue[uint32]()
	for k := range keySet {
		q.Enqueue(k)
	}

	workers := runtime.NumCPU()
	if workers > len(keySet) {
		workers = len(keySet)
	}
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				k, ok := q.Dequeue()
				if !ok {
					return // queue drained: all keys were enqueued upfront
				}
				nc := op(a.containerAt(k), b.containerAt(k))
				if nc != nil && nc.cardinality() > 0 {
					res.slotFor(k, true).Store(&nc)
				}
			}
		}()
	}
	wg.Wait()
	return res
}
