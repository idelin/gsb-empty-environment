// Package slaballoc implements a fixed-page allocator for variable-length
// objects with stable, generation-checked handles and compaction.
//
// Memory layout (all sizes in bytes, verified by tests):
//
//	Handle  uint64                      8 bytes, bit layout:
//	  [63:48] page index   (16 bits, up to 65535 pages)
//	  [47:24] slot index   (24 bits, up to 16M slots per page)
//	  [23: 0] generation   (24 bits, wraps after 16M reuses of a slot)
//
//	slotEntry (per live or free slot)  16 bytes:
//	  offset     uint32   4   byte offset of the object inside the page
//	  size       uint32   4   aligned object size
//	  generation uint32   4   low 24 bits compared against the handle
//	  next        int32   4   slotLive(-2) if live, else next free-slot index
//
//	extent (per free fragment, in page.free)  8 bytes:
//	  off  uint32, size uint32; kept sorted by off and always coalesced
//
// Per-object bookkeeping cost is exactly one slotEntry (16 bytes);
// the handle itself is 8 bytes and lives in the caller's hands.
package slaballoc

import (
	"errors"
	"fmt"
	"sort"
)

const (
	genBits  = 24
	slotBits = 24

	genMask  = (1 << genBits) - 1
	slotMask = (1 << slotBits) - 1

	alignment = 8

	slotLive = -2 // slotEntry.next marker for a live slot
	slotEnd  = -1 // free-slot list terminator
)

var (
	ErrOutOfMemory   = errors.New("slaballoc: out of memory")
	ErrObjectTooBig  = errors.New("slaballoc: object larger than page")
	ErrInvalidSize   = errors.New("slaballoc: size must be > 0")
	ErrInvalidHandle = errors.New("slaballoc: invalid handle")
	ErrStaleHandle   = errors.New("slaballoc: stale handle (generation mismatch)")
)

// Handle is an opaque 8-byte reference to an object. It stays valid across
// compaction (objects move, slots don't) and becomes stale after Free.
type Handle uint64

func makeHandle(pageIdx, slotIdx, gen uint32) Handle {
	return Handle(uint64(pageIdx)<<48 | uint64(slotIdx&slotMask)<<24 | uint64(gen&genMask))
}

func (h Handle) pageIdx() uint32 { return uint32(h >> 48) }
func (h Handle) slotIdx() uint32 { return uint32(h>>24) & slotMask }
func (h Handle) gen() uint32     { return uint32(h) & genMask }

func (h Handle) String() string {
	return fmt.Sprintf("Handle{page:%d slot:%d gen:%d}", h.pageIdx(), h.slotIdx(), h.gen())
}

type slotEntry struct {
	offset     uint32
	size       uint32
	generation uint32
	next       int32
}

type extent struct {
	off  uint32
	size uint32
}

type page struct {
	data         []byte
	slots        []slotEntry
	freeSlotHead int32
	free         []extent // sorted by off, never adjacent (coalesced)
	liveBytes    uint32
}

func newPage(pageSize uint32) *page {
	return &page{
		data:         make([]byte, pageSize),
		freeSlotHead: slotEnd,
		free:         []extent{{off: 0, size: pageSize}},
	}
}

func align(n uint32) uint32 { return (n + alignment - 1) &^ (alignment - 1) }

func (p *page) totalFree() uint32 {
	var total uint32
	for _, e := range p.free {
		total += e.size
	}
	return total
}

// carve removes size bytes from the best-fitting free extent.
// On failure the free list is left untouched.
func (p *page) carve(size uint32) (uint32, bool) {
	best := -1
	for i, e := range p.free {
		if e.size >= size && (best < 0 || e.size < p.free[best].size) {
			best = i
		}
	}
	if best < 0 {
		return 0, false
	}
	e := p.free[best]
	off := e.off
	if e.size == size {
		p.free = append(p.free[:best], p.free[best+1:]...)
	} else {
		p.free[best].off += size
		p.free[best].size -= size
	}
	return off, true
}

// insertFree returns an extent to the free list, keeping it sorted and
// coalescing with adjacent neighbors.
func (p *page) insertFree(off, size uint32) {
	i := sort.Search(len(p.free), func(i int) bool { return p.free[i].off >= off })
	p.free = append(p.free, extent{})
	copy(p.free[i+1:], p.free[i:])
	p.free[i] = extent{off: off, size: size}
	if i+1 < len(p.free) && p.free[i].off+p.free[i].size == p.free[i+1].off {
		p.free[i].size += p.free[i+1].size
		p.free = append(p.free[:i+1], p.free[i+2:]...)
	}
	if i > 0 && p.free[i-1].off+p.free[i-1].size == p.free[i].off {
		p.free[i-1].size += p.free[i].size
		p.free = append(p.free[:i], p.free[i+1:]...)
	}
}

// compact moves all live objects to the start of the page, updating each
// slot's offset. Handles reference slots, so they remain valid. The free
// list is rebuilt as a single extent covering the tail.
func (p *page) compact() {
	order := make([]int, 0, len(p.slots))
	for i := range p.slots {
		if p.slots[i].next == slotLive {
			order = append(order, i)
		}
	}
	sort.Slice(order, func(a, b int) bool { return p.slots[order[a]].offset < p.slots[order[b]].offset })
	var cursor uint32
	for _, idx := range order {
		s := &p.slots[idx]
		if s.offset != cursor {
			copy(p.data[cursor:cursor+s.size], p.data[s.offset:s.offset+s.size])
			s.offset = cursor
		}
		cursor += s.size
	}
	p.liveBytes = cursor
	p.free = p.free[:0]
	if cursor < uint32(len(p.data)) {
		p.free = append(p.free, extent{off: cursor, size: uint32(len(p.data)) - cursor})
	}
}

func (p *page) allocSlot() (int, bool) {
	if p.freeSlotHead != slotEnd {
		idx := int(p.freeSlotHead)
		p.freeSlotHead = p.slots[idx].next
		return idx, true
	}
	if len(p.slots) >= slotMask+1 {
		return 0, false
	}
	p.slots = append(p.slots, slotEntry{})
	return len(p.slots) - 1, true
}

// alloc reserves size aligned bytes. It compacts only when total free space
// suffices but no single extent does. Any failure path leaves the free list
// in a valid state.
func (p *page) alloc(size uint32) (slotIdx int, ok bool) {
	off, found := p.carve(size)
	if !found {
		if p.totalFree() < size {
			return 0, false // free list untouched
		}
		p.compact()
		off, found = p.carve(size)
		if !found {
			return 0, false // compact() rebuilt a valid free list
		}
	}
	idx, slotOK := p.allocSlot()
	if !slotOK {
		p.insertFree(off, size) // roll back the carve
		return 0, false
	}
	s := &p.slots[idx]
	s.offset = off
	s.size = size
	s.next = slotLive
	p.liveBytes += size
	return idx, true
}

// Allocator manages a set of fixed-size pages.
type Allocator struct {
	pageSize uint32
	maxPages int
	pages    []*page
}

// New creates an Allocator. maxPages <= 0 means unlimited pages.
func New(pageSize int, maxPages int) *Allocator {
	if pageSize <= 0 {
		pageSize = 64 * 1024
	}
	return &Allocator{pageSize: align(uint32(pageSize)), maxPages: maxPages}
}

// Alloc reserves n bytes and returns a stable handle.
func (a *Allocator) Alloc(n int) (Handle, error) {
	if n <= 0 {
		return 0, ErrInvalidSize
	}
	size := align(uint32(n))
	if size > a.pageSize {
		return 0, ErrObjectTooBig
	}
	for pi, p := range a.pages {
		if idx, ok := p.alloc(size); ok {
			return makeHandle(uint32(pi), uint32(idx), p.slots[idx].generation), nil
		}
	}
	if a.maxPages > 0 && len(a.pages) >= a.maxPages {
		return 0, ErrOutOfMemory
	}
	p := newPage(a.pageSize)
	a.pages = append(a.pages, p)
	idx, ok := p.alloc(size) // fresh page always fits
	if !ok {
		return 0, ErrOutOfMemory
	}
	return makeHandle(uint32(len(a.pages)-1), uint32(idx), p.slots[idx].generation), nil
}

func (a *Allocator) resolve(h Handle) (*page, int, error) {
	pi := int(h.pageIdx())
	if pi >= len(a.pages) {
		return nil, 0, ErrInvalidHandle
	}
	p := a.pages[pi]
	si := int(h.slotIdx())
	if si >= len(p.slots) {
		return nil, 0, ErrInvalidHandle
	}
	s := &p.slots[si]
	if s.next != slotLive {
		return nil, 0, ErrStaleHandle
	}
	if s.generation&genMask != h.gen() {
		return nil, 0, ErrStaleHandle
	}
	return p, si, nil
}

// Get returns the object's bytes (aligned allocation size). The slice is
// only valid until the next Alloc that triggers compaction on this page.
func (a *Allocator) Get(h Handle) ([]byte, error) {
	p, si, err := a.resolve(h)
	if err != nil {
		return nil, err
	}
	s := &p.slots[si]
	return p.data[s.offset : s.offset+s.size], nil
}

// Free releases the object and bumps the slot generation, invalidating all
// previously issued handles for it. The freed extent is coalesced with
// adjacent free fragments.
func (a *Allocator) Free(h Handle) error {
	p, si, err := a.resolve(h)
	if err != nil {
		return err
	}
	s := &p.slots[si]
	p.insertFree(s.offset, s.size)
	p.liveBytes -= s.size
	s.next = p.freeSlotHead
	p.freeSlotHead = int32(si)
	s.generation = (s.generation + 1) & genMask
	return nil
}

// Offset reports the object's current byte offset inside its page.
// It is introspection for tests/demos; callers must not cache it.
func (a *Allocator) Offset(h Handle) (uint32, error) {
	p, si, err := a.resolve(h)
	if err != nil {
		return 0, err
	}
	return p.slots[si].offset, nil
}

// PageStats summarizes one page's occupancy.
type PageStats struct {
	LiveBytes   uint32
	FreeBytes   uint32
	FreeExtents int
	Slots       int
	LiveSlots   int
}

func (a *Allocator) PageStats(pageIdx int) PageStats {
	p := a.pages[pageIdx]
	st := PageStats{LiveBytes: p.liveBytes, FreeBytes: p.totalFree(), FreeExtents: len(p.free), Slots: len(p.slots)}
	for i := range p.slots {
		if p.slots[i].next == slotLive {
			st.LiveSlots++
		}
	}
	return st
}

func (a *Allocator) NumPages() int { return len(a.pages) }
func (a *Allocator) PageSize() int { return int(a.pageSize) }
