package endconv

import "errors"

// Status tells a Parser.Next caller whether a record was produced or
// more input bytes are required.
type Status int

const (
	// NeedMoreData means the buffered bytes do not yet contain one
	// complete record; no data is bad, more bytes must be written.
	NeedMoreData Status = iota
	// RecordReady means Next returned a complete, converted record.
	RecordReady
)

var (
	// ErrRecordQuota means buffered input exceeded MaxRecordBytes, i.e.
	// the peer is sending an oversized record rather than a slow one.
	ErrRecordQuota = errors.New("endconv: record exceeds maximum record bytes")
	// ErrPayloadQuota means a VarBlob header declared a payload longer
	// than MaxPayloadBytes. It is reported before any allocation sized
	// by the untrusted length field.
	ErrPayloadQuota = errors.New("endconv: variable payload exceeds quota")
)

// Limits caps how much a parser is willing to hold / allocate. Both
// bounds are checked before allocating anything based on untrusted
// length data.
type Limits struct {
	MaxRecordBytes  int // total bytes one record may occupy (header+payloads)
	MaxPayloadBytes int // bytes one VarBlob payload may occupy
}

// Parser accepts arbitrarily chunked byte input and emits converted
// records. Bytes for several records may arrive in one Write; one record
// may be spread over many Writes.
type Parser struct {
	schema Schema
	dir    Direction
	limits Limits
	buf    []byte
}

// NewParser constructs a parser. Non-positive limits default to 64 KiB.
func NewParser(s Schema, dir Direction, limits Limits) (*Parser, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	if limits.MaxRecordBytes <= 0 {
		limits.MaxRecordBytes = 64 << 10
	}
	if limits.MaxPayloadBytes <= 0 {
		limits.MaxPayloadBytes = limits.MaxRecordBytes
	}
	if limits.MaxPayloadBytes > limits.MaxRecordBytes {
		limits.MaxPayloadBytes = limits.MaxRecordBytes
	}
	return &Parser{schema: s, dir: dir, limits: limits}, nil
}

// Buffered returns the number of unconsumed bytes held by the parser.
func (p *Parser) Buffered() int { return len(p.buf) }

// Write appends a chunk of input bytes. It returns ErrRecordQuota when
// accepting the chunk would let a single pending record exceed the
// configured maximum; such input is bad data, not missing data, and no
// bytes are retained from the offending chunk.
func (p *Parser) Write(b []byte) error {
	if len(p.buf)+len(b) > p.limits.MaxRecordBytes {
		return ErrRecordQuota
	}
	p.buf = append(p.buf, b...)
	return nil
}

// Next parses and converts one record from buffered bytes.
//
// Semantics:
//   - RecordReady: the returned slice is an independent allocation and
//     its bytes have been consumed from the parser.
//   - NeedMoreData: a prefix of a record was seen but more bytes are
//     required; nil is returned.
//   - non-nil error: the buffered data is invalid (currently only
//     ErrPayloadQuota); no bytes are consumed so the caller can inspect
//     or report the offending record.
func (p *Parser) Next() ([]byte, Status, error) {
	n, status, err := p.peekLength()
	if status != RecordReady {
		return nil, status, err
	}
	// Length fully known and quota-checked; allocate only now.
	rec := make([]byte, n)
	if _, err := Convert(rec, p.buf[:n], p.schema, p.dir); err != nil {
		return nil, NeedMoreData, err
	}
	// Consume the record; drop the backing array when fully drained so
	// it can be garbage collected.
	if n == len(p.buf) {
		p.buf = nil
	} else {
		remaining := len(p.buf) - n
		copy(p.buf, p.buf[n:])
		p.buf = p.buf[:remaining]
	}
	return rec, RecordReady, nil
}

// peekLength walks the buffer like RecordLength but distinguishes
// "not enough bytes yet" from "declared length exceeds the quota".
func (p *Parser) peekLength() (int, Status, error) {
	bo := sourceOrder(p.dir)
	off := 0
	for _, f := range p.schema {
		size := fixedSize(f)
		if f.Kind == VarBlob {
			if len(p.buf)-off < 2 {
				return 0, NeedMoreData, nil
			}
			payloadLen := int(bo.Uint16(p.buf[off : off+2]))
			if payloadLen > p.limits.MaxPayloadBytes {
				return 0, NeedMoreData, ErrPayloadQuota
			}
			size = 2 + payloadLen
		}
		if len(p.buf)-off < size {
			return 0, NeedMoreData, nil
		}
		off += size
	}
	return off, RecordReady, nil
}
