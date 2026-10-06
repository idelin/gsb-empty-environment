// Package binparse parses and encodes a length-prefixed binary record
// format whose byte order is selected by a file header flag.
//
// File layout (all multi-byte integers use the header-selected order):
//
//	header: magic "PGB1" | order byte (1=LE, 2=BE) | version byte | record count uint32
//	record: body length uint32 | body
//	body:   optional-field bitmap uint32 | field*
//	field:  tag uint8 | value length uint16 | value
//
// Known tags are declared in the bitmap; unknown tags are skipped by
// length. A known field whose length does not match its fixed size is an
// error, and no field or nested body may extend past its enclosing body.
package binparse

import "encoding/binary"

// Magic identifies the file format.
const Magic = "PGB1"

// Version is the only format version this package reads and writes.
const Version uint8 = 1

const (
	headerLen = 10 // magic(4) + order(1) + version(1) + count(4)
	maxDepth  = 32 // nested body recursion limit
)

// ByteOrder selects the integer encoding used throughout a file.
type ByteOrder int

const (
	LittleEndian ByteOrder = 1
	BigEndian    ByteOrder = 2
)

func (o ByteOrder) bin() binary.ByteOrder {
	if o == BigEndian {
		return binary.BigEndian
	}
	return binary.LittleEndian
}

// Known field tags.
const (
	TagID     uint8 = 0x01 // fixed 4-byte uint32
	TagName   uint8 = 0x02 // variable-length UTF-8 string
	TagFlags  uint8 = 0x03 // fixed 2-byte uint16
	TagNested uint8 = 0x04 // nested body (bitmap + fields)
)

// Bitmap bits declaring the presence of each known optional field.
const (
	bitID uint32 = 1 << iota
	bitName
	bitFlags
	bitNested

	knownMask = bitID | bitName | bitFlags | bitNested
)

func bitForTag(tag uint8) (uint32, bool) {
	switch tag {
	case TagID:
		return bitID, true
	case TagName:
		return bitName, true
	case TagFlags:
		return bitFlags, true
	case TagNested:
		return bitNested, true
	}
	return 0, false
}

// fixedLenForTag returns the required value length for fixed-size tags,
// or -1 for variable-length tags.
func fixedLenForTag(tag uint8) int {
	switch tag {
	case TagID:
		return 4
	case TagFlags:
		return 2
	}
	return -1
}
