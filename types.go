package binparse

// File is a parsed binary document.
type File struct {
	Order   ByteOrder
	Version uint8
	Records []Record
}

// Record is a length-prefixed record. Offset is the position of its
// length prefix; Length is the body length in bytes.
type Record struct {
	Offset int
	Length int
	Body   Body
}

// Body is a bitmap-declared field sequence. Nested bodies share this
// shape. Offset is the position of the bitmap; End is one past the last
// byte of the enclosing extent.
type Body struct {
	Offset int
	End    int
	Bitmap uint32
	Fields []Field
}

// Field is a single tagged value. Offset is the position of the tag byte
// and Length the value length. Raw is set only for unknown tags and
// aliases the input. Exactly one of ID, Name, Flags, or Nested is
// meaningful, selected by Tag.
type Field struct {
	Tag    uint8
	Offset int
	Length int
	Raw    []byte
	ID     uint32
	Name   string
	Flags  uint16
	Nested *Body
}
