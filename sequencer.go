package gifcompose

import (
	"image"
	"image/gif"
	"io"
)

// Sequencer composites GIF frames in order, one frame at a time. It keeps
// only the render canvas and at most one DisposalPrevious snapshot, so its
// memory use stays constant regardless of the frame count.
type Sequencer struct {
	engine
	next int
}

// NewSequencer returns a Sequencer over g. The GIF must outlive the
// sequencer and must not be mutated while the sequencer is in use.
func NewSequencer(g *gif.GIF) (*Sequencer, error) {
	e, err := newEngine(g)
	if err != nil {
		return nil, err
	}
	return &Sequencer{engine: *e}, nil
}

// Len reports the number of frame blocks in the GIF.
func (s *Sequencer) Len() int {
	return len(s.g.Image)
}

// Next composites and returns the next frame. It returns io.EOF once every
// frame has been consumed. The returned image is an independent copy and is
// safe to keep after subsequent calls.
func (s *Sequencer) Next() (*image.NRGBA, error) {
	if s.next >= s.Len() {
		return nil, io.EOF
	}
	frame := s.next
	s.next++
	s.renderFrame(frame)
	rendered := s.snapshot()
	s.applyDisposal(frame)
	return rendered, nil
}

// Reset restarts the sequencer at frame 0 with a transparent canvas.
func (s *Sequencer) Reset() {
	s.engine.reset()
	s.next = 0
}
