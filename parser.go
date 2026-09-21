package framechain

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

// Parser incrementally decodes frames from arbitrarily chunked byte input.
// It never blocks: Feed returns every frame completed so far, and reports
// corruption as ErrBadData. A short buffer is not an error — it just means
// the caller should feed more bytes.
type Parser struct {
	buf []byte
	// off tracks consumed bytes so buf can be compacted lazily.
	off int
}

// NewParser returns a ready-to-use Parser.
func NewParser() *Parser { return &Parser{} }

// Feed appends chunk to the internal buffer and returns all frames that
// became complete. It returns an error wrapping ErrBadData on corruption;
// after such an error the Parser must not be reused.
func (p *Parser) Feed(chunk []byte) ([]*Frame, error) {
	p.buf = append(p.buf, chunk...)
	var out []*Frame
	for {
		f, ok, err := p.tryOne()
		if err != nil {
			return out, err
		}
		if !ok {
			break
		}
		out = append(out, f)
	}
	// Compact: drop consumed prefix.
	if p.off > 0 {
		p.buf = append([]byte(nil), p.buf[p.off:]...)
		p.off = 0
	}
	return out, nil
}

// tryOne attempts to decode a single frame from the buffered bytes.
// ok=false means "need more data", never an error by itself.
func (p *Parser) tryOne() (f *Frame, ok bool, err error) {
	b := p.buf[p.off:]
	if len(b) < headerSize {
		return nil, false, nil //缺数据：连头部都不全
	}
	if !bytes.Equal(b[:4], []byte(Magic)) {
		return nil, false, fmt.Errorf("%w: bad magic %q", ErrBadData, b[:4])
	}
	paylen := binary.BigEndian.Uint32(b[4+8+DigestSize : headerSize])
	// 分配前检查长度额度：超限直接判坏数据，不做任何分配。
	if paylen > MaxPayload {
		return nil, false, fmt.Errorf("%w: payload length %d exceeds quota %d", ErrBadData, paylen, MaxPayload)
	}
	total := frameSize(paylen)
	if len(b) < total {
		return nil, false, nil // 缺数据：帧体未收齐
	}
	f, err = decode(b[:total], paylen)
	if err != nil {
		return nil, false, err
	}
	p.off += total
	return f, true, nil
}

// Finish reports whether the stream ended cleanly. pending>0 means the
// tail of the input is a truncated frame: acceptable only as the very last
// frame of a stream (callers decide via Chain/Verify).
func (p *Parser) Finish() (pending int) { return len(p.buf) - p.off }
