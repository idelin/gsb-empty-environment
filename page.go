// Package slottedpage implements a variable-length record page store with a
// slot directory. Records are addressed by stable handles (page ID, slot
// number, generation); in-page compaction never changes a live handle, and
// slot reuse invalidates handles minted from the previous generation.
package slottedpage

import "errors"

const (
	// DefaultPageSize is the page size used by NewPage.
	DefaultPageSize = 4096
	// MaxPageSize is the largest supported page (offsets are uint16).
	MaxPageSize = 1 << 16
	// MaxRecordSize is the largest record payload (lengths are uint16).
	MaxRecordSize = 1<<16 - 1

	headerSize = 32
	slotSize   = 12

	flagTombstone uint32 = 1 << 0
)

var (
	ErrPageFull     = errors.New("slottedpage: not enough space on page")
	ErrNotFound     = errors.New("slottedpage: slot does not exist")
	ErrStaleHandle  = errors.New("slottedpage: handle generation mismatch")
	ErrRecordTooBig = errors.New("slottedpage: record exceeds maximum size")
	ErrBadPageSize  = errors.New("slottedpage: invalid page size")
)

// Handle is the public, stable identifier of a record.
type Handle struct {
	PageID uint64
	Slot   uint16
	Gen    uint32
}

type slot struct {
	offset uint16
	length uint16
	gen    uint32
	flags  uint32
}

func (s *slot) tombstone() bool { return s.flags&flagTombstone != 0 }

// Page is a single fixed-size slotted page.
type Page struct {
	id         uint64
	buf        []byte // full page image; record bytes live in [freeEnd, len(buf))
	slots      []slot
	freeEnd    uint16 // start of the record heap, grows downward
	fragmented uint32 // bytes inside the heap freed by delete/shrink
}

// NewPage creates an empty page of the given size.
func NewPage(id uint64, size int) (*Page, error) {
	if size < headerSize+slotSize || size > MaxPageSize {
		return nil, ErrBadPageSize
	}
	return &Page{
		id:      id,
		buf:     make([]byte, size),
		freeEnd: uint16(size),
	}, nil
}

// ID returns the page identifier.
func (p *Page) ID() uint64 { return p.id }

func (p *Page) dirEnd() int { return headerSize + len(p.slots)*slotSize }

func (p *Page) contiguousFree() int { return int(p.freeEnd) - p.dirEnd() }

func (p *Page) lookup(h Handle) (*slot, error) {
	if h.PageID != p.id || int(h.Slot) >= len(p.slots) {
		return nil, ErrNotFound
	}
	s := &p.slots[h.Slot]
	if s.tombstone() || s.gen != h.Gen {
		return nil, ErrStaleHandle
	}
	return s, nil
}

// Insert stores data and returns its handle. Tombstoned slots are reused
// with a bumped generation, so handles minted before the delete stay stale.
func (p *Page) Insert(data []byte) (Handle, error) {
	if len(data) > MaxRecordSize {
		return Handle{}, ErrRecordTooBig
	}
	idx := -1
	for i := range p.slots {
		if p.slots[i].tombstone() {
			idx = i
			break
		}
	}
	need := len(data)
	if idx < 0 {
		need += slotSize
	}
	// Validate before mutating: a failed insert leaves the page untouched.
	if need > p.contiguousFree()+int(p.fragmented) {
		return Handle{}, ErrPageFull
	}
	if need > p.contiguousFree() {
		p.Compact()
	}
	if idx < 0 {
		p.slots = append(p.slots, slot{gen: 1})
		idx = len(p.slots) - 1
	}
	p.freeEnd -= uint16(len(data))
	copy(p.buf[p.freeEnd:], data)
	s := &p.slots[idx]
	s.offset = p.freeEnd
	s.length = uint16(len(data))
	s.flags = 0
	return Handle{PageID: p.id, Slot: uint16(idx), Gen: s.gen}, nil
}

// Get returns a copy of the record addressed by h.
func (p *Page) Get(h Handle) ([]byte, error) {
	s, err := p.lookup(h)
	if err != nil {
		return nil, err
	}
	out := make([]byte, s.length)
	copy(out, p.buf[s.offset:s.offset+uint16(s.length)])
	return out, nil
}

// Update replaces the record addressed by h, growing or shrinking it in
// place. If the new record cannot fit even after compaction, the page is
// left completely unchanged.
func (p *Page) Update(h Handle, data []byte) error {
	if len(data) > MaxRecordSize {
		return ErrRecordTooBig
	}
	s, err := p.lookup(h)
	if err != nil {
		return err
	}
	old := int(s.length)
	// Pre-validate against total reclaimable space so a failure mutates nothing.
	if len(data) > p.contiguousFree()+int(p.fragmented)+old {
		return ErrPageFull
	}
	// Detach the old record: its bytes join the fragmented pool, and the
	// slot is temporarily tombstoned so compaction below skips it.
	p.fragmented += uint32(old)
	s.offset, s.length = 0, 0
	s.flags |= flagTombstone
	if len(data) > p.contiguousFree() {
		p.Compact()
	}
	p.freeEnd -= uint16(len(data))
	copy(p.buf[p.freeEnd:], data)
	s.offset = p.freeEnd
	s.length = uint16(len(data))
	s.flags &^= flagTombstone
	return nil
}

// Delete removes the record addressed by h and bumps the slot generation,
// invalidating every handle previously minted for this slot.
func (p *Page) Delete(h Handle) error {
	s, err := p.lookup(h)
	if err != nil {
		return err
	}
	p.fragmented += uint32(s.length)
	s.offset = 0
	s.length = 0
	s.flags |= flagTombstone
	s.gen++
	return nil
}

// Compact rewrites all live records contiguously at the end of the page,
// merging every fragmented byte back into the contiguous free region.
// Handles are unaffected: only internal offsets move.
func (p *Page) Compact() {
	tmp := make([]byte, len(p.buf))
	pos := len(p.buf)
	for i := range p.slots {
		s := &p.slots[i]
		if s.tombstone() {
			continue
		}
		pos -= int(s.length)
		copy(tmp[pos:pos+int(s.length)], p.buf[s.offset:s.offset+uint16(s.length)])
		s.offset = uint16(pos)
	}
	p.buf = tmp
	p.freeEnd = uint16(pos)
	p.fragmented = 0
}

// Stats reports how the page's bytes are accounted for. The invariant
// Header+Directory+Records+Free+Fragmented == PageSize always holds.
type Stats struct {
	PageSize        int
	HeaderBytes     int
	DirectoryBytes  int
	RecordBytes     int
	FreeBytes       int
	FragmentedBytes int
	LiveSlots       int
	TombstoneSlots  int
}

// Stats returns the current space accounting of the page.
func (p *Page) Stats() Stats {
	st := Stats{
		PageSize:        len(p.buf),
		HeaderBytes:     headerSize,
		DirectoryBytes:  len(p.slots) * slotSize,
		FreeBytes:       p.contiguousFree(),
		FragmentedBytes: int(p.fragmented),
	}
	for i := range p.slots {
		if p.slots[i].tombstone() {
			st.TombstoneSlots++
		} else {
			st.LiveSlots++
			st.RecordBytes += int(p.slots[i].length)
		}
	}
	return st
}
