package morph

import "errors"

// Op selects the morphological operation performed by a Stream.
type Op int

const (
	// Dilation takes the max over the structuring element.
	Dilation Op = iota
	// Erosion takes the min over the structuring element.
	Erosion
)

var (
	errClosed   = errors.New("morph: stream is closed")
	errBadWidth = errors.New("morph: row width does not match stream width")
	errNegWidth = errors.New("morph: width must be non-negative")
	errNilRow   = errors.New("morph: row is nil but width is positive")
)

// Stream computes a morphological operation over an image whose rows
// arrive in blocks. It keeps only the rows that can still contribute to
// future output: at most Element.RowSpan() rows plus rows not yet
// emitted, independent of the total image height.
//
// Pixels outside the image are fixed to zero. For Erosion this means any
// structuring-element offset that falls outside the image forces the
// output pixel to zero; those offsets are never skipped.
type Stream struct {
	op    Op
	el    Element
	width int

	nextIn  int // number of input rows received
	nextOut int // number of output rows emitted
	closed  bool
	rows    map[int][]uint8 // cached input rows, indexes in [nextOut+minDY, nextIn)
}

// NewStream creates a stream for images of the given width. The height
// may be unknown until Close.
func NewStream(op Op, el Element, width int) (*Stream, error) {
	if width < 0 {
		return nil, errNegWidth
	}
	return &Stream{
		op:    op,
		el:    el,
		width: width,
		rows:  make(map[int][]uint8),
	}, nil
}

// Push appends a block of input rows and returns every output row that
// becomes computable as a result. Rows are copied; the caller may reuse
// the buffers after Push returns.
func (s *Stream) Push(rows [][]uint8) ([][]uint8, error) {
	if s.closed {
		return nil, errClosed
	}
	for _, row := range rows {
		if len(row) != s.width {
			return nil, errBadWidth
		}
		if row == nil && s.width > 0 {
			return nil, errNilRow
		}
		cp := make([]uint8, len(row))
		copy(cp, row)
		s.rows[s.nextIn] = cp
		s.nextIn++
	}
	return s.emit(), nil
}

// Close marks the end of input: the image height is fixed at the number
// of rows pushed. It returns all remaining output rows. Rows below the
// last input row are out of the image and fixed to zero.
func (s *Stream) Close() ([][]uint8, error) {
	if s.closed {
		return nil, errClosed
	}
	s.closed = true
	out := s.emit()
	s.rows = nil
	return out, nil
}

// BufferedRows reports how many input rows the stream currently caches.
func (s *Stream) BufferedRows() int { return len(s.rows) }

// emit produces every output row whose contributing input rows are all
// known, then evicts cached rows no future output row can need.
func (s *Stream) emit() [][]uint8 {
	var out [][]uint8
	for s.emittable(s.nextOut) {
		out = append(out, s.computeRow(s.nextOut))
		s.nextOut++
	}
	// Rows below nextOut+minDY can never contribute again.
	for idx := range s.rows {
		if idx < s.nextOut+s.el.minDY {
			delete(s.rows, idx)
		}
	}
	return out
}

// emittable reports whether output row y is fully determined. Every
// needed input row y+dy must either have been received or be known to lie
// outside the image (above row 0 always; below the bottom only once the
// stream is closed and the height is fixed).
func (s *Stream) emittable(y int) bool {
	if y+s.el.maxDY < s.nextIn {
		return true
	}
	return s.closed && y < s.nextIn
}

func (s *Stream) computeRow(y int) []uint8 {
	out := make([]uint8, s.width)
	for x := 0; x < s.width; x++ {
		var best uint8
		if s.op == Erosion {
			best = 255
		}
		for _, p := range s.el.offsets {
			var v uint8 // out-of-image pixels are fixed to zero
			if ry, rx := y+p.Y, x+p.X; rx >= 0 && rx < s.width {
				if row, ok := s.rows[ry]; ok {
					v = row[rx]
				}
			}
			if s.op == Erosion {
				if v < best {
					best = v
				}
			} else if v > best {
				best = v
			}
		}
		out[x] = best
	}
	return out
}
