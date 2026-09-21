package tlv

import "fmt"

// Parser incrementally decodes a TLV stream from arbitrarily chunked
// input. Complete top-level fields become available via Fields as soon
// as their bytes arrive; partial trailing data is buffered, not an
// error, until Finalize reports it as ErrNeedMoreData.
type Parser struct {
	// MaxValueLen caps any single value length (checked before
	// allocation, as soon as a header is complete). Zero uses
	// DefaultMaxValueLen.
	MaxValueLen uint32

	buf    []byte
	abs    int64 // absolute stream offset of buf[0]
	fields []*Field
	err    error // sticky hard error
}

// NewParser returns a streaming parser with the default quota.
func NewParser() *Parser { return &Parser{} }

func (p *Parser) quota() uint32 {
	if p.MaxValueLen == 0 {
		return DefaultMaxValueLen
	}
	return p.MaxValueLen
}

// Feed consumes one chunk of stream bytes. It returns a hard error for
// malformed data; running out of bytes mid-field is not an error.
func (p *Parser) Feed(chunk []byte) error {
	if p.err != nil {
		return p.err
	}
	p.buf = append(p.buf, chunk...)
	for len(p.buf) > 0 {
		f, n, err := parseField(p.buf, p.abs, p.quota())
		if err != nil {
			if de, ok := err.(*Error); ok && de.Kind == KindNeedMoreData {
				return nil // wait for more bytes
			}
			p.err = err
			return err
		}
		p.fields = append(p.fields, f)
		p.buf = p.buf[n:]
		p.abs += int64(n)
	}
	return nil
}

// Fields returns the top-level fields completed so far.
func (p *Parser) Fields() []*Field { return p.fields }

// Finalize ends the stream. Buffered partial data means the input was
// truncated: an error wrapping ErrNeedMoreData is returned.
func (p *Parser) Finalize() ([]*Field, error) {
	if p.err != nil {
		return nil, p.err
	}
	if len(p.buf) > 0 {
		return nil, &Error{Kind: KindNeedMoreData, Offset: p.abs,
			Detail: fmt.Sprintf("stream ended with %d unconsumed bytes", len(p.buf))}
	}
	return p.fields, nil
}
