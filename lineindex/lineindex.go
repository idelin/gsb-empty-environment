// Package lineindex maintains an incrementally updatable line index over a
// byte buffer and maps byte offsets to line numbers, Unicode scalar columns
// and UTF-16 columns, for use by error display UIs.
//
// Line terminators recognized: LF, CRLF and lone CR. A CRLF pair may be
// split across chunk boundaries; the index reconciles it on the next edit.
// Invalid UTF-8 is decoded one replacement char (U+FFFD) per bad byte, and
// the bad bytes remain marked via the Token API.
package lineindex

import (
	"sort"
	"unicode/utf8"
)

// Index is an incrementally updatable line index over a byte buffer.
// The zero value is ready to use.
type Index struct {
	buf    []byte
	starts []int // byte offset of each line start; starts[0] == 0
}

// New returns an empty Index.
func New() *Index {
	return &Index{starts: []int{0}}
}

// Len returns the buffer length in bytes.
func (x *Index) Len() int { return len(x.buf) }

// LineCount returns the number of lines. A trailing terminator produces a
// final empty line.
func (x *Index) LineCount() int { return len(x.starts) }

// Bytes returns the underlying buffer. Callers must not mutate it.
func (x *Index) Bytes() []byte { return x.buf }

// Append adds a chunk at the end of the buffer, reconciling a CR that may
// have been left pending at the previous chunk boundary.
func (x *Index) Append(chunk []byte) {
	x.Insert(len(x.buf), chunk)
}

// Insert splices data into the buffer at byte offset off and repairs the
// index from the affected line onward.
func (x *Index) Insert(off int, data []byte) {
	if off < 0 {
		off = 0
	}
	if off > len(x.buf) {
		off = len(x.buf)
	}
	if len(data) == 0 {
		return
	}
	tail := make([]byte, len(x.buf)-off)
	copy(tail, x.buf[off:])
	x.buf = append(x.buf[:off], data...)
	x.buf = append(x.buf, tail...)
	x.rescanFrom(off)
}

// Delete removes the byte range [off, off+n) and repairs the index.
func (x *Index) Delete(off, n int) {
	if off < 0 {
		n += off
		off = 0
	}
	if off > len(x.buf) {
		return
	}
	if off+n > len(x.buf) {
		n = len(x.buf) - off
	}
	if n <= 0 {
		return
	}
	x.buf = append(x.buf[:off], x.buf[off+n:]...)
	x.rescanFrom(off)
}

// lineOf returns the index of the line containing byte offset pos.
func (x *Index) lineOf(pos int) int {
	return sort.Search(len(x.starts), func(i int) bool { return x.starts[i] > pos }) - 1
}

// rescanFrom rebuilds line starts from the line containing pos to EOF.
// If the byte before the rescan point is CR, the rescan backs up one line
// so a CRLF pair split by the edit is merged correctly.
func (x *Index) rescanFrom(pos int) {
	li := x.lineOf(pos)
	start := x.starts[li]
	if start > 0 && x.buf[start-1] == '\r' {
		li--
		start = x.starts[li]
	}
	x.starts = x.starts[:li+1]
	for i := start; i < len(x.buf); {
		switch x.buf[i] {
		case '\n':
			x.starts = append(x.starts, i+1)
			i++
		case '\r':
			if i+1 < len(x.buf) && x.buf[i+1] == '\n' {
				x.starts = append(x.starts, i+2)
				i += 2
			} else {
				x.starts = append(x.starts, i+1)
				i++
			}
		default:
			i++
		}
	}
}

// Location is the position of a byte offset in line/column terms.
// Columns are zero-based counts from the start of the line.
type Location struct {
	Offset    int // clamped byte offset
	Line      int // zero-based line number
	ScalarCol int // Unicode scalar values (code points) before Offset on the line
	UTF16Col  int // UTF-16 code units before Offset on the line
}

// Locate maps a byte offset to its line and columns. Offsets outside the
// buffer are clamped. Each invalid UTF-8 byte counts as one scalar and one
// UTF-16 unit; astral-plane runes count as two UTF-16 units.
func (x *Index) Locate(offset int) Location {
	offset = clamp(offset, 0, len(x.buf))
	line := x.lineOf(offset)
	scalar, u16 := 0, 0
	for i := x.starts[line]; i < offset; {
		r, size := utf8.DecodeRune(x.buf[i:offset])
		scalar++
		if r >= 0x10000 {
			u16 += 2
		} else {
			u16++
		}
		i += size
	}
	return Location{Offset: offset, Line: line, ScalarCol: scalar, UTF16Col: u16}
}

// LineBounds returns the byte range [start, end) of a line's content,
// excluding its terminator. line is clamped to the valid range.
func (x *Index) LineBounds(line int) (start, end int) {
	line = clamp(line, 0, len(x.starts)-1)
	start = x.starts[line]
	end = len(x.buf)
	if line+1 < len(x.starts) {
		end = x.starts[line+1]
		if end > start && x.buf[end-1] == '\n' {
			end--
			if end > start && x.buf[end-1] == '\r' {
				end--
			}
		} else if end > start && x.buf[end-1] == '\r' {
			end--
		}
	}
	return start, end
}

// Offset maps a (line, scalar column) pair back to a byte offset.
// Columns past the end of the line clamp to the line's content end.
func (x *Index) Offset(line, scalarCol int) int {
	return x.offsetBy(line, scalarCol, false)
}

// OffsetUTF16 maps a (line, UTF-16 column) pair back to a byte offset.
func (x *Index) OffsetUTF16(line, utf16Col int) int {
	return x.offsetBy(line, utf16Col, true)
}

func (x *Index) offsetBy(line, col int, utf16Units bool) int {
	if col < 0 {
		col = 0
	}
	start, end := x.LineBounds(line)
	n := 0
	for i := start; i < end; {
		r, size := utf8.DecodeRune(x.buf[i:end])
		w := 1
		if utf16Units && r >= 0x10000 {
			w = 2
		}
		if n+w > col {
			return i
		}
		n += w
		if n == col {
			return i + size
		}
		i += size
	}
	return end
}

// Token is one decoded unit of a line: either a rune or a replacement
// char standing in for a single invalid UTF-8 byte.
type Token struct {
	Offset  int    // byte offset of the token within the buffer
	Width   int    // byte width; 1 for an invalid byte
	Text    string // decoded text; U+FFFD for an invalid byte
	Invalid bool   // true when Text replaces a bad byte
}

// LineTokens decodes a line's content into tokens, marking invalid UTF-8
// bytes individually.
func (x *Index) LineTokens(line int) []Token {
	start, end := x.LineBounds(line)
	var toks []Token
	for i := start; i < end; {
		r, size := utf8.DecodeRune(x.buf[i:end])
		invalid := r == utf8.RuneError && size == 1
		toks = append(toks, Token{
			Offset:  i,
			Width:   size,
			Text:    string(r),
			Invalid: invalid,
		})
		i += size
	}
	return toks
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
