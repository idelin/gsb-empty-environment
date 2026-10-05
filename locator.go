// Package locator maintains an incrementally updatable line index over a
// byte buffer and converts byte offsets into display coordinates: 1-based
// line numbers, Unicode-scalar-value columns and UTF-16 code-unit columns.
//
// Line terminators recognized are LF, CRLF and a lone CR; a CRLF pair split
// across two edits (e.g. an Append ending in '\r' followed by one starting
// with '\n') is still treated as a single terminator.
//
// Invalid UTF-8 is decoded one replacement character (U+FFFD) per bad byte,
// matching utf8.DecodeRune semantics, and every bad byte offset is recorded
// so callers can mark the affected glyphs.
package locator

import (
	"sort"
	"unicode/utf8"
)

// Position describes the display coordinates of a byte offset.
type Position struct {
	Offset      int  // byte offset the position was computed for
	Line        int  // 1-based line number
	Column      int  // 1-based column in Unicode scalar values
	ColumnUTF16 int  // 1-based column in UTF-16 code units
	Replaced    bool // byte at Offset is invalid UTF-8 (renders as U+FFFD)
}

// Index is an incrementally updatable line index over a byte buffer.
type Index struct {
	buf       []byte
	lineStart []int // byte offset of each line's first byte; lineStart[0] == 0
	invalid   []int // sorted byte offsets of invalid UTF-8 bytes
}

// New returns an empty Index.
func New() *Index {
	return &Index{lineStart: []int{0}}
}

// FromBytes returns an Index over a copy of b.
func FromBytes(b []byte) *Index {
	ix := New()
	ix.Append(b)
	return ix
}

// FromString returns an Index over s.
func FromString(s string) *Index {
	return FromBytes([]byte(s))
}

// Len returns the buffer length in bytes.
func (ix *Index) Len() int { return len(ix.buf) }

// Bytes returns a copy of the buffer.
func (ix *Index) Bytes() []byte {
	out := make([]byte, len(ix.buf))
	copy(out, ix.buf)
	return out
}

// String returns the buffer as a string.
func (ix *Index) String() string { return string(ix.buf) }

// Append adds p at the end of the buffer.
func (ix *Index) Append(p []byte) { ix.Insert(len(ix.buf), p) }

// Insert splices p into the buffer at byte offset and reindexes the tail.
func (ix *Index) Insert(offset int, p []byte) {
	if offset < 0 {
		offset = 0
	}
	if offset > len(ix.buf) {
		offset = len(ix.buf)
	}
	if len(p) == 0 {
		return
	}
	tail := make([]byte, len(ix.buf)-offset)
	copy(tail, ix.buf[offset:])
	ix.buf = append(ix.buf[:offset], p...)
	ix.buf = append(ix.buf, tail...)
	ix.reindex(offset)
}

// Delete removes the byte range [start, end) and reindexes the tail.
func (ix *Index) Delete(start, end int) {
	if start < 0 {
		start = 0
	}
	if end > len(ix.buf) {
		end = len(ix.buf)
	}
	if start >= end {
		return
	}
	ix.buf = append(ix.buf[:start], ix.buf[end:]...)
	ix.reindex(start)
}

// reindex rebuilds line starts and invalid-byte marks from the line
// containing pos to the end of the buffer. A CR immediately before pos is
// included so a CRLF pair split by the edit merges into one terminator.
func (ix *Index) reindex(pos int) {
	if pos > 0 && ix.buf[pos-1] == '\r' {
		pos--
	}
	i := sort.Search(len(ix.lineStart), func(i int) bool { return ix.lineStart[i] > pos }) - 1
	if i < 0 {
		i = 0
	}
	start := ix.lineStart[i]

	starts := make([]int, 0, 8)
	for j := start; j < len(ix.buf); {
		switch ix.buf[j] {
		case '\n':
			j++
			starts = append(starts, j)
		case '\r':
			if j+1 < len(ix.buf) && ix.buf[j+1] == '\n' {
				j += 2
			} else {
				j++
			}
			starts = append(starts, j)
		default:
			j++
		}
	}
	ix.lineStart = append(ix.lineStart[:i+1], starts...)

	// Rebuild invalid-byte marks from start onward. start is a line start
	// (or 0), and no multi-byte rune ever spans a newline byte, so decoding
	// from start is aligned with the rest of the buffer.
	keep := sort.Search(len(ix.invalid), func(k int) bool { return ix.invalid[k] >= start })
	inv := make([]int, 0, keep+8)
	inv = append(inv, ix.invalid[:keep]...)
	for j := start; j < len(ix.buf); {
		r, size := utf8.DecodeRune(ix.buf[j:])
		if r == utf8.RuneError && size == 1 {
			inv = append(inv, j)
		}
		j += size
	}
	ix.invalid = inv
}

// LineCount returns the number of lines. A trailing terminator leaves a
// final empty line, so "a\n" has 2 lines and "" has 1.
func (ix *Index) LineCount() int { return len(ix.lineStart) }

// lineOf returns the 0-based index of the line containing offset.
func (ix *Index) lineOf(offset int) int {
	i := sort.Search(len(ix.lineStart), func(i int) bool { return ix.lineStart[i] > offset }) - 1
	if i < 0 {
		return 0
	}
	return i
}

// LineBounds returns the byte range [start, end) of 1-based line,
// excluding its terminator. ok is false if line is out of range.
func (ix *Index) LineBounds(line int) (start, end int, ok bool) {
	if line < 1 || line > len(ix.lineStart) {
		return 0, 0, false
	}
	start = ix.lineStart[line-1]
	if line == len(ix.lineStart) {
		return start, len(ix.buf), true
	}
	end = ix.lineStart[line] // strip the terminator
	if ix.buf[end-1] == '\n' {
		end--
		if end-1 >= start && ix.buf[end-1] == '\r' {
			end--
		}
	} else { // lone '\r'
		end--
	}
	return start, end, true
}

// InvalidOffsets returns the absolute byte offsets of invalid UTF-8 bytes
// on 1-based line, or nil if line is out of range.
func (ix *Index) InvalidOffsets(line int) []int {
	start, end, ok := ix.LineBounds(line)
	if !ok {
		return nil
	}
	lo := sort.Search(len(ix.invalid), func(k int) bool { return ix.invalid[k] >= start })
	hi := sort.Search(len(ix.invalid), func(k int) bool { return ix.invalid[k] >= end })
	out := make([]int, hi-lo)
	copy(out, ix.invalid[lo:hi])
	return out
}

// IsInvalid reports whether the byte at offset is invalid UTF-8.
func (ix *Index) IsInvalid(offset int) bool {
	k := sort.Search(len(ix.invalid), func(k int) bool { return ix.invalid[k] >= offset })
	return k < len(ix.invalid) && ix.invalid[k] == offset
}

func utf16Len(r rune) int {
	if r > 0xFFFF {
		return 2
	}
	return 1
}

// Locate converts a byte offset into display coordinates. Offsets are
// clamped to [0, Len]. If offset falls inside a multi-byte rune, the
// coordinates of that rune's first byte are returned.
func (ix *Index) Locate(offset int) Position {
	if offset < 0 {
		offset = 0
	}
	if offset > len(ix.buf) {
		offset = len(ix.buf)
	}
	line := ix.lineOf(offset)
	col, col16 := 1, 1
	for p := ix.lineStart[line]; p < offset; {
		r, size := utf8.DecodeRune(ix.buf[p:])
		if p+size > offset { // offset is mid-rune
			break
		}
		col++
		col16 += utf16Len(r)
		p += size
	}
	return Position{
		Offset:      offset,
		Line:        line + 1,
		Column:      col,
		ColumnUTF16: col16,
		Replaced:    offset < len(ix.buf) && ix.IsInvalid(offset),
	}
}

// Offset converts a 1-based line and 1-based Unicode-scalar column back to
// a byte offset. ok is false if line is out of range or col points past the
// end of the line; in the latter case the line's end offset is returned.
func (ix *Index) Offset(line, col int) (offset int, ok bool) {
	return ix.scan(line, col, false)
}

// OffsetUTF16 is like Offset but takes a 1-based UTF-16 code-unit column.
func (ix *Index) OffsetUTF16(line, col16 int) (offset int, ok bool) {
	return ix.scan(line, col16, true)
}

func (ix *Index) scan(line, col int, utf16 bool) (int, bool) {
	start, end, ok := ix.LineBounds(line)
	if !ok {
		return 0, false
	}
	if col < 1 {
		col = 1
	}
	c := 1
	for p := start; p < end; {
		if c == col {
			return p, true
		}
		r, size := utf8.DecodeRune(ix.buf[p:end])
		if utf16 {
			c += utf16Len(r)
		} else {
			c++
		}
		p += size
	}
	if c == col {
		return end, true
	}
	return end, false
}
