// Package ttfglyph reads glyph outlines directly from TrueType font files.
//
// The font data is accessed through an io.ReaderAt: only the tables that are
// actually needed are pulled in, and every multi-byte read goes through a
// bounds-checked cursor so that a malformed table produces an error instead
// of an out-of-bounds access.
package ttfglyph

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync"
)

// Safety limits. They are generous for normal fonts while bounding the work a
// malicious or corrupt font can force on the pipeline.
const (
	// maxRecursion bounds nesting of composite glyph components.
	maxRecursion = 16
	// maxPoints bounds the number of points a single simple glyph may declare.
	maxPoints = 16384
	// maxContours bounds the number of contours a single simple glyph may declare.
	maxContours = 1024
	// maxTotalPoints bounds the accumulated points of one expanded glyph,
	// including every component of a composite glyph.
	maxTotalPoints = 65535
	// maxTables bounds the number of entries in the table directory.
	maxTables = 1000
)

var (
	// ErrCorrupt is returned when a table contains inconsistent or truncated data.
	ErrCorrupt = errors.New("ttfglyph: corrupt font table")
	// ErrUnsupported is returned when the font uses an unsupported feature.
	ErrUnsupported = errors.New("ttfglyph: unsupported font feature")
	// ErrNotFound is returned when a required table or glyph is missing.
	ErrNotFound = errors.New("ttfglyph: not found")
)

func corruptf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrCorrupt, fmt.Sprintf(format, args...))
}

// Point is a single outline point in font units (FUnits). OnCurve marks the
// point as an on-curve point; otherwise it is an off-curve control point.
type Point struct {
	X, Y    int32
	OnCurve bool
}

// Contour is one closed contour of a glyph outline. The points form a closed
// loop; implicit midpoints between consecutive off-curve points are already
// materialized, so every quadratic segment can be rendered directly.
type Contour struct {
	Points []Point
}

// Metrics holds the horizontal metrics of a glyph in font units.
type Metrics struct {
	AdvanceWidth    uint16
	LeftSideBearing int16
}

// Font provides on-demand access to the tables of a TrueType font.
type Font struct {
	r      io.ReaderAt
	size   int64
	tables map[string]tableEntry

	headOnce sync.Once
	head     headInfo
	headErr  error

	maxpOnce  sync.Once
	numGlyphs uint16
	maxpErr   error

	locaOnce sync.Once
	loca     []byte
	locaErr  error

	cmapOnce sync.Once
	cmap     cmapSubtable
	cmapErr  error
}

type tableEntry struct {
	offset uint32
	length uint32
}

type headInfo struct {
	indexToLocFormat int16
}

// New parses the offset table and table directory of a TrueType font. No glyph
// data is read until a lookup or outline is requested.
func New(r io.ReaderAt, size int64) (*Font, error) {
	if size < 12 {
		return nil, corruptf("font smaller than sfnt header")
	}
	f := &Font{r: r, size: size, tables: make(map[string]tableEntry)}

	var hdr [12]byte
	if err := f.readFull(hdr[:], 0); err != nil {
		return nil, err
	}
	sfntVersion := binary.BigEndian.Uint32(hdr[0:4])
	switch sfntVersion {
	case 0x00010000, 0x74727565: // 1.0 / 'true'
	case 0x4F54544F: // 'OTTO' CFF outlines
		return nil, fmt.Errorf("%w: CFF/OpenType outlines", ErrUnsupported)
	case 0x74797031: // 'typ1'
	default:
		return nil, ErrUnsupported
	}
	numTables := binary.BigEndian.Uint16(hdr[4:6])
	if numTables == 0 || int(numTables) > maxTables {
		return nil, corruptf("invalid table count %d", numTables)
	}

	dir := make([]byte, int(numTables)*16)
	if err := f.readFull(dir, 12); err != nil {
		return nil, err
	}
	for i := 0; i < int(numTables); i++ {
		e := dir[i*16 : (i+1)*16]
		tag := string(e[0:4])
		entry := tableEntry{
			offset: binary.BigEndian.Uint32(e[8:12]),
			length: binary.BigEndian.Uint32(e[12:16]),
		}
		if !f.rangeOK(int64(entry.offset), int64(entry.length)) {
			return nil, corruptf("table %q extends past end of font", tag)
		}
		if _, dup := f.tables[tag]; dup {
			return nil, corruptf("duplicate table %q", tag)
		}
		f.tables[tag] = entry
	}
	return f, nil
}

// NumGlyphs returns the number of glyphs declared by the maxp table.
func (f *Font) NumGlyphs() (uint16, error) {
	f.maxpOnce.Do(func() {
		b, err := f.table("maxp", 6)
		if err != nil {
			f.maxpErr = err
			return
		}
		f.numGlyphs = binary.BigEndian.Uint16(b[4:6])
	})
	return f.numGlyphs, f.maxpErr
}

func (f *Font) loadHead() {
	b, err := f.table("head", 54)
	if err != nil {
		f.headErr = err
		return
	}
	format := int16(binary.BigEndian.Uint16(b[0:2]))
	if format != 0x0001 {
		f.headErr = corruptf("unsupported head.magicNumber/version %#x", format)
		return
	}
	lf := int16(binary.BigEndian.Uint16(b[50:52]))
	if lf != 0 && lf != 1 {
		f.headErr = corruptf("invalid indexToLocFormat %d", lf)
		return
	}
	f.head = headInfo{indexToLocFormat: lf}
}

func (f *Font) headTable() (headInfo, error) {
	f.headOnce.Do(f.loadHead)
	return f.head, f.headErr
}

func (f *Font) locaTable() ([]byte, error) {
	f.locaOnce.Do(func() {
		head, err := f.headTable()
		if err != nil {
			f.locaErr = err
			return
		}
		n, err := f.NumGlyphs()
		if err != nil {
			f.locaErr = err
			return
		}
		var entrySize int
		if head.indexToLocFormat == 0 {
			entrySize = 2
		} else {
			entrySize = 4
		}
		b, err := f.table("loca", entrySize*(int(n)+1))
		if err != nil {
			f.locaErr = err
			return
		}
		f.loca = b
	})
	return f.loca, f.locaErr
}

// table returns the full contents of a directory-listed table, requiring at
// least minLen bytes.
func (f *Font) table(tag string, minLen int) ([]byte, error) {
	e, ok := f.tables[tag]
	if !ok {
		return nil, fmt.Errorf("%w: table %q", ErrNotFound, tag)
	}
	if int64(e.length) < int64(minLen) {
		return nil, corruptf("table %q too short: %d bytes", tag, e.length)
	}
	b := make([]byte, e.length)
	if err := f.readFull(b, int64(e.offset)); err != nil {
		return nil, err
	}
	return b, nil
}

// readTable returns the raw bytes [off, off+n) of a table without copying the
// whole table first; the glyf reader uses it for on-demand access.
func (f *Font) readTable(e tableEntry, off, n int64) ([]byte, error) {
	if off < 0 || n < 0 || off+n > int64(e.length) {
		return nil, corruptf("read outside table bounds (off=%d n=%d len=%d)", off, n, e.length)
	}
	b := make([]byte, n)
	if err := f.readFull(b, int64(e.offset)+off); err != nil {
		return nil, err
	}
	return b, nil
}

func (f *Font) rangeOK(off, n int64) bool {
	if off < 0 || n < 0 {
		return false
	}
	return off+n <= f.size && off+n >= off
}

// readFull reads exactly len(b) bytes at off. Every ReaderAt call in the
// package funnels through here so truncated fonts cannot cause overruns.
func (f *Font) readFull(b []byte, off int64) error {
	if !f.rangeOK(off, int64(len(b))) {
		return corruptf("read of %d bytes at %d outside font (size %d)", len(b), off, f.size)
	}
	n, err := f.r.ReadAt(b, off)
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	if n != len(b) {
		return corruptf("short read at %d: got %d bytes, want %d", off, n, len(b))
	}
	return nil
}

// cursor is a bounds-checked big-endian reader over a table slice.
type cursor struct {
	b   []byte
	pos int
}

func newCursor(b []byte) *cursor { return &cursor{b: b} }

func (c *cursor) u8() (byte, error) {
	if c.pos+1 > len(c.b) {
		return 0, corruptf("unexpected end of table at byte %d", c.pos)
	}
	v := c.b[c.pos]
	c.pos++
	return v, nil
}

func (c *cursor) u16() (uint16, error) {
	if c.pos+2 > len(c.b) {
		return 0, corruptf("unexpected end of table at byte %d", c.pos)
	}
	v := binary.BigEndian.Uint16(c.b[c.pos : c.pos+2])
	c.pos += 2
	return v, nil
}

func (c *cursor) i16() (int16, error) {
	v, err := c.u16()
	return int16(v), err
}

func (c *cursor) u32() (uint32, error) {
	if c.pos+4 > len(c.b) {
		return 0, corruptf("unexpected end of table at byte %d", c.pos)
	}
	v := binary.BigEndian.Uint32(c.b[c.pos : c.pos+4])
	c.pos += 4
	return v, nil
}
