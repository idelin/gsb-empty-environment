package wire

import (
	"encoding/hex"
	"fmt"
	"io"
	"strings"
)

// WriteDump writes a human-readable dump of the message, one field per
// line, with nested messages indented.
func (m *Message) WriteDump(w io.Writer) error {
	return writeDump(w, m, 0)
}

func writeDump(w io.Writer, m *Message, indent int) error {
	pad := strings.Repeat("  ", indent)
	for i := range m.Fields {
		f := &m.Fields[i]
		var err error
		switch {
		case f.Nested != nil:
			if _, err = fmt.Fprintf(w, "%s%d: message {\n", pad, f.Number); err != nil {
				return err
			}
			if err = writeDump(w, f.Nested, indent+1); err != nil {
				return err
			}
			_, err = fmt.Fprintf(w, "%s}\n", pad)
		case f.Packed != nil:
			_, err = fmt.Fprintf(w, "%s%d: packed %s %v\n", pad, f.Number,
				typeName(f.Packed.ElemType), f.Packed.Elems)
		case f.Type == TypeBytes:
			_, err = fmt.Fprintf(w, "%s%d: bytes len=%d %s\n", pad, f.Number,
				len(f.Raw), hex.EncodeToString(f.Raw))
		default:
			_, err = fmt.Fprintf(w, "%s%d: %s %d\n", pad, f.Number,
				typeName(f.Type), f.Value)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func typeName(typ int) string {
	switch typ {
	case TypeVarint:
		return "varint"
	case TypeFixed64:
		return "fixed64"
	case TypeBytes:
		return "bytes"
	case TypeFixed32:
		return "fixed32"
	default:
		return fmt.Sprintf("type%d", typ)
	}
}
