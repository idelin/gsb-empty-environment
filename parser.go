package wire

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// ErrNeedMore is returned by Parser.Next when the buffered bytes do not
// yet contain a complete field. Feed more data and call Next again.
var ErrNeedMore = errors.New("wire: need more data")

var errIncomplete = errors.New("wire: incomplete varint")

// EventKind identifies what a Parser.Next call produced.
type EventKind int

const (
	EventUint64       EventKind = iota // wire type 0, value in U64
	EventFixed64                       // wire type 1, value in F64
	EventFixed32                       // wire type 5, value in F32
	EventBytes                         // wire type 2, raw payload in Raw
	EventPacked                        // wire type 2, decoded elements in Packed
	EventMessageStart                  // nested message opened (schema-declared)
	EventMessageEnd                    // nested message closed
)

// Event is one parsed item. Path holds the field numbers of the
// enclosing messages (empty at the top level); Field is the field number
// of this event. The full path of the field is Path + Field.
type Event struct {
	Kind  EventKind
	Path  []int
	Field int

	U64 uint64
	F64 uint64
	F32 uint32
	Raw []byte

	Packed     []uint64
	PackedWire int
}

// Stats reports resource usage of a Parser.
type Stats struct {
	BytesFed        int64 // total bytes passed to Feed
	BytesConsumed   int64 // bytes fully parsed (absolute offset of the read head)
	Fields          int   // fields parsed so far (nested opens included)
	Messages        int   // nested messages opened
	PeakBufferBytes int   // high-water mark of the internal buffer
}

type frame struct {
	remaining int64 // bytes left in this (sub)message; -1 = unbounded root
	field     int   // field number that opened this message (0 = root)
	path      []int // field path of this message, including field
}

// Parser is an incremental wire parser. Bytes are supplied in arbitrary
// chunks with Feed; Next extracts one event at a time and returns
// ErrNeedMore when the current chunk is exhausted mid-field, so parsing
// can be paused and resumed at will.
type Parser struct {
	buf    []byte
	base   int64 // absolute offset of buf[0]
	limits Limits
	schema Schema
	frames []frame
	fields int
	fed    int64
	eof    bool
	stats  Stats
}

// NewParser creates a Parser. limits and schema are captured by value /
// reference respectively and must not be mutated while parsing.
func NewParser(limits Limits, schema Schema) *Parser {
	return &Parser{
		limits: limits,
		schema: schema,
		frames: []frame{{remaining: -1}},
	}
}

// Feed appends a chunk of input. It fails if the caller-supplied total
// byte limit would be exceeded.
func (p *Parser) Feed(b []byte) error {
	if p.eof {
		return errors.New("wire: Feed after Finish")
	}
	p.fed += int64(len(b))
	if p.limits.MaxTotalBytes > 0 && p.fed > p.limits.MaxTotalBytes {
		return &Error{
			Code:   ErrLimit,
			Offset: p.base,
			Path:   p.currentPath(),
			Detail: fmt.Sprintf("total input %d bytes exceeds limit %d", p.fed, p.limits.MaxTotalBytes),
		}
	}
	p.buf = append(p.buf, b...)
	if len(p.buf) > p.stats.PeakBufferBytes {
		p.stats.PeakBufferBytes = len(p.buf)
	}
	p.stats.BytesFed = p.fed
	return nil
}

// Finish signals end of input. Subsequent Next calls report truncation
// for incomplete fields and io.EOF once the message is fully drained.
func (p *Parser) Finish() { p.eof = true }

// Stats returns a snapshot of parser resource usage.
func (p *Parser) Stats() Stats {
	p.stats.BytesConsumed = p.base
	p.stats.Fields = p.fields
	return p.stats
}

func (p *Parser) currentPath() []int {
	return append([]int(nil), p.frames[len(p.frames)-1].path...)
}

func (p *Parser) errf(code ErrCode, off int64, path []int, format string, args ...interface{}) *Error {
	return &Error{
		Code:   code,
		Offset: off,
		Path:   append([]int(nil), path...),
		Detail: fmt.Sprintf(format, args...),
	}
}

// Next returns the next event, ErrNeedMore if the buffer is exhausted
// mid-field, io.EOF once the finished input is fully consumed, or a
// *Error describing the failure.
func (p *Parser) Next() (Event, error) {
	// Close any nested messages whose declared length is exhausted.
	for len(p.frames) > 1 && p.frames[len(p.frames)-1].remaining == 0 {
		fr := p.frames[len(p.frames)-1]
		p.frames = p.frames[:len(p.frames)-1]
		return Event{
			Kind:  EventMessageEnd,
			Field: fr.field,
			Path:  append([]int(nil), fr.path[:len(fr.path)-1]...),
		}, nil
	}
	if len(p.buf) == 0 {
		if p.eof {
			if len(p.frames) > 1 {
				fr := p.frames[len(p.frames)-1]
				return Event{}, p.errf(ErrTruncated, p.base, fr.path,
					"message ended with %d bytes missing", fr.remaining)
			}
			return Event{}, io.EOF
		}
		return Event{}, ErrNeedMore
	}
	return p.parseField()
}

// incomplete decides between ErrNeedMore and a truncation error.
func (p *Parser) incomplete(fr *frame, what string) error {
	bounded := fr.remaining >= 0 && fr.remaining <= int64(len(p.buf))
	if bounded || p.eof {
		return p.errf(ErrTruncated, p.base, fr.path, "truncated %s", what)
	}
	return ErrNeedMore
}

// consume advances the read head by n bytes.
func (p *Parser) consume(n int) {
	p.buf = p.buf[n:]
	p.base += int64(n)
	fr := &p.frames[len(p.frames)-1]
	if fr.remaining > 0 {
		fr.remaining -= int64(n)
	}
}

// countField enforces the field-count limit.
func (p *Parser) countField(off int64, path []int) error {
	p.fields++
	if p.limits.MaxFields > 0 && p.fields > p.limits.MaxFields {
		return p.errf(ErrLimit, off, path, "field count exceeds limit %d", p.limits.MaxFields)
	}
	return nil
}

func (p *Parser) parseField() (Event, error) {
	fr := &p.frames[len(p.frames)-1]
	avail := int64(len(p.buf))
	if fr.remaining >= 0 && fr.remaining < avail {
		avail = fr.remaining
	}
	keyOff := p.base

	key, n, err := parseVarint(p.buf[:avail])
	if err == errIncomplete {
		return Event{}, p.incomplete(fr, "key")
	}
	if err != nil {
		return Event{}, p.errf(ErrOverflow, keyOff, fr.path, "varint overflow in key")
	}

	fieldNum := int(key >> 3)
	wt := int(key & 7)
	if fieldNum < 1 || fieldNum > MaxFieldNumber {
		return Event{}, p.errf(ErrFieldNumber, keyOff, fr.path,
			"invalid field number %d (must be 1..%d)", fieldNum, MaxFieldNumber)
	}
	parentPath := p.currentPath()
	fullPath := append(append([]int(nil), fr.path...), fieldNum)
	switch wt {
	case WireVarint, WireFixed64, WireBytes, WireFixed32:
	default:
		return Event{}, p.errf(ErrWireType, keyOff, fullPath, "unsupported wire type %d", wt)
	}

	rest := p.buf[n:]
	restAvail := avail - int64(n)

	switch wt {
	case WireVarint:
		v, m, err := parseVarint(rest[:restAvail])
		if err == errIncomplete {
			return Event{}, p.incomplete(fr, "varint value")
		}
		if err != nil {
			return Event{}, p.errf(ErrOverflow, keyOff+int64(n), fullPath, "varint overflow in value")
		}
		if err := p.countField(keyOff, fullPath); err != nil {
			return Event{}, err
		}
		p.consume(n + m)
		return Event{Kind: EventUint64, Path: parentPath, Field: fieldNum, U64: v}, nil

	case WireFixed64:
		if restAvail < 8 {
			return Event{}, p.incomplete(fr, "fixed64 value")
		}
		v := binary.LittleEndian.Uint64(rest)
		if err := p.countField(keyOff, fullPath); err != nil {
			return Event{}, err
		}
		p.consume(n + 8)
		return Event{Kind: EventFixed64, Path: parentPath, Field: fieldNum, F64: v}, nil

	case WireFixed32:
		if restAvail < 4 {
			return Event{}, p.incomplete(fr, "fixed32 value")
		}
		v := binary.LittleEndian.Uint32(rest)
		if err := p.countField(keyOff, fullPath); err != nil {
			return Event{}, err
		}
		p.consume(n + 4)
		return Event{Kind: EventFixed32, Path: parentPath, Field: fieldNum, F32: v}, nil
	}

	// WireBytes: key, then length varint, then payload.
	lengthOff := keyOff + int64(n)
	l, m, err := parseVarint(rest[:restAvail])
	if err == errIncomplete {
		return Event{}, p.incomplete(fr, "length")
	}
	if err != nil {
		return Event{}, p.errf(ErrOverflow, lengthOff, fullPath, "varint overflow in length")
	}
	length := int64(l)

	// All size checks happen before any allocation.
	if p.limits.MaxFieldLen > 0 && length > p.limits.MaxFieldLen {
		return Event{}, p.errf(ErrLimit, lengthOff, fullPath,
			"field length %d exceeds limit %d", length, p.limits.MaxFieldLen)
	}
	if fr.remaining >= 0 && length > fr.remaining-int64(n)-int64(m) {
		return Event{}, p.errf(ErrTruncated, lengthOff, fullPath,
			"declared length %d exceeds %d remaining bytes of enclosing message",
			length, fr.remaining-int64(n)-int64(m))
	}
	if int64(len(rest))-int64(m) < length {
		return Event{}, p.incomplete(fr, "payload")
	}
	payload := rest[m : int64(m)+length]

	lf, declared := p.schema[fieldNum]
	switch {
	case declared && lf.Kind == KindMessage:
		if p.limits.MaxDepth > 0 && len(p.frames) > p.limits.MaxDepth {
			return Event{}, p.errf(ErrLimit, lengthOff, fullPath,
				"nesting depth exceeds limit %d", p.limits.MaxDepth)
		}
		if err := p.countField(keyOff, fullPath); err != nil {
			return Event{}, err
		}
		p.consume(n + m)
		parent := &p.frames[len(p.frames)-1]
		if parent.remaining > 0 {
			parent.remaining -= length
		}
		p.frames = append(p.frames, frame{remaining: length, field: fieldNum, path: fullPath})
		p.stats.Messages++
		return Event{Kind: EventMessageStart, Path: parentPath, Field: fieldNum}, nil

	case declared && lf.Kind == KindPacked:
		vals, err := decodePacked(payload, lf.ElemWire)
		if err != nil {
			return Event{}, p.errf(ErrPacked, lengthOff+int64(m), fullPath, "%s", err)
		}
		if err := p.countField(keyOff, fullPath); err != nil {
			return Event{}, err
		}
		p.consume(n + m + int(length))
		return Event{
			Kind:       EventPacked,
			Path:       parentPath,
			Field:      fieldNum,
			Packed:     vals,
			PackedWire: lf.ElemWire,
		}, nil

	default:
		raw := make([]byte, length)
		copy(raw, payload)
		if err := p.countField(keyOff, fullPath); err != nil {
			return Event{}, err
		}
		p.consume(n + m + int(length))
		return Event{Kind: EventBytes, Path: parentPath, Field: fieldNum, Raw: raw}, nil
	}
}

// parseVarint decodes a base-128 varint of at most 10 bytes. It returns
// errIncomplete if b ends before the varint terminates, and an error if
// the varint overflows a uint64.
func parseVarint(b []byte) (uint64, int, error) {
	var v uint64
	for i := 0; i < len(b) && i < 10; i++ {
		c := b[i]
		if i == 9 && c > 1 {
			return 0, 0, errors.New("wire: varint overflows uint64")
		}
		v |= uint64(c&0x7f) << (7 * i)
		if c < 0x80 {
			return v, i + 1, nil
		}
	}
	return 0, 0, errIncomplete
}

// decodePacked decodes a packed payload of fixed-width or varint
// elements. The payload is already length-bounded by the caller.
func decodePacked(b []byte, elemWire int) ([]uint64, error) {
	switch elemWire {
	case WireFixed64:
		if len(b)%8 != 0 {
			return nil, fmt.Errorf("packed fixed64 length %d not divisible by 8", len(b))
		}
		vals := make([]uint64, len(b)/8)
		for i := range vals {
			vals[i] = binary.LittleEndian.Uint64(b[i*8:])
		}
		return vals, nil
	case WireFixed32:
		if len(b)%4 != 0 {
			return nil, fmt.Errorf("packed fixed32 length %d not divisible by 4", len(b))
		}
		vals := make([]uint64, len(b)/4)
		for i := range vals {
			vals[i] = uint64(binary.LittleEndian.Uint32(b[i*4:]))
		}
		return vals, nil
	case WireVarint:
		var vals []uint64
		for len(b) > 0 {
			v, n, err := parseVarint(b)
			if err != nil {
				return nil, fmt.Errorf("bad packed varint element: %v", err)
			}
			vals = append(vals, v)
			b = b[n:]
		}
		return vals, nil
	}
	return nil, fmt.Errorf("unsupported packed element wire type %d", elemWire)
}
