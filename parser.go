package binparse

import (
	"encoding/binary"
)

// Parse decodes a complete file. It returns an *Error on any malformed
// input and never panics, regardless of the bytes supplied.
func Parse(data []byte) (*File, error) {
	p := &parser{data: data}
	return p.parse()
}

type parser struct {
	data  []byte
	bo    binary.ByteOrder
	order ByteOrder
}

func (p *parser) parse() (*File, error) {
	if len(p.data) < 4 {
		return nil, fail(ErrTruncated, 0, "header truncated: %d of %d bytes", len(p.data), headerLen)
	}
	if string(p.data[:4]) != Magic {
		return nil, fail(ErrBadMagic, 0, "magic %q, want %q", p.data[:4], Magic)
	}
	if len(p.data) < headerLen {
		return nil, fail(ErrTruncated, len(p.data), "header truncated: %d of %d bytes", len(p.data), headerLen)
	}
	switch p.data[4] {
	case byte(LittleEndian):
		p.order = LittleEndian
	case byte(BigEndian):
		p.order = BigEndian
	default:
		return nil, fail(ErrBadByteOrder, 4, "byte-order flag %#02x", p.data[4])
	}
	p.bo = p.order.bin()
	if p.data[5] != Version {
		return nil, fail(ErrUnsupportedVersion, 5, "version %d, want %d", p.data[5], Version)
	}
	count := p.bo.Uint32(p.data[6:10])

	f := &File{Order: p.order, Version: p.data[5]}
	cur := headerLen
	for i := uint32(0); i < count; i++ {
		rec, next, err := p.parseRecord(cur)
		if err != nil {
			return nil, err
		}
		f.Records = append(f.Records, rec)
		cur = next
	}
	if cur != len(p.data) {
		return nil, fail(ErrTrailingData, cur, "%d bytes after last record", len(p.data)-cur)
	}
	return f, nil
}

func (p *parser) parseRecord(at int) (Record, int, error) {
	rec := Record{Offset: at}
	if uint64(at)+4 > uint64(len(p.data)) {
		return rec, 0, fail(ErrTruncated, at, "record length prefix truncated")
	}
	bodyLen := p.bo.Uint32(p.data[at : at+4])
	bodyStart := at + 4
	bodyEnd64 := uint64(bodyStart) + uint64(bodyLen)
	if bodyEnd64 > uint64(len(p.data)) {
		return rec, 0, fail(ErrTruncated, at, "record body of %d bytes overruns input", bodyLen)
	}
	bodyEnd := int(bodyEnd64)
	body, err := p.parseBody(bodyStart, bodyEnd, 0)
	if err != nil {
		return rec, 0, err
	}
	rec.Length = int(bodyLen)
	rec.Body = body
	return rec, bodyEnd, nil
}

// parseBody parses the field sequence in data[start:end]. Every field it
// accepts lies fully inside [start, end), so a nested body can never
// extend past its parent.
func (p *parser) parseBody(start, end, depth int) (Body, error) {
	body := Body{Offset: start, End: end}
	if depth > maxDepth {
		return body, fail(ErrNestingTooDeep, start, "depth %d exceeds limit %d", depth, maxDepth)
	}
	if end-start < 4 {
		return body, fail(ErrTruncated, start, "body of %d bytes cannot hold bitmap", end-start)
	}
	body.Bitmap = p.bo.Uint32(p.data[start : start+4])
	if body.Bitmap&^knownMask != 0 {
		return body, fail(ErrBitmap, start, "reserved bitmap bits set: %#08x", body.Bitmap)
	}

	var seen uint32
	cur := start + 4
	for cur < end {
		field, next, err := p.parseField(cur, end, body.Bitmap, depth)
		if err != nil {
			return body, err
		}
		if bit, ok := bitForTag(field.Tag); ok {
			if seen&bit != 0 {
				return body, fail(ErrBitmap, cur, "duplicate field for tag %#02x", field.Tag)
			}
			seen |= bit
		}
		body.Fields = append(body.Fields, field)
		cur = next
	}

	if missing := body.Bitmap & knownMask &^ seen; missing != 0 {
		return body, fail(ErrBitmap, start, "bitmap declares fields not present: bits %#04x", missing)
	}
	return body, nil
}

func (p *parser) parseField(at, end int, bitmap uint32, depth int) (Field, int, error) {
	field := Field{Tag: p.data[at], Offset: at}
	if end-at < 3 {
		return field, 0, fail(ErrTruncated, at, "field header truncated: %d of 3 bytes", end-at)
	}
	valLen := int(p.bo.Uint16(p.data[at+1 : at+3]))
	valStart := at + 3
	valEnd := valStart + valLen
	if valEnd > end {
		return field, 0, fail(ErrOutOfBounds, at,
			"tag %#02x claims %d value bytes, only %d remain in enclosing body",
			field.Tag, valLen, end-valStart)
	}
	field.Length = valLen

	bit, known := bitForTag(field.Tag)
	if !known {
		field.Raw = p.data[valStart:valEnd]
		return field, valEnd, nil
	}
	if bitmap&bit == 0 {
		return field, 0, fail(ErrBitmap, at, "tag %#02x present but not declared in bitmap %#08x", field.Tag, bitmap)
	}
	if want := fixedLenForTag(field.Tag); want >= 0 && valLen != want {
		return field, 0, fail(ErrLengthMismatch, at,
			"tag %#02x has length %d, want %d", field.Tag, valLen, want)
	}

	value := p.data[valStart:valEnd]
	switch field.Tag {
	case TagID:
		field.ID = p.bo.Uint32(value)
	case TagName:
		field.Name = string(value)
	case TagFlags:
		field.Flags = p.bo.Uint16(value)
	case TagNested:
		nested, err := p.parseBody(valStart, valEnd, depth+1)
		if err != nil {
			return field, 0, err
		}
		field.Nested = &nested
	}
	return field, valEnd, nil
}
