package gifcompose

import (
	"image"
	"image/gif"
)

// CompositeAll composites every GIF frame in order and returns them as
// logical-screen-sized images. It is a convenience wrapper around Sequencer
// for callers that need the whole animation at once.
func CompositeAll(g *gif.GIF) ([]*image.NRGBA, error) {
	seq, err := NewSequencer(g)
	if err != nil {
		return nil, err
	}
	frames := make([]*image.NRGBA, 0, seq.Len())
	for {
		frame, err := seq.Next()
		if err != nil {
			break
		}
		frames = append(frames, frame)
	}
	return frames, nil
}
