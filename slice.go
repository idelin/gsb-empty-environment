package blockstore

import (
	"encoding/binary"
	"io"
)

// Slice is a persistent, immutable byte sequence. Its authoritative block
// chain is the encoded index (enc); exts is the decoded cache of the same
// information. A live (unreleased) Slice acts as a borrow of its physical
// blocks: they cannot be reclaimed while it exists.
type Slice struct {
	st       *Store
	enc      []byte // encoded index, source of truth
	exts     []uint64
	length   int
	released bool
}

// newSlice registers a slice over already-committed extents.
func (st *Store) newSlice(length int, exts []uint64) *Slice {
	s := &Slice{st: st, length: length, exts: exts}
	s.enc = encodeIndex(length, exts)
	st.slices[s] = struct{}{}
	return s
}

// NewSlice builds a slice holding data, allocating fresh blocks.
func (st *Store) NewSlice(data []byte) (*Slice, error) {
	e := newEditor(st, nil, 0)
	if err := e.splice(0, 0, data); err != nil {
		return nil, err
	}
	return e.Commit()
}

// Len returns the logical length in bytes.
func (s *Slice) Len() int { return s.length }

// IndexBytes returns the size of the slice's encoded block index in bytes.
func (s *Slice) IndexBytes() int { return len(s.enc) }

// RawIndex returns a copy of the encoded block index.
func (s *Slice) RawIndex() []byte {
	out := make([]byte, len(s.enc))
	copy(out, s.enc)
	return out
}

// BlockIDs returns a copy of the decoded physical block id chain.
func (s *Slice) BlockIDs() []uint64 {
	out := make([]uint64, len(s.exts))
	copy(out, s.exts)
	return out
}

// Snapshot returns a new independent branch sharing every physical block
// with s. It copies nothing; later edits to either branch do not affect
// the other.
func (s *Slice) Snapshot() *Slice {
	if s.released {
		panic("blockstore: Snapshot on released slice")
	}
	st := s.st
	counts := make(map[uint64]int, len(s.exts))
	for _, id := range s.exts {
		counts[id]++
	}
	for id, n := range counts {
		st.blocks[id].refs += n
	}
	return st.newSlice(s.length, append([]uint64(nil), s.exts...))
}

// Release drops this slice's borrow of its blocks. Blocks no longer
// referenced by any live slice are reclaimed and leave the ledger.
// Release is idempotent.
func (s *Slice) Release() {
	if s.released {
		return
	}
	s.released = true
	st := s.st
	delete(st.slices, s)
	counts := make(map[uint64]int, len(s.exts))
	for _, id := range s.exts {
		counts[id]++
	}
	for id, n := range counts {
		b := st.blocks[id]
		b.refs -= n
		if b.refs == 0 {
			delete(st.blocks, id)
			st.committed--
		}
	}
}

// ReadAt implements io.ReaderAt with cross-block reads. Reading an empty
// slice or reading at/after the end returns io.EOF, never a panic.
func (s *Slice) ReadAt(p []byte, off int64) (int, error) {
	if s.released {
		return 0, ErrReleased
	}
	if off < 0 {
		return 0, ErrOutOfRange
	}
	if off >= int64(s.length) {
		return 0, io.EOF
	}
	n := copy(p, s.readBytes(int(off), len(p)))
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

// readBytes reads up to n bytes starting at off; off must be in bounds.
func (s *Slice) readBytes(off, n int) []byte {
	if n > s.length-off {
		n = s.length - off
	}
	out := make([]byte, 0, n)
	bs := s.st.bs
	for n > 0 {
		b := s.st.blocks[s.exts[off/bs]]
		inner := off % bs
		k := bs - inner
		if k > n {
			k = n
		}
		out = append(out, b.data[inner:inner+k]...)
		off += k
		n -= k
	}
	return out
}

// ReadRange returns exactly n bytes starting at off.
func (s *Slice) ReadRange(off, n int) ([]byte, error) {
	if s.released {
		return nil, ErrReleased
	}
	if off < 0 || n < 0 || off+n > s.length {
		return nil, ErrOutOfRange
	}
	return s.readBytes(off, n), nil
}

// Bytes materializes the whole logical sequence.
func (s *Slice) Bytes() []byte {
	if s.released || s.length == 0 {
		return nil
	}
	return s.readBytes(0, s.length)
}

func (s *Slice) uintN(off, n int) ([]byte, error) {
	if s.released {
		return nil, ErrReleased
	}
	if off < 0 || off+n > s.length {
		return nil, ErrOutOfRange
	}
	return s.readBytes(off, n), nil
}

// Uint16 reads 2 bytes at off with the given byte order, across block
// boundaries if needed.
func (s *Slice) Uint16(off int, order binary.ByteOrder) (uint16, error) {
	b, err := s.uintN(off, 2)
	if err != nil {
		return 0, err
	}
	return order.Uint16(b), nil
}

// Uint32 reads 4 bytes at off with the given byte order, across block
// boundaries if needed.
func (s *Slice) Uint32(off int, order binary.ByteOrder) (uint32, error) {
	b, err := s.uintN(off, 4)
	if err != nil {
		return 0, err
	}
	return order.Uint32(b), nil
}

// Uint64 reads 8 bytes at off with the given byte order, across block
// boundaries if needed.
func (s *Slice) Uint64(off int, order binary.ByteOrder) (uint64, error) {
	b, err := s.uintN(off, 8)
	if err != nil {
		return 0, err
	}
	return order.Uint64(b), nil
}

// Edit opens a multi-operation editor on this slice's content. The editor
// must be committed or cancelled.
func (s *Slice) Edit() *Editor {
	if s.released {
		panic("blockstore: Edit on released slice")
	}
	return newEditor(s.st, s.exts, s.length)
}

// Overwrite returns a new slice with data written at off, replacing
// len(data) existing bytes. off+len(data) must be within bounds.
func (s *Slice) Overwrite(off int, data []byte) (*Slice, error) {
	e := s.Edit()
	if err := e.Overwrite(off, data); err != nil {
		return nil, err
	}
	return e.Commit()
}

// Insert returns a new slice with data inserted at off.
func (s *Slice) Insert(off int, data []byte) (*Slice, error) {
	e := s.Edit()
	if err := e.Insert(off, data); err != nil {
		return nil, err
	}
	return e.Commit()
}

// Delete returns a new slice with n bytes removed starting at off.
func (s *Slice) Delete(off, n int) (*Slice, error) {
	e := s.Edit()
	if err := e.Delete(off, n); err != nil {
		return nil, err
	}
	return e.Commit()
}

// Move returns a new slice with the n bytes at src removed and re-inserted
// at dst, where dst is a position in the sequence after the removal. The
// moved bytes are read from the pre-operation content, so overlapping
// ranges are safe.
func (s *Slice) Move(dst, src, n int) (*Slice, error) {
	e := s.Edit()
	if err := e.Move(dst, src, n); err != nil {
		return nil, err
	}
	return e.Commit()
}

// CopyWithin returns a new slice where the n bytes at src are copied over
// the n bytes at dst. The source is read from the pre-operation content
// (memmove semantics), so overlapping ranges are safe.
func (s *Slice) CopyWithin(dst, src, n int) (*Slice, error) {
	e := s.Edit()
	if err := e.CopyWithin(dst, src, n); err != nil {
		return nil, err
	}
	return e.Commit()
}
