package strdict

import "encoding/binary"

// Record opcodes. A stream is a concatenation of records; framing is explicit
// so a decoder can resynchronize at any byte boundary after buffering.
const (
	opInline byte = 0x00 // uvarint length N, then N raw bytes (UTF-8 string)
	opRef    byte = 0x01 // uvarint index into the session's current dictionary version
	opSwitch byte = 0x02 // uvarint dictionary version; pins all FOLLOWING refs
	opEnd    byte = 0x03 // end of stream
)

// maxLenBytes bounds a uvarint length field (binary.MaxVarintLen64).
const maxLenBytes = binary.MaxVarintLen64
