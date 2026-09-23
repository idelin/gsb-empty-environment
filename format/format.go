// Package format implements the KVS1 container format, the
// implementation under test.
//
// Wire layout (all integers little-endian):
//
//	magic   4 bytes  "KVS1"
//	count   uint16   number of entries, <= MaxEntries
//	entry   repeated count times:
//	  klen  uint8    1..MaxKeyLen
//	  key   klen     printable ASCII 0x21..0x7E excluding '='
//	  vlen  uint16   0..MaxValLen
//	  value vlen     arbitrary bytes
//	crc32   uint32   IEEE checksum over all preceding bytes
//
// Total encoded size must not exceed Budget.
package format

import "errors"

const (
	Magic      = "KVS1"
	MaxEntries = 64
	MaxKeyLen  = 32
	MaxValLen  = 512
	Budget     = 4096
)

// Class identifies the constraint a format error violates.
type Class string

const (
	ClassNone      Class = ""
	ClassMagic     Class = "magic"
	ClassCount     Class = "count"
	ClassKeyLen    Class = "keylen"
	ClassKeyChar   Class = "keychar"
	ClassValLen    Class = "vallen"
	ClassTruncated Class = "truncated"
	ClassCRC       Class = "crc"
	ClassBudget    Class = "budget"
	ClassTrailing  Class = "trailing"
)

// Error is a format error tagged with the violated constraint class.
type Error struct {
	Class Class
	Msg   string
}

func (e *Error) Error() string { return string(e.Class) + ": " + e.Msg }

// ClassOf extracts the constraint class from an error.
func ClassOf(err error) Class {
	var e *Error
	if errors.As(err, &e) {
		return e.Class
	}
	return "unknown"
}

// Entry is one key/value pair.
type Entry struct {
	Key   string
	Value []byte
}

// ValidKeyByte reports whether c may appear in a key.
func ValidKeyByte(c byte) bool {
	return c >= 0x21 && c <= 0x7e && c != '='
}
