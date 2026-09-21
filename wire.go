package bridge

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// ErrNeedMoreData means the input is a valid prefix; more bytes may complete it.
var ErrNeedMoreData = errors.New("bridge: need more data")

// ErrBadData means the input is invalid regardless of how many more bytes arrive.
var ErrBadData = errors.New("bridge: bad data")

type Kind byte

const (
	KindVarint Kind = 0
	KindBytes  Kind = 1
)

const headerSize = 5 // 1 byte version + 4 byte big-endian payload length

// Limits caps resource usage. Every length is checked against these budgets
// before any allocation proportional to it is made.
type Limits struct {
	MaxFrameSize int
	MaxFieldLen  int
	MaxFields    int
}

func DefaultLimits() Limits {
	return Limits{MaxFrameSize: 1 << 20, MaxFieldLen: 1 << 16, MaxFields: 4096}
}

func appendVarint(dst []byte, v uint64) []byte {
	var tmp [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(tmp[:], v)
	return append(dst, tmp[:n]...)
}

// readVarint distinguishes a truncated varint (ErrNeedMoreData) from an
// overflowing one (ErrBadData).
func readVarint(buf []byte) (uint64, int, error) {
	v, n := binary.Uvarint(buf)
	switch {
	case n == 0:
		return 0, 0, ErrNeedMoreData
	case n < 0:
		return 0, 0, fmt.Errorf("%w: varint overflow", ErrBadData)
	}
	return v, n, nil
}

// Field is one decoded field. Unknown fields keep their raw value bytes so
// they can be re-encoded verbatim.
type Field struct {
	ID   uint32
	Kind Kind
	Num  uint64
	Raw  []byte
}

func (f Field) appendWire(dst []byte) []byte {
	dst = appendVarint(dst, uint64(f.ID)<<1|uint64(f.Kind))
	if f.Kind == KindVarint {
		return appendVarint(dst, f.Num)
	}
	dst = appendVarint(dst, uint64(len(f.Raw)))
	return append(dst, f.Raw...)
}

func parseField(buf []byte, lim Limits) (Field, int, error) {
	tag, n, err := readVarint(buf)
	if err != nil {
		return Field{}, 0, err
	}
	id := uint32(tag >> 1)
	if id == 0 {
		return Field{}, 0, fmt.Errorf("%w: field id 0", ErrBadData)
	}
	kind := Kind(tag & 1)
	rest := buf[n:]
	if kind == KindVarint {
		v, m, err := readVarint(rest)
		if err != nil {
			return Field{}, 0, err
		}
		return Field{ID: id, Kind: kind, Num: v}, n + m, nil
	}
	l, m, err := readVarint(rest)
	if err != nil {
		return Field{}, 0, err
	}
	// Budget check happens before any allocation or slicing based on l.
	if l > uint64(lim.MaxFieldLen) {
		return Field{}, 0, fmt.Errorf("%w: field %d length %d exceeds budget %d", ErrBadData, id, l, lim.MaxFieldLen)
	}
	if uint64(len(rest)-m) < l {
		return Field{}, 0, ErrNeedMoreData
	}
	return Field{ID: id, Kind: kind, Raw: rest[m : m+int(l)]}, n + m + int(l), nil
}

func parseFields(payload []byte, lim Limits) ([]Field, error) {
	var fields []Field
	for len(payload) > 0 {
		if len(fields) >= lim.MaxFields {
			return nil, fmt.Errorf("%w: more than %d fields", ErrBadData, lim.MaxFields)
		}
		f, n, err := parseField(payload, lim)
		if err != nil {
			return nil, err
		}
		if f.Kind == KindBytes {
			raw := make([]byte, len(f.Raw))
			copy(raw, f.Raw)
			f.Raw = raw
		}
		fields = append(fields, f)
		payload = payload[n:]
	}
	return fields, nil
}

// EncodeFrame serializes a versioned frame: version byte, 4-byte length, payload.
func EncodeFrame(version int, fields []Field) []byte {
	var payload []byte
	for _, f := range fields {
		payload = f.appendWire(payload)
	}
	out := make([]byte, 0, headerSize+len(payload))
	out = append(out, byte(version))
	var lb [4]byte
	binary.BigEndian.PutUint32(lb[:], uint32(len(payload)))
	out = append(out, lb[:]...)
	return append(out, payload...)
}

// Parser incrementally parses exactly one frame from arbitrarily chunked input.
type Parser struct {
	lim        Limits
	buf        []byte
	version    int
	length     int
	headerDone bool
}

func NewParser(lim Limits) *Parser { return &Parser{lim: lim} }

// Feed ingests one chunk. It returns ErrBadData for invalid input; a valid but
// incomplete prefix is not an error.
func (p *Parser) Feed(chunk []byte) error {
	if p.headerDone && len(p.buf)+len(chunk) > headerSize+p.length {
		return fmt.Errorf("%w: trailing bytes beyond frame", ErrBadData)
	}
	p.buf = append(p.buf, chunk...)
	if !p.headerDone && len(p.buf) >= headerSize {
		v := p.buf[0]
		if v != 1 && v != 2 {
			return fmt.Errorf("%w: unknown version %d", ErrBadData, v)
		}
		l := int(binary.BigEndian.Uint32(p.buf[1:headerSize]))
		// Budget check before the payload buffer is ever grown to l bytes.
		if l <= 0 || l > p.lim.MaxFrameSize {
			return fmt.Errorf("%w: frame length %d exceeds budget %d", ErrBadData, l, p.lim.MaxFrameSize)
		}
		p.version = int(v)
		p.length = l
		p.headerDone = true
	}
	return nil
}

// Next returns the complete frame, or ErrNeedMoreData if more chunks are needed.
func (p *Parser) Next() (version int, payload []byte, err error) {
	if !p.headerDone || len(p.buf) < headerSize+p.length {
		return 0, nil, ErrNeedMoreData
	}
	return p.version, p.buf[headerSize:], nil
}
