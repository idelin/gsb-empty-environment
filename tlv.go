package sframe

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strings"
)

// Type identifies the encoding of a field value.
type Type byte

const (
	TypeString Type = 0x01
	TypeInt64  Type = 0x02
	TypeBool   Type = 0x03
	TypeObject Type = 0x04 // nested fields; enables structured extension
	TypeBytes  Type = 0x05
)

// Structural limits, enforced while parsing so hostile input cannot trigger
// excessive work or recursion.
const (
	maxDepth  = 32
	maxFields = 1 << 12
)

// Field is one TLV entry: nameLen(1) name type(1) valueLen(4) value.
// Unknown fields (names this tool does not recognize) parse and re-serialize
// verbatim, so extensions survive a redaction pass untouched.
type Field struct {
	Name string
	Type Type
	Raw  []byte   // TypeString / TypeBytes
	Int  int64    // TypeInt64
	Bool bool     // TypeBool
	Kids []*Field // TypeObject
}

// ParsePayload decodes a frame payload into a field tree. The payload is
// already known-complete (frame CRC verified), so any error is corruption.
func ParsePayload(payload []byte) ([]*Field, error) {
	return parseObject(payload, 0)
}

func parseObject(buf []byte, depth int) ([]*Field, error) {
	if depth > maxDepth {
		return nil, corruptf("nesting deeper than %d levels", maxDepth)
	}
	var fields []*Field
	for len(buf) > 0 {
		if len(fields) >= maxFields {
			return nil, corruptf("more than %d fields in one object", maxFields)
		}
		f, n, err := parseField(buf, depth)
		if err != nil {
			return nil, err
		}
		fields = append(fields, f)
		buf = buf[n:]
	}
	return fields, nil
}

func parseField(buf []byte, depth int) (*Field, int, error) {
	if len(buf) < 1 {
		return nil, 0, corruptf("truncated field header")
	}
	nameLen := int(buf[0])
	if len(buf) < 1+nameLen+1+4 {
		return nil, 0, corruptf("truncated field header (name %d bytes)", nameLen)
	}
	name := string(buf[1 : 1+nameLen])
	typ := Type(buf[1+nameLen])
	vlen := binary.BigEndian.Uint32(buf[2+nameLen : 6+nameLen])
	if uint64(vlen) > uint64(len(buf)) {
		return nil, 0, corruptf("field %q declares %d value bytes beyond buffer", name, vlen)
	}
	total := 6 + nameLen + int(vlen)
	if len(buf) < total {
		return nil, 0, corruptf("field %q truncated: need %d bytes, have %d", name, total, len(buf))
	}
	val := buf[6+nameLen : total]
	f := &Field{Name: name, Type: typ}
	switch typ {
	case TypeString, TypeBytes:
		f.Raw = append([]byte(nil), val...)
	case TypeInt64:
		if vlen != 8 {
			return nil, 0, corruptf("field %q: int64 needs 8 bytes, got %d", name, vlen)
		}
		f.Int = int64(binary.BigEndian.Uint64(val))
	case TypeBool:
		if vlen != 1 || val[0] > 1 {
			return nil, 0, corruptf("field %q: invalid bool encoding", name)
		}
		f.Bool = val[0] == 1
	case TypeObject:
		kids, err := parseObject(val, depth+1)
		if err != nil {
			return nil, 0, err
		}
		f.Kids = kids
	default:
		return nil, 0, corruptf("field %q: unknown type 0x%02x", name, byte(typ))
	}
	return f, total, nil
}

// MarshalObject serializes fields back to payload form, recomputing every
// length prefix from the actual value sizes.
func MarshalObject(fields []*Field) []byte {
	var out []byte
	for _, f := range fields {
		out = f.appendTo(out)
	}
	return out
}

func (f *Field) valueBytes() []byte {
	switch f.Type {
	case TypeString, TypeBytes:
		return f.Raw
	case TypeInt64:
		var b [8]byte
		binary.BigEndian.PutUint64(b[:], uint64(f.Int))
		return b[:]
	case TypeBool:
		if f.Bool {
			return []byte{1}
		}
		return []byte{0}
	case TypeObject:
		return MarshalObject(f.Kids)
	}
	return nil
}

func (f *Field) appendTo(dst []byte) []byte {
	val := f.valueBytes()
	dst = append(dst, byte(len(f.Name)))
	dst = append(dst, f.Name...)
	dst = append(dst, byte(f.Type))
	var lb [4]byte
	binary.BigEndian.PutUint32(lb[:], uint32(len(val)))
	dst = append(dst, lb[:]...)
	return append(dst, val...)
}

// Dump renders a field tree for humans. Used by the demo and tests.
func Dump(fields []*Field) string {
	var b strings.Builder
	dumpFields(&b, fields, 0)
	return b.String()
}

func dumpFields(b *strings.Builder, fields []*Field, depth int) {
	pad := strings.Repeat("  ", depth)
	for _, f := range fields {
		switch f.Type {
		case TypeString:
			fmt.Fprintf(b, "%s%s: %q\n", pad, f.Name, f.Raw)
		case TypeBytes:
			fmt.Fprintf(b, "%s%s: 0x%s\n", pad, f.Name, hex.EncodeToString(f.Raw))
		case TypeInt64:
			fmt.Fprintf(b, "%s%s: %d\n", pad, f.Name, f.Int)
		case TypeBool:
			fmt.Fprintf(b, "%s%s: %t\n", pad, f.Name, f.Bool)
		case TypeObject:
			fmt.Fprintf(b, "%s%s:\n", pad, f.Name)
			dumpFields(b, f.Kids, depth+1)
		}
	}
}
