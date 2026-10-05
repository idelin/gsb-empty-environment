package wire

import (
	"errors"
	"strconv"
	"strings"
)

// ErrNeedMore is returned by Parser.Next when the buffered input does not
// yet contain a complete field. Feed more bytes and call Next again; the
// parser keeps its state, so parsing can be paused and resumed at will.
var ErrNeedMore = errors.New("wire: need more input")

// Error describes a parse failure with the absolute byte offset (from the
// start of the top-level message) and the path of field numbers from the
// root message to the field being parsed when the error occurred.
type Error struct {
	Op     string   // operation that failed: key, varint, length, fixed64, fixed32, packed, nested, limit, truncated
	Offset int64    // absolute byte offset of the offending input
	Path   []uint32 // field-number path from the root message
	Msg    string   // human-readable detail
}

func (e *Error) Error() string {
	var sb strings.Builder
	sb.WriteString("wire: ")
	sb.WriteString(e.Op)
	sb.WriteString(": ")
	sb.WriteString(e.Msg)
	sb.WriteString(" (offset ")
	sb.WriteString(strconv.FormatInt(e.Offset, 10))
	if len(e.Path) > 0 {
		sb.WriteString(", field path ")
		for i, f := range e.Path {
			if i > 0 {
				sb.WriteByte('.')
			}
			sb.WriteString(strconv.FormatUint(uint64(f), 10))
		}
	}
	sb.WriteByte(')')
	return sb.String()
}
