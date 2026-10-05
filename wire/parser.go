package wire

import (
	"encoding/binary"
	"fmt"
	"io"
)

// Limits bound the resources a parse may consume. The zero value of
// each field means "unlimited"; callers are expected to set them.
// All checks happen before the corresponding allocation or recursion.
type Limits struct {
	MaxTotalBytes int64 // total bytes fed for the top-level message
	MaxFieldLen   int64 // payload bytes of a single length-delimited field
	MaxFields     int   // fields per message (each nesting level separately)
	MaxDepth      int   // nesting depth; the root message is depth 1
}

// Stats records resource consumption and peaks observed during parsing.
type Stats struct {
	BytesFed       int64 // total bytes passed to Feed
	FieldsParsed   int64 // fields parsed, including nested messages
	MessagesParsed int64 // nested messages parsed
	BytesAllocated int64 // bytes copied out of the input buffer
	PeakBuffered   int   // peak bytes held in the parser buffer
}

// Parser incrementally parses one top-level message. Input may arrive in
// arbitrarily sized chunks via Feed; Next returns one field at a time and
// reports ErrNeedMore when the current chunk is exhausted mid-field, so
// parsing can be paused and resumed freely.
type Parser struct {
	lim    Limits
	schema *Schema

	buf    []byte // unconsumed input; buf[0] is always a field boundary
	off    int64  // absolute offset of buf[0]
	fed    int64  // total bytes fed
	fields int    // fields returned so far at this level
	closed bool   // Finish was called

	stats Stats
}

// NewParser returns a parser for one top-level message, interpreting
// length-delimited fields according to schema (nil = keep everything raw).
func NewParser(lim Limits, schema *Schema) *Parser {
	return &Parser{lim: lim, schema: schema}
}

// Stats returns a snapshot of the parser's resource statistics.
func (p *Parser) Stats() Stats { return p.stats }

// Offset returns the absolute offset of the next unparsed byte.
func (p *Parser) Offset() int64 { return p.off }

// Feed appends a chunk of input. It fails if the total byte limit would
// be exceeded, before anything is buffered.
func (p *Parser) Feed(b []byte) error {
	if p.closed {
		return &Error{Op: "feed", Offset: p.fed, Msg: "input fed after Finish"}
	}
	if p.lim.MaxTotalBytes > 0 && p.fed+int64(len(b)) > p.lim.MaxTotalBytes {
		return &Error{Op: "limit", Offset: p.fed,
			Msg: fmt.Sprintf("total message bytes exceed limit %d", p.lim.MaxTotalBytes)}
	}
	p.buf = append(p.buf, b...)
	p.fed += int64(len(b))
	p.stats.BytesFed += int64(len(b))
	if len(p.buf) > p.stats.PeakBuffered {
		p.stats.PeakBuffered = len(p.buf)
	}
	return nil
}

// Next parses and returns the next field. It returns ErrNeedMore if the
// buffered input ends mid-field (call Feed, then Next again), io.EOF if
// the stream was finished and fully consumed, or a *Error describing the
// first malformed byte.
func (p *Parser) Next() (*Field, error) {
	if len(p.buf) == 0 {
		if p.closed {
			return nil, io.EOF
		}
		return nil, ErrNeedMore
	}
	if p.lim.MaxFields > 0 && p.fields >= p.lim.MaxFields {
		return nil, p.errAt("limit", p.off, nil,
			fmt.Sprintf("field count exceeds limit %d", p.lim.MaxFields))
	}
	f, n, err := p.scanField(p.buf, p.off, nil, 1, p.schema)
	if err == errIncomplete {
		if p.closed {
			return nil, p.errAt("truncated", p.off, nil, "message ends mid-field")
		}
		return nil, ErrNeedMore
	}
	if err != nil {
		return nil, err
	}
	copy(p.buf, p.buf[n:])
	p.buf = p.buf[:len(p.buf)-n]
	p.off += int64(n)
	p.fields++
	p.stats.FieldsParsed++
	return f, nil
}

// Finish marks the end of the input stream. Afterwards Next returns
// io.EOF once all buffered complete fields are consumed, or a truncation
// error if the stream ends mid-field.
func (p *Parser) Finish() error {
	p.closed = true
	return nil
}

// ParseMessage parses a complete in-memory message in one call.
func ParseMessage(b []byte, lim Limits, schema *Schema) (*Message, error) {
	p := NewParser(lim, schema)
	if err := p.Feed(b); err != nil {
		return nil, err
	}
	if err := p.Finish(); err != nil {
		return nil, err
	}
	m := &Message{}
	for {
		f, err := p.Next()
		if err == io.EOF {
			return m, nil
		}
		if err != nil {
			return nil, err
		}
		m.Fields = append(m.Fields, *f)
	}
}

// scanField parses one field from the front of b. base is the absolute
// offset of b[0], path the field-number path of the enclosing message,
// depth the nesting depth of the enclosing message (root = 1). It returns
// the field and the number of bytes consumed, or errIncomplete if b does
// not hold a whole field.
func (p *Parser) scanField(b []byte, base int64, path []uint32, depth int, schema *Schema) (*Field, int, error) {
	key, n, err := decodeVarint(b)
	if err == errIncomplete {
		return nil, 0, errIncomplete
	}
	if err != nil {
		return nil, 0, p.errAt("key", base, path, err.Error())
	}
	num := uint32(key >> 3)
	typ := int(key & 7)
	if num < 1 || num > MaxFieldNumber {
		return nil, 0, p.errAt("key", base, path,
			fmt.Sprintf("invalid field number %d", num))
	}
	switch typ {
	case TypeVarint, TypeFixed64, TypeBytes, TypeFixed32:
	default:
		return nil, 0, p.errAt("key", base, path,
			fmt.Sprintf("unsupported wire type %d", typ))
	}

	f := &Field{Number: num, Type: typ}
	pos := n
	switch typ {
	case TypeVarint:
		v, m, err := decodeVarint(b[pos:])
		if err == errIncomplete {
			return nil, 0, errIncomplete
		}
		if err != nil {
			return nil, 0, p.errAt("varint", base+int64(pos), path, err.Error())
		}
		f.Value = v
		f.Raw = p.copyBytes(b[pos : pos+m])
		pos += m
	case TypeFixed64:
		if len(b)-pos < 8 {
			return nil, 0, errIncomplete
		}
		f.Value = binary.LittleEndian.Uint64(b[pos:])
		f.Raw = p.copyBytes(b[pos : pos+8])
		pos += 8
	case TypeFixed32:
		if len(b)-pos < 4 {
			return nil, 0, errIncomplete
		}
		f.Value = uint64(binary.LittleEndian.Uint32(b[pos:]))
		f.Raw = p.copyBytes(b[pos : pos+4])
		pos += 4
	case TypeBytes:
		l, m, err := decodeVarint(b[pos:])
		if err == errIncomplete {
			return nil, 0, errIncomplete
		}
		if err != nil {
			return nil, 0, p.errAt("length", base+int64(pos), path, err.Error())
		}
		lenOff := base + int64(pos)
		pos += m
		// Check limits before allocating or waiting for the payload.
		if p.lim.MaxFieldLen > 0 && l > uint64(p.lim.MaxFieldLen) {
			return nil, 0, p.errAt("limit", lenOff, path,
				fmt.Sprintf("length-delimited field of %d bytes exceeds limit %d", l, p.lim.MaxFieldLen))
		}
		if uint64(len(b)-pos) < l {
			return nil, 0, errIncomplete
		}
		payloadOff := base + int64(pos)
		f.Raw = p.copyBytes(b[pos : pos+int(l)])
		pos += int(l)
		if sub, ok := schema.nestedFor(num); ok {
			nested, err := p.parseMessage(f.Raw, payloadOff, appendPath(path, num), depth+1, sub)
			if err != nil {
				return nil, 0, err
			}
			f.Nested = nested
		} else if et, ok := schema.packedFor(num); ok {
			packed, err := p.parsePacked(f.Raw, et, payloadOff, appendPath(path, num))
			if err != nil {
				return nil, 0, err
			}
			f.Packed = packed
		}
	}
	return f, pos, nil
}

// parseMessage fully parses b as a message at the given depth.
func (p *Parser) parseMessage(b []byte, base int64, path []uint32, depth int, schema *Schema) (*Message, error) {
	if p.lim.MaxDepth > 0 && depth > p.lim.MaxDepth {
		return nil, p.errAt("limit", base, path,
			fmt.Sprintf("nesting depth exceeds limit %d", p.lim.MaxDepth))
	}
	m := &Message{}
	off := 0
	for off < len(b) {
		if p.lim.MaxFields > 0 && len(m.Fields) >= p.lim.MaxFields {
			return nil, p.errAt("limit", base+int64(off), path,
				fmt.Sprintf("field count exceeds limit %d", p.lim.MaxFields))
		}
		f, n, err := p.scanField(b[off:], base+int64(off), path, depth, schema)
		if err == errIncomplete {
			return nil, p.errAt("truncated", base+int64(off), path,
				"nested message ends mid-field")
		}
		if err != nil {
			return nil, err
		}
		m.Fields = append(m.Fields, *f)
		off += n
		p.stats.FieldsParsed++
	}
	p.stats.MessagesParsed++
	return m, nil
}

// parsePacked decodes a packed array payload. base is the absolute offset
// of raw[0]; path already includes the packed field's number.
func (p *Parser) parsePacked(raw []byte, elemType int, base int64, path []uint32) (*Packed, error) {
	pk := &Packed{ElemType: elemType}
	switch elemType {
	case TypeVarint:
		off := 0
		for off < len(raw) {
			v, n, err := decodeVarint(raw[off:])
			if err == errIncomplete {
				return nil, p.errAt("packed", base+int64(off), path,
					"truncated varint in packed array")
			}
			if err != nil {
				return nil, p.errAt("packed", base+int64(off), path, err.Error())
			}
			pk.Elems = append(pk.Elems, v)
			off += n
		}
	case TypeFixed64:
		if len(raw)%8 != 0 {
			return nil, p.errAt("packed", base, path,
				fmt.Sprintf("packed fixed64 length %d is not a multiple of 8", len(raw)))
		}
		for off := 0; off < len(raw); off += 8 {
			pk.Elems = append(pk.Elems, binary.LittleEndian.Uint64(raw[off:]))
		}
	case TypeFixed32:
		if len(raw)%4 != 0 {
			return nil, p.errAt("packed", base, path,
				fmt.Sprintf("packed fixed32 length %d is not a multiple of 4", len(raw)))
		}
		for off := 0; off < len(raw); off += 4 {
			pk.Elems = append(pk.Elems, uint64(binary.LittleEndian.Uint32(raw[off:])))
		}
	default:
		return nil, p.errAt("packed", base, path,
			fmt.Sprintf("invalid packed element wire type %d", elemType))
	}
	return pk, nil
}

func (p *Parser) copyBytes(b []byte) []byte {
	c := make([]byte, len(b))
	copy(c, b)
	p.stats.BytesAllocated += int64(len(c))
	return c
}

func (p *Parser) errAt(op string, off int64, path []uint32, msg string) *Error {
	var cp []uint32
	if len(path) > 0 {
		cp = make([]uint32, len(path))
		copy(cp, path)
	}
	return &Error{Op: op, Offset: off, Path: cp, Msg: msg}
}

func appendPath(path []uint32, num uint32) []uint32 {
	p := make([]uint32, len(path)+1)
	copy(p, path)
	p[len(path)] = num
	return p
}
