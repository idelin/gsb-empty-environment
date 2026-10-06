package nbf

import (
	"encoding/binary"
	"fmt"
)

// File is a parsed nbf file.
type File struct {
	Version uint16
	Records []Record
}

// Record is a parsed record. Fields is the bitmap of known optional
// fields that were present; the corresponding struct members are valid
// only when their bit is set.
type Record struct {
	Fields    uint16
	ID        uint32   // tag 0
	Timestamp uint64   // tag 1
	Flags     uint16   // tag 2
	Name      string   // tag 3
	Children  []Record // tag 4, nested record list
}

// Has reports whether the given known tag was present in the record.
func (r *Record) Has(tag uint) bool { return r.Fields&(1<<tag) != 0 }

// Parse decodes a complete nbf file. The byte order is selected from the
// header magic. Any truncation, length mismatch on a known field, or
// nested body overrunning its parent range yields a *ParseError.
func Parse(data []byte) (*File, error) {
	if len(data) < 4 {
		return nil, &ParseError{Offset: 0, Msg: "file too short for magic"}
	}
	var order binary.ByteOrder
	switch string(data[:4]) {
	case MagicBigEndian:
		order = binary.BigEndian
	case MagicLittleEndian:
		order = binary.LittleEndian
	default:
		return nil, &ParseError{Offset: 0, Msg: fmt.Sprintf("unknown magic %q", data[:4])}
	}
	r := &reader{buf: data, order: order}
	if _, err := r.read(4); err != nil {
		return nil, err
	}
	version, err := r.u16()
	if err != nil {
		return nil, err
	}
	count, err := r.u16()
	if err != nil {
		return nil, err
	}
	f := &File{Version: version}
	for i := 0; i < int(count); i++ {
		rec, err := parseRecord(r)
		if err != nil {
			return nil, err
		}
		f.Records = append(f.Records, *rec)
	}
	if r.remaining() != 0 {
		return nil, r.failf("%d trailing bytes after last record", r.remaining())
	}
	return f, nil
}

// parseRecord reads one record (bitmap, body length, body) from r. The
// body is parsed inside a sub-reader bounded by its declared length, so
// it can never consume bytes belonging to the enclosing range.
func parseRecord(r *reader) (*Record, error) {
	bitmap, err := r.u16()
	if err != nil {
		return nil, err
	}
	bodyLen, err := r.u32()
	if err != nil {
		return nil, err
	}
	body, err := r.sub(int(bodyLen))
	if err != nil {
		return nil, err
	}
	rec := &Record{}
	var seen uint16
	for body.remaining() > 0 {
		tag, err := body.u8()
		if err != nil {
			return nil, err
		}
		l, err := body.u16()
		if err != nil {
			return nil, err
		}
		valOff := body.base + body.off
		val, err := body.read(int(l))
		if err != nil {
			return nil, err
		}
		kind, known := schema[tag]
		if tag >= 16 || !known {
			continue // unknown field: skipped by length
		}
		if err := rec.setField(body.order, tag, kind, val, valOff); err != nil {
			return nil, err
		}
		seen |= 1 << tag
	}
	if declared := bitmap & knownMask; declared != seen {
		return nil, &ParseError{
			Offset: body.base,
			Msg:    fmt.Sprintf("bitmap declares known fields %#04x but body contains %#04x", declared, seen),
		}
	}
	rec.Fields = seen
	return rec, nil
}

func (rec *Record) setField(order binary.ByteOrder, tag byte, kind fieldKind, val []byte, valOff int) error {
	if want := fixedLen(kind); want >= 0 && len(val) != want {
		return &ParseError{
			Offset: valOff,
			Msg:    fmt.Sprintf("known field tag %d has length %d, want %d", tag, len(val), want),
		}
	}
	switch kind {
	case kindU32:
		rec.ID = order.Uint32(val)
	case kindU64:
		rec.Timestamp = order.Uint64(val)
	case kindU16:
		rec.Flags = order.Uint16(val)
	case kindString:
		rec.Name = string(val)
	case kindNested:
		children, err := parseNested(val, order, valOff)
		if err != nil {
			return err
		}
		rec.Children = children
	}
	return nil
}

// parseNested parses a nested record list confined to val, the parent
// field's value bytes. Any record whose length prefix reaches past the
// end of val fails the sub-reader's bounds check.
func parseNested(val []byte, order binary.ByteOrder, base int) ([]Record, error) {
	r := &reader{buf: val, base: base, order: order}
	count, err := r.u16()
	if err != nil {
		return nil, err
	}
	var out []Record
	for i := 0; i < int(count); i++ {
		rec, err := parseRecord(r)
		if err != nil {
			return nil, err
		}
		out = append(out, *rec)
	}
	if r.remaining() != 0 {
		return nil, r.failf("%d trailing bytes in nested body", r.remaining())
	}
	return out, nil
}
