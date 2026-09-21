package recordio

import (
	"encoding/binary"
	"fmt"
)

// Parser incrementally decodes a record stream from arbitrarily sized chunks.
// Call Feed whenever bytes arrive and Next to extract completed records.
//
// Next returns ErrNeedMoreData while the prefix is merely incomplete and
// ErrBadData once the framing is provably invalid. After any bad-data error
// the parser is permanently failed; feeding more bytes can never make later
// bytes parse successfully (no guessing/resynchronization), so a corrupt
// length cannot cause subsequent data to be read misaligned.
type Parser struct {
	maxRecordSize int

	buf     []byte
	offset  int64 // absolute stream offset of buf's first byte
	started bool  // file magic consumed
	err     error // sticky terminal error
}

// NewParser builds a parser with the given per-record body quota. Non-positive
// values select DefaultMaxRecordSize.
func NewParser(maxRecordSize int) *Parser {
	if maxRecordSize <= 0 {
		maxRecordSize = DefaultMaxRecordSize
	}
	return &Parser{maxRecordSize: maxRecordSize}
}

// Feed appends one arbitrary-sized chunk (a single byte up to the whole file).
func (p *Parser) Feed(chunk []byte) {
	if p.err == nil && len(chunk) > 0 {
		p.buf = append(p.buf, chunk...)
	}
}

// Err returns the parser's terminal bad-data error, if any.
func (p *Parser) Err() error { return p.err }

// Next returns the next record and its absolute offset in the stream. A
// return of ErrNeedMoreData leaves all pending bytes untouched.
func (p *Parser) Next() (rec *Record, offset int64, err error) {
	if p.err != nil {
		return nil, 0, p.err
	}
	if !p.started {
		if len(p.buf) < HeaderSize {
			return nil, 0, ErrNeedMoreData
		}
		if string(p.buf[:HeaderSize]) != Magic {
			p.err = fmt.Errorf("%w: not a record stream (magic %q, want %q)",
				ErrBadData, string(p.buf[:HeaderSize]), Magic)
			return nil, 0, p.err
		}
		p.consume(HeaderSize)
		p.started = true
	}

	if len(p.buf) < lenSize {
		return nil, 0, ErrNeedMoreData
	}
	bodyLen := int(binary.LittleEndian.Uint32(p.buf[:lenSize]))
	if bodyLen < recMetaSize {
		p.err = fmt.Errorf("%w: record at offset %d declares body length %d, minimum is %d",
			ErrBadData, p.offset, bodyLen, recMetaSize)
		return nil, 0, p.err
	}
	// Quota is validated against the declared length itself, before any
	// allocation keyed on that length happens.
	if bodyLen > p.maxRecordSize {
		p.err = fmt.Errorf("%w: record at offset %d declares body length %d, quota is %d",
			ErrBadData, p.offset, bodyLen, p.maxRecordSize)
		return nil, 0, p.err
	}

	total := lenSize + bodyLen
	if len(p.buf) < total {
		return nil, 0, ErrNeedMoreData
	}
	rec, err = parseBody(p.buf[lenSize:total])
	if err != nil {
		p.err = fmt.Errorf("record at offset %d: %w", p.offset, err)
		return nil, 0, p.err
	}
	offset = p.offset
	p.consume(total)
	return rec, offset, nil
}

// Done reports that the stream ended on an exact frame boundary with no
// partial frame or stray byte pending. A header-only stream (zero records)
// is Done; a truncated one is not.
func (p *Parser) Done() bool {
	return p.err == nil && p.started && len(p.buf) == 0
}

func (p *Parser) consume(n int) {
	remaining := copy(p.buf, p.buf[n:])
	p.buf = p.buf[:remaining]
	p.offset += int64(n)
}

// Drain repeatedly calls Next and invokes visit for every complete record.
// It returns ErrNeedMoreData if more bytes are required; any other error is
// bad data. visit receives each record and its stream offset.
func (p *Parser) Drain(visit func(rec *Record, offset int64) error) error {
	for {
		rec, offset, err := p.Next()
		if err == ErrNeedMoreData {
			return err
		}
		if err != nil {
			return err
		}
		if err := visit(rec, offset); err != nil {
			return err
		}
	}
}
