package compactidx

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
)

var (
	// ErrClosed is returned by any query issued after its handle (view or
	// cursor) or the owning Index has been closed.
	ErrClosed = errors.New("compactidx: query on a closed handle")
	// ErrNotLoaded is returned when a view is requested before any
	// successful Load.
	ErrNotLoaded = errors.New("compactidx: no mapping loaded")
	// errUnvalidatedRead is a defense-in-depth guard: validated images must
	// never trigger it. It fires if any code path tries to read bytes
	// outside the validated regions of the mapping.
	errUnvalidatedRead = errors.New("compactidx: attempt to read unvalidated bytes")
)

// Index owns the current mapping and hands out read-only views. It is safe
// for concurrent use.
type Index struct {
	mu       sync.Mutex
	cur      *mapping
	closed   bool
	unmapped int
}

// NewIndex returns an empty index with no mapping.
func NewIndex() *Index { return &Index{} }

// Load validates the whole offset graph of data and, only if it is fully
// trustworthy, installs it as the current mapping. A failed load leaves the
// previous mapping untouched. The previous mapping, if any, is released
// lazily: it stays mapped until its last outstanding view/cursor borrow is
// released, so old handles keep reading the old generation and are never
// rebound to the new one.
//
// data must not be mutated after a successful Load.
func (idx *Index) Load(data []byte) error {
	lay, err := validate(data)
	if err != nil {
		return err
	}
	idx.mu.Lock()
	if idx.closed {
		idx.mu.Unlock()
		return ErrClosed
	}
	old := idx.cur
	m := &mapping{gen: 1, data: data, lay: lay, idx: idx}
	if old != nil {
		m.gen = old.gen + 1
	}
	idx.cur = m
	idx.mu.Unlock()
	if old != nil {
		old.requestClose()
	}
	return nil
}

// View opens a read-only view borrowing the current mapping. Multiple views
// share the same mapping lifetime.
func (idx *Index) View() (*View, error) {
	idx.mu.Lock()
	if idx.closed {
		idx.mu.Unlock()
		return nil, ErrClosed
	}
	m := idx.cur
	idx.mu.Unlock()
	if m == nil {
		return nil, ErrNotLoaded
	}
	if !m.borrow() {
		return nil, ErrClosed
	}
	return &View{idx: idx, m: m, gen: m.gen}, nil
}

// Close requests shutdown. Queries on any handle fail from this point, but
// the underlying mapping is only unmapped once the last outstanding borrow
// (view or cursor) is released.
func (idx *Index) Close() {
	idx.mu.Lock()
	if idx.closed {
		idx.mu.Unlock()
		return
	}
	idx.closed = true
	m := idx.cur
	idx.cur = nil
	idx.mu.Unlock()
	if m != nil {
		m.requestClose()
	}
}

// Unmapped reports how many mappings have been fully unmapped so far. It
// makes the deferred-release behavior observable.
func (idx *Index) Unmapped() int {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	return idx.unmapped
}

func (idx *Index) isClosed() bool {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	return idx.closed
}

func (idx *Index) noteUnmapped() {
	idx.mu.Lock()
	idx.unmapped++
	idx.mu.Unlock()
}

// Accesses returns the byte ranges read from the current mapping so far,
// for cross-checking against an independent layout checker.
func (idx *Index) Accesses() []Range {
	idx.mu.Lock()
	m := idx.cur
	idx.mu.Unlock()
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Range(nil), m.accesses...)
}

// ValidatedRegions returns the validated byte regions of the current
// mapping.
func (idx *Index) ValidatedRegions() []Range {
	idx.mu.Lock()
	m := idx.cur
	idx.mu.Unlock()
	if m == nil {
		return nil
	}
	return append([]Range(nil), m.lay.regions...)
}

// Generation returns the generation of the current mapping (0 if none).
func (idx *Index) Generation() uint64 {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	if idx.cur == nil {
		return 0
	}
	return idx.cur.gen
}

// mapping is one leased generation of an image.
type mapping struct {
	gen  uint64
	data []byte
	lay  *layout
	idx  *Index

	mu       sync.Mutex
	borrows  int
	closing  bool
	unmapped bool
	accesses []Range
}

// borrow takes a lease. It fails only once the mapping is unmapped.
func (m *mapping) borrow() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.unmapped {
		return false
	}
	m.borrows++
	return true
}

// release drops a lease, unmapping on the last one after a close request.
func (m *mapping) release() {
	m.mu.Lock()
	m.borrows--
	last := m.closing && m.borrows == 0 && !m.unmapped
	if last {
		m.unmapped = true
	}
	m.mu.Unlock()
	if last {
		m.idx.noteUnmapped()
	}
}

// requestClose marks the mapping for unmapping once drained.
func (m *mapping) requestClose() {
	m.mu.Lock()
	m.closing = true
	last := m.borrows == 0 && !m.unmapped
	if last {
		m.unmapped = true
	}
	m.mu.Unlock()
	if last {
		m.idx.noteUnmapped()
	}
}

func (m *mapping) isUnmapped() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.unmapped
}

// read returns data[off:off+n), refusing any byte outside the validated
// regions and recording the access for the independent layout checker.
func (m *mapping) read(off, n uint64) ([]byte, error) {
	if !m.lay.contains(off, n) {
		return nil, fmt.Errorf("%w: [%d,%d)", errUnvalidatedRead, off, off+n)
	}
	end := off + n
	m.mu.Lock()
	m.accesses = append(m.accesses, Range{off, end})
	m.mu.Unlock()
	return m.data[off:end], nil
}

func (m *mapping) readUvarint(off uint64) (uint64, uint64, error) {
	var x uint64
	var s uint
	for i := 0; i < maxUvarintLen; i++ {
		b, err := m.read(off+uint64(i), 1)
		if err != nil {
			return 0, 0, err
		}
		if b[0] < 0x80 {
			if i == maxUvarintLen-1 && b[0] > 1 {
				return 0, 0, fmt.Errorf("%w: varint overflows uint64 at offset %d", ErrBadRecord, off)
			}
			return x | uint64(b[0])<<s, off + uint64(i) + 1, nil
		}
		x |= uint64(b[0]&0x7f) << s
		s += 7
	}
	return 0, 0, fmt.Errorf("%w: unterminated varint at offset %d", ErrBadRecord, off)
}

// keyAt reads the head-record key at table offset off.
func (m *mapping) keyAt(off uint64) ([]byte, error) {
	keyLen, pos, err := m.readUvarint(off)
	if err != nil {
		return nil, err
	}
	return m.read(pos, keyLen)
}

// valueAt reassembles the value of the chain starting at off.
func (m *mapping) valueAt(off uint64) ([]byte, error) {
	var out []byte
	for {
		keyLen, pos, err := m.readUvarint(off)
		if err != nil {
			return nil, err
		}
		pos += keyLen // key bytes already validated; not needed here
		valLen, pos, err := m.readUvarint(pos)
		if err != nil {
			return nil, err
		}
		chunk, err := m.read(pos, valLen)
		if err != nil {
			return nil, err
		}
		out = append(out, chunk...)
		pos += valLen
		tag, err := m.read(pos, 1)
		if err != nil {
			return nil, err
		}
		pos++
		switch tag[0] {
		case linkNone:
			return out, nil
		case linkMore:
			raw, err := m.read(pos, 8)
			if err != nil {
				return nil, err
			}
			off = binary.LittleEndian.Uint64(raw)
		default:
			return nil, fmt.Errorf("%w: unknown link tag 0x%02x", ErrBadRecord, tag[0])
		}
	}
}

// get binary-searches the offset table for key.
func (m *mapping) get(key string) ([]byte, bool, error) {
	kb := []byte(key)
	lo, hi := 0, len(m.lay.table)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		k, err := m.keyAt(m.lay.table[mid])
		if err != nil {
			return nil, false, err
		}
		if bytes.Compare(k, kb) < 0 {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo >= len(m.lay.table) {
		return nil, false, nil
	}
	k, err := m.keyAt(m.lay.table[lo])
	if err != nil {
		return nil, false, err
	}
	if !bytes.Equal(k, kb) {
		return nil, false, nil
	}
	v, err := m.valueAt(m.lay.table[lo])
	if err != nil {
		return nil, false, err
	}
	return v, true, nil
}

// View is a read-only handle bound to one mapping generation. It keeps
// serving its own generation even after the Index loads a newer image, and
// is never rebound. Safe for concurrent use.
type View struct {
	idx *Index
	m   *mapping
	gen uint64

	mu     sync.Mutex
	closed bool
}

// Generation returns the mapping generation this view is bound to.
func (v *View) Generation() uint64 { return v.gen }

// Get looks up key by binary search over the offset table.
func (v *View) Get(key string) ([]byte, bool, error) {
	v.mu.Lock()
	closed := v.closed
	v.mu.Unlock()
	if closed || v.idx.isClosed() || v.m.isUnmapped() {
		return nil, false, ErrClosed
	}
	return v.m.get(key)
}

// Cursor opens an iterator borrowing the same mapping as the view. The
// cursor holds its own lease, independent of the view's.
func (v *View) Cursor() (*Cursor, error) {
	v.mu.Lock()
	closed := v.closed
	v.mu.Unlock()
	if closed || v.idx.isClosed() {
		return nil, ErrClosed
	}
	if !v.m.borrow() {
		return nil, ErrClosed
	}
	return &Cursor{idx: v.idx, m: v.m}, nil
}

// Close releases the view's borrow. Closing one view never invalidates
// other views or cursors sharing the mapping.
func (v *View) Close() {
	v.mu.Lock()
	if v.closed {
		v.mu.Unlock()
		return
	}
	v.closed = true
	v.mu.Unlock()
	v.m.release()
}

// Cursor iterates entries in key order over one mapping generation.
type Cursor struct {
	idx *Index
	m   *mapping

	pos    int
	key    string
	val    []byte
	err    error
	closed bool
}

// Next advances to the next entry, returning false at the end or on error
// (see Err). After the owning Index is closed, Next reports ErrClosed.
func (c *Cursor) Next() bool {
	if c.closed || c.err != nil {
		return false
	}
	if c.idx.isClosed() {
		c.err = ErrClosed
		return false
	}
	if c.m.isUnmapped() {
		c.err = ErrClosed
		return false
	}
	if c.pos >= len(c.m.lay.table) {
		return false
	}
	off := c.m.lay.table[c.pos]
	k, err := c.m.keyAt(off)
	if err != nil {
		c.err = err
		return false
	}
	v, err := c.m.valueAt(off)
	if err != nil {
		c.err = err
		return false
	}
	c.key = string(k)
	c.val = v
	c.pos++
	return true
}

// Key returns the current entry's key.
func (c *Cursor) Key() string { return c.key }

// Value returns the current entry's reassembled value.
func (c *Cursor) Value() []byte { return c.val }

// Err returns the first error encountered, if any.
func (c *Cursor) Err() error { return c.err }

// Close releases the cursor's borrow on the mapping.
func (c *Cursor) Close() {
	if c.closed {
		return
	}
	c.closed = true
	c.m.release()
}
