package framepack

// Parser reassembles frames from an arbitrarily chunked byte stream. Input
// may be split at any byte boundary. Incomplete trailing data is kept
// internally and reported as ErrNeedMoreData by ParseFrame, never as
// corruption.
type Parser struct {
	maxFrame int
	maxMsg   int
	buf      []byte
}

// NewParser creates a parser enforcing the same quotas as the encoder:
// no frame may exceed maxFrame bytes (header included) and no message may
// exceed maxMsg bytes. Both are checked before any payload allocation.
func NewParser(maxFrame, maxMsg int) *Parser {
	return &Parser{maxFrame: maxFrame, maxMsg: maxMsg}
}

// ParseFrame decodes one frame from buf, returning the frame and the number
// of bytes consumed. It returns ErrNeedMoreData if buf holds only a prefix
// of a frame, and ErrBadData if buf is corrupt or violates a length quota.
func ParseFrame(buf []byte, maxFrame, maxMsg int) (Frame, int, error) {
	return parseFrame(buf, maxFrame, maxMsg)
}

// Feed appends data and returns every frame that becomes complete. A
// trailing partial frame is buffered for the next Feed. Corrupt input
// yields an error wrapping ErrBadData; frames decoded before the corruption
// are still returned.
func (p *Parser) Feed(data []byte) ([]Frame, error) {
	p.buf = append(p.buf, data...)
	var frames []Frame
	for len(p.buf) > 0 {
		f, n, err := parseFrame(p.buf, p.maxFrame, p.maxMsg)
		if err != nil {
			if err == ErrNeedMoreData {
				break
			}
			return frames, err
		}
		frames = append(frames, f)
		p.buf = p.buf[n:]
	}
	if len(p.buf) == 0 {
		p.buf = nil
	}
	return frames, nil
}

// Buffered reports how many unparsed bytes are currently held.
func (p *Parser) Buffered() int { return len(p.buf) }
