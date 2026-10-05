// Package slottedpage implements a variable-length record page store with a
// slot directory. Records are addressed by stable handles of the form
// (PageID, Slot, Generation). In-page compaction never changes live handles;
// deleting a record bumps the slot generation so that reusing the slot
// invalidates older handles.
package slottedpage

import (
	"encoding/binary"
	"errors"
)

const (
	// DefaultPageSize is the page size used by NewPage.
	DefaultPageSize = 4096

	headerSize = 16 // magic(4) pageID(8) slotCount(2) recTop(2)
	slotSize   = 8  // offset(2) length(2) generation(4)

	tombstoneOffset = 0xFFFF
	magic           = 0x53504A31 // "SPJ1"
)

var (
	// ErrStaleHandle is returned when a handle does not match a live slot:
	// wrong page, wrong generation, deleted record, or out-of-range slot.
	ErrStaleHandle = errors.New("slottedpage: stale or invalid handle")
	// ErrNoSpace is returned when an insert or update does not fit, even
	// after compaction. The page is left completely unchanged.
	ErrNoSpace = errors.New("slottedpage: not enough space on page")
	// ErrBadPage is returned by Deserialize for malformed page images.
	ErrBadPage = errors.New("slottedpage: malformed page image")
	// ErrRecordTooLarge is returned when a record can never fit on an
	// empty page of this size.
	ErrRecordTooLarge = errors.New("slottedpage: record exceeds maximum page capacity")
)

// Handle is the public address of a record. It stays valid across
// compaction, and becomes stale once its slot is deleted and reused.
type Handle struct {
	PageID     uint64
	Slot       uint16
	Generation uint32
}

// Stats reports page space accounting. Directory and fragmentation are
// included explicitly.
type Stats struct {
	PageSize        int // total page bytes
	HeaderBytes     int // fixed page header
	DirectoryBytes  int // slot directory (all slots, incl. tombstones)
	RecordBytes     int // bytes held by live records
	FreeBytes       int // total reclaimable bytes (contiguous + fragmented)
	ContiguousFree  int // bytes between directory end and record region
	FragmentedBytes int // reclaimable bytes trapped in dead record space
	SlotCount       int // total directory entries
	LiveSlots       int // non-tombstone entries
}

type slot struct {
	offset     uint16
	length     uint16
	generation uint32
	tombstone  bool
}

// Page is a single slotted page. The zero value is not usable; construct
// with NewPage, NewPageSize, or Deserialize.
type Page struct {
	id     uint64
	slots  []slot
	buf    []byte
	recTop int // records occupy buf[recTop:]
}

// NewPage creates an empty page of DefaultPageSize with the given ID.
func NewPage(id uint64) *Page { return NewPageSize(id, DefaultPageSize) }

// NewPageSize creates an empty page of the given size with the given ID.
// Size must fit the header plus one slot and be addressable by uint16.
func NewPageSize(id uint64, size int) *Page {
	if size < headerSize+slotSize || size > 0x10000 {
		panic("slottedpage: invalid page size")
	}
	return &Page{id: id, buf: make([]byte, size), recTop: size}
}

// ID returns the page ID.
func (p *Page) ID() uint64 { return p.id }

// MaxRecordSize is the largest record that fits on an empty page.
func (p *Page) MaxRecordSize() int { return len(p.buf) - headerSize - slotSize }

func (p *Page) dirEnd() int { return headerSize + len(p.slots)*slotSize }

func (p *Page) contiguousFree() int { return p.recTop - p.dirEnd() }

func (p *Page) liveRecordBytes() int {
	n := 0
	for _, s := range p.slots {
		if !s.tombstone {
			n += int(s.length)
		}
	}
	return n
}

func (p *Page) totalFree() int {
	return len(p.buf) - headerSize - len(p.slots)*slotSize - p.liveRecordBytes()
}

// Stats returns the current space accounting for the page.
func (p *Page) Stats() Stats {
	contig := p.contiguousFree()
	free := p.totalFree()
	st := Stats{
		PageSize:        len(p.buf),
		HeaderBytes:     headerSize,
		DirectoryBytes:  len(p.slots) * slotSize,
		RecordBytes:     p.liveRecordBytes(),
		FreeBytes:       free,
		ContiguousFree:  contig,
		FragmentedBytes: free - contig,
		SlotCount:       len(p.slots),
	}
	for _, s := range p.slots {
		if !s.tombstone {
			st.LiveSlots++
		}
	}
	return st
}

func (p *Page) resolve(h Handle) (int, error) {
	if h.PageID != p.id || int(h.Slot) >= len(p.slots) {
		return 0, ErrStaleHandle
	}
	s := p.slots[h.Slot]
	if s.tombstone || s.generation != h.Generation {
		return 0, ErrStaleHandle
	}
	return int(h.Slot), nil
}

// Get returns a copy of the record addressed by h.
func (p *Page) Get(h Handle) ([]byte, error) {
	idx, err := p.resolve(h)
	if err != nil {
		return nil, err
	}
	s := p.slots[idx]
	out := make([]byte, s.length)
	copy(out, p.buf[s.offset:int(s.offset)+int(s.length)])
	return out, nil
}

// Insert stores rec and returns its handle. Tombstone slots are reused,
// which invalidates any handle issued before the slot was deleted.
func (p *Page) Insert(rec []byte) (Handle, error) {
	if len(rec) > p.MaxRecordSize() {
		return Handle{}, ErrRecordTooLarge
	}
	idx := -1
	for i := range p.slots {
		if p.slots[i].tombstone {
			idx = i
			break
		}
	}
	need := len(rec)
	if idx < 0 {
		need += slotSize
	}
	if need > p.totalFree() {
		return Handle{}, ErrNoSpace
	}
	if need > p.contiguousFree() {
		p.Compact()
	}
	if idx < 0 {
		p.slots = append(p.slots, slot{generation: 1})
		idx = len(p.slots) - 1
	}
	p.recTop -= len(rec)
	copy(p.buf[p.recTop:p.recTop+len(rec)], rec)
	gen := p.slots[idx].generation
	p.slots[idx] = slot{offset: uint16(p.recTop), length: uint16(len(rec)), generation: gen}
	return Handle{PageID: p.id, Slot: uint16(idx), Generation: gen}, nil
}

// Update replaces the record addressed by h, which may grow or shrink it.
// If the new record does not fit even after compaction, Update returns
// ErrNoSpace and the page is left completely unchanged.
func (p *Page) Update(h Handle, rec []byte) error {
	idx, err := p.resolve(h)
	if err != nil {
		return err
	}
	if len(rec) > p.MaxRecordSize() {
		return ErrRecordTooLarge
	}
	// Fit check happens before any mutation: growth must be covered by
	// total reclaimable space (compaction preserves live handles).
	if delta := len(rec) - int(p.slots[idx].length); delta > p.totalFree() {
		return ErrNoSpace
	}
	// Compact the other records, releasing the old record's space so the
	// new image can be placed at the top of the free region.
	p.compactExcept(idx)
	p.recTop -= len(rec)
	copy(p.buf[p.recTop:p.recTop+len(rec)], rec)
	p.slots[idx].offset = uint16(p.recTop)
	p.slots[idx].length = uint16(len(rec))
	return nil
}

// Delete removes the record addressed by h. The slot becomes a tombstone
// and its generation is bumped, so every previously issued handle for the
// slot (including h) is stale from now on.
func (p *Page) Delete(h Handle) error {
	idx, err := p.resolve(h)
	if err != nil {
		return err
	}
	p.slots[idx] = slot{offset: tombstoneOffset, generation: p.slots[idx].generation + 1, tombstone: true}
	return nil
}

// Compact packs all live records at the tail of the page, merging all
// fragmented space into one contiguous free region. Live handles and their
// generations are preserved; only physical offsets change.
func (p *Page) Compact() {
	p.compactExcept(-1)
}

// compactExcept packs all live records except the slot skip at the tail of
// the page, preserving slot order by physical offset.
func (p *Page) compactExcept(skip int) {
	order := make([]int, 0, len(p.slots))
	for i := range p.slots {
		if !p.slots[i].tombstone && i != skip {
			order = append(order, i)
		}
	}
	// Pack from the page tail downward, descending by current offset.
	for i := 1; i < len(order); i++ {
		for j := i; j > 0 && p.slots[order[j-1]].offset < p.slots[order[j]].offset; j-- {
			order[j-1], order[j] = order[j], order[j-1]
		}
	}
	dst := len(p.buf)
	for _, i := range order {
		s := p.slots[i]
		dst -= int(s.length)
		copy(p.buf[dst:dst+int(s.length)], p.buf[s.offset:int(s.offset)+int(s.length)])
		p.slots[i].offset = uint16(dst)
	}
	p.recTop = dst
}

// Serialize returns the page image: header, slot directory, then the raw
// record region. The image round-trips through Deserialize.
func (p *Page) Serialize() []byte {
	out := make([]byte, len(p.buf))
	binary.LittleEndian.PutUint32(out[0:], magic)
	binary.LittleEndian.PutUint64(out[4:], p.id)
	binary.LittleEndian.PutUint16(out[12:], uint16(len(p.slots)))
	binary.LittleEndian.PutUint16(out[14:], uint16(p.recTop))
	for i, s := range p.slots {
		off := headerSize + i*slotSize
		binary.LittleEndian.PutUint16(out[off:], s.offset)
		binary.LittleEndian.PutUint16(out[off+2:], s.length)
		binary.LittleEndian.PutUint32(out[off+4:], s.generation)
	}
	copy(out[p.recTop:], p.buf[p.recTop:])
	return out
}

// Deserialize rebuilds a page from an image produced by Serialize.
func Deserialize(data []byte) (*Page, error) {
	if len(data) < headerSize+slotSize || len(data) > 0x10000 {
		return nil, ErrBadPage
	}
	if binary.LittleEndian.Uint32(data[0:]) != magic {
		return nil, ErrBadPage
	}
	p := &Page{
		id:     binary.LittleEndian.Uint64(data[4:]),
		recTop: int(binary.LittleEndian.Uint16(data[14:])),
		buf:    make([]byte, len(data)),
	}
	n := int(binary.LittleEndian.Uint16(data[12:]))
	if headerSize+n*slotSize > len(data) || p.recTop < headerSize+n*slotSize || p.recTop > len(data) {
		return nil, ErrBadPage
	}
	for i := 0; i < n; i++ {
		off := headerSize + i*slotSize
		s := slot{
			offset:     binary.LittleEndian.Uint16(data[off:]),
			length:     binary.LittleEndian.Uint16(data[off+2:]),
			generation: binary.LittleEndian.Uint32(data[off+4:]),
		}
		if s.offset == tombstoneOffset {
			s.tombstone = true
			s.offset = tombstoneOffset
		} else {
			end := int(s.offset) + int(s.length)
			if int(s.offset) < p.recTop || end > len(data) {
				return nil, ErrBadPage
			}
		}
		p.slots = append(p.slots, s)
	}
	copy(p.buf[p.recTop:], data[p.recTop:])
	return p, nil
}
