package morph

import "errors"

// Op selects the morphological operation.
type Op int

const (
	// Dilate takes the maximum over the structuring-element neighborhood.
	Dilate Op = iota
	// Erode takes the minimum over the structuring-element neighborhood.
	Erode
)

// Stream consumes an image in row blocks (top to bottom) and produces
// output rows as soon as they are final. Out-of-image pixels are fixed
// to zero for both dilation and erosion.
//
// Memory use is bounded: at most Element.rowSpan() rows of the input are
// retained, regardless of image height.
type Stream struct {
	w, h int
	el   Element
	op   Op

	// Ring buffer of the last rowSpan() input rows.
	// Slot r % span holds input row r.
	ring   [][]uint8
	rowsIn int // number of input rows consumed so far
	nextY  int // next output row to emit
	done   bool
}

// NewStream creates a streaming processor for a w-by-h image.
func NewStream(w, h int, el Element, op Op) (*Stream, error) {
	if w <= 0 || h <= 0 {
		return nil, errors.New("morph: image dimensions must be positive")
	}
	if len(el.pts) == 0 {
		return nil, ErrEmptyElement
	}
	if op != Dilate && op != Erode {
		return nil, errors.New("morph: unknown operation")
	}
	span := el.rowSpan()
	ring := make([][]uint8, span)
	for i := range ring {
		ring[i] = make([]uint8, w)
	}
	return &Stream{w: w, h: h, el: el, op: op, ring: ring}, nil
}

// BufferRows reports the maximum number of input rows retained.
func (s *Stream) BufferRows() int { return len(s.ring) }

// WriteRows consumes the next block of input rows. len(p) must be a
// multiple of the image width, and the total number of rows written must
// not exceed the image height. It returns the output rows finalized by
// this block (possibly none, possibly several), each of length w, in
// top-to-bottom order.
func (s *Stream) WriteRows(p []byte) ([]byte, error) {
	if s.done {
		return nil, errors.New("morph: write after image complete")
	}
	if len(p)%s.w != 0 {
		return nil, errors.New("morph: block length not a multiple of width")
	}
	n := len(p) / s.w
	if s.rowsIn+n > s.h {
		return nil, errors.New("morph: too many input rows")
	}
	var out []byte
	for i := 0; i < n; i++ {
		copy(s.ring[s.rowsIn%len(s.ring)], p[i*s.w:(i+1)*s.w])
		s.rowsIn++
		out = s.emitReady(out)
	}
	return out, nil
}

// Close flushes any remaining output rows. It must be called after the
// last WriteRows; the returned rows complete the h-row output image.
// Trailing rows are finalized using zero for out-of-image input rows.
func (s *Stream) Close() ([]byte, error) {
	if s.rowsIn != s.h {
		return nil, errors.New("morph: image incomplete")
	}
	s.done = true
	return s.emitReady(nil), nil
}

// emitReady appends every output row that is final given rowsIn.
// Output row y is final once all input rows it can touch (y+minDY ..
// y+maxDY, clipped to the image) have arrived, i.e. when
// y+maxDY < rowsIn or all h rows are in.
func (s *Stream) emitReady(out []byte) []byte {
	for s.nextY < s.h && (s.nextY+s.el.maxDY < s.rowsIn || s.rowsIn == s.h) {
		out = s.computeRow(s.nextY, out)
		s.nextY++
	}
	return out
}

// computeRow appends output row y to out.
func (s *Stream) computeRow(y int, out []byte) []byte {
	span := len(s.ring)
	row := make([]uint8, s.w)
	for x := 0; x < s.w; x++ {
		var acc uint8
		if s.op == Erode {
			acc = 0xff
		}
		for _, p := range s.el.pts {
			yy, xx := y+p.Y, x+p.X
			var v uint8 // out-of-image pixels are fixed zero
			if yy >= 0 && yy < s.h && xx >= 0 && xx < s.w {
				v = s.ring[yy%span][xx]
			}
			if s.op == Dilate {
				if v > acc {
					acc = v
				}
			} else if v < acc {
				acc = v
			}
		}
		row[x] = acc
	}
	return append(out, row...)
}

// Apply runs op over the whole w-by-h image in one shot.
func Apply(img []byte, w, h int, el Element, op Op) ([]byte, error) {
	s, err := NewStream(w, h, el, op)
	if err != nil {
		return nil, err
	}
	if len(img) != w*h {
		return nil, errors.New("morph: image length mismatch")
	}
	out, err := s.WriteRows(img)
	if err != nil {
		return nil, err
	}
	rest, err := s.Close()
	if err != nil {
		return nil, err
	}
	return append(out, rest...), nil
}

// DilateImage is one-shot dilation over the whole image.
func DilateImage(img []byte, w, h int, el Element) ([]byte, error) {
	return Apply(img, w, h, el, Dilate)
}

// ErodeImage is one-shot erosion over the whole image.
func ErodeImage(img []byte, w, h int, el Element) ([]byte, error) {
	return Apply(img, w, h, el, Erode)
}
