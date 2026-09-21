package frameq

import "errors"

// Parser reassembles frames from arbitrarily chunked byte input.
// Feed bytes as they arrive; call Next to drain complete frames.
type Parser struct {
	buf      []byte
	maxFrame int
}

// NewParser returns a Parser that rejects any frame whose declared
// length exceeds maxFrame bytes.
func NewParser(maxFrame int) *Parser {
	return &Parser{maxFrame: maxFrame}
}

// Feed appends a chunk of input. Chunks may split a frame anywhere.
func (p *Parser) Feed(b []byte) {
	p.buf = append(p.buf, b...)
}

// Next returns the next complete frame. If the buffered data is a prefix
// of a valid frame it returns ErrIncomplete; if the buffered data is
// corrupt it returns a non-nil corruption error and the buffer is left
// untouched so the caller can decide how to resynchronize.
func (p *Parser) Next() (*Frame, error) {
	f, n, err := ParseFrame(p.buf, p.maxFrame)
	if err != nil {
		return nil, err
	}
	p.buf = p.buf[n:]
	return f, nil
}

// Buffered returns the number of unconsumed bytes currently held.
func (p *Parser) Buffered() int { return len(p.buf) }

// IsIncomplete reports whether err means "need more data" as opposed to
// corrupt data.
func IsIncomplete(err error) bool { return errors.Is(err, ErrIncomplete) }
