package taggedunion

import (
	"encoding/binary"
	"fmt"
)

// Kind is a supported field type. The set is intentionally closed.
type Kind int

const (
	KindUint8 Kind = iota
	KindInt32
	KindString // u16 length-prefixed UTF-8 bytes
)

// Field is one typed field of a branch schema.
type Field struct {
	Name string
	Kind Kind
}

// Schema describes one (tag, version) branch. Decoding a payload with the
// wrong schema fails deterministically: field bounds are checked and any
// trailing bytes are rejected, so a tag-A payload carrying tag-B fields
// cannot be silently accepted.
type Schema struct {
	Tag     uint16
	Version uint16
	Fields  []Field
}

func (s *Schema) encodeFields(fields map[string]any) ([]byte, error) {
	var out []byte
	for _, f := range s.Fields {
		path := fmt.Sprintf("tag %d v%d.%s", s.Tag, s.Version, f.Name)
		val, ok := fields[f.Name]
		if !ok {
			return nil, &Error{path, 0, "missing required field"}
		}
		switch f.Kind {
		case KindUint8:
			n, ok := val.(uint8)
			if !ok {
				return nil, &Error{path, 0, fmt.Sprintf("want uint8, got %T", val)}
			}
			out = append(out, n)
		case KindInt32:
			n, ok := val.(int32)
			if !ok {
				return nil, &Error{path, 0, fmt.Sprintf("want int32, got %T", val)}
			}
			var b [4]byte
			binary.BigEndian.PutUint32(b[:], uint32(n))
			out = append(out, b[:]...)
		case KindString:
			str, ok := val.(string)
			if !ok {
				return nil, &Error{path, 0, fmt.Sprintf("want string, got %T", val)}
			}
			if len(str) > 0xFFFF {
				return nil, &Error{path, 0, "string exceeds 65535 bytes"}
			}
			var b [2]byte
			binary.BigEndian.PutUint16(b[:], uint16(len(str)))
			out = append(out, b[:]...)
			out = append(out, str...)
		}
	}
	return out, nil
}

// decodeFields decodes payload strictly per schema. base is the absolute
// offset of payload inside the enclosing buffer, used for error offsets.
func (s *Schema) decodeFields(payload []byte, path string, base int) (map[string]any, error) {
	fields := make(map[string]any, len(s.Fields))
	off := 0
	for _, f := range s.Fields {
		fp := path + "." + f.Name
		switch f.Kind {
		case KindUint8:
			if len(payload)-off < 1 {
				return nil, &Error{fp, base + off, "unexpected end of payload"}
			}
			fields[f.Name] = payload[off]
			off++
		case KindInt32:
			if len(payload)-off < 4 {
				return nil, &Error{fp, base + off, "unexpected end of payload"}
			}
			fields[f.Name] = int32(binary.BigEndian.Uint32(payload[off:]))
			off += 4
		case KindString:
			if len(payload)-off < 2 {
				return nil, &Error{fp, base + off, "unexpected end of payload"}
			}
			n := int(binary.BigEndian.Uint16(payload[off:]))
			if len(payload)-off-2 < n {
				return nil, &Error{fp, base + off, fmt.Sprintf("string length %d exceeds remaining payload %d", n, len(payload)-off-2)}
			}
			fields[f.Name] = string(payload[off+2 : off+2+n])
			off += 2 + n
		}
	}
	if off != len(payload) {
		return nil, &Error{
			Path:   path,
			Offset: base + off,
			Msg:    fmt.Sprintf("%d trailing bytes: payload does not match schema tag=%d v=%d", len(payload)-off, s.Tag, s.Version),
		}
	}
	return fields, nil
}
