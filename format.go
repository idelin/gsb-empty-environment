// Package nbf parses and encodes a length-prefixed nested binary record
// format. The file header magic selects the byte order; each record carries
// an optional-field bitmap followed by a length-prefixed body of TLV fields.
// Unknown fields are skipped by length; known fields with a mismatched
// length are rejected. Nested bodies are parsed inside a sub-reader bounded
// by the enclosing field, so no nested body can overrun its parent's range.
package nbf

import "fmt"

const (
	// MagicBigEndian and MagicLittleEndian are the 4-byte file magics.
	// The magic found in the header selects the byte order for the
	// entire file.
	MagicBigEndian    = "NBFB"
	MagicLittleEndian = "NBFL"

	headerLen = 8 // magic(4) + version(2) + record count(2)
)

// Known field tags.
const (
	tagID        = 0 // uint32, fixed length 4
	tagTimestamp = 1 // uint64, fixed length 8
	tagFlags     = 2 // uint16, fixed length 2
	tagName      = 3 // string, variable length
	tagChildren  = 4 // nested record list, variable length
)

// knownMask covers the bitmap bits assigned to known tags (0..4).
const knownMask = 0x1f

// fieldKind describes how a known field's value is interpreted.
type fieldKind int

const (
	kindU32 fieldKind = iota
	kindU64
	kindU16
	kindString
	kindNested
)

// schema maps known tags to their value kind. Tags absent from the map
// (and tags >= 16, which have no bitmap bit) are unknown and skipped.
var schema = map[byte]fieldKind{
	tagID:        kindU32,
	tagTimestamp: kindU64,
	tagFlags:     kindU16,
	tagName:      kindString,
	tagChildren:  kindNested,
}

// fixedLen reports the required value length for fixed-size kinds,
// or -1 for variable-length kinds.
func fixedLen(kind fieldKind) int {
	switch kind {
	case kindU32:
		return 4
	case kindU64:
		return 8
	case kindU16:
		return 2
	}
	return -1
}

// ParseError describes a malformed or truncated input. Offset is the
// absolute byte offset in the original file where the problem was
// detected, even for errors inside nested bodies.
type ParseError struct {
	Offset int
	Msg    string
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("nbf: parse error at offset %d: %s", e.Offset, e.Msg)
}
