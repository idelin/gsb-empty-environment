package netsec

import "errors"

// ErrMalformedSingleCommand is returned when the information object bytes do
// not contain the four octets of a C_SC_NA_1 single command (three IOA octets
// plus one command octet).
var ErrMalformedSingleCommand = errors.New("netsec: malformed single command, want 4 information object bytes")

// SingleCommand is a decoded C_SC_NA_1 (type 45) single command information
// object.
type SingleCommand struct {
	IOA    IOA
	Select bool  // S/E bit: true selects (S/E=1), false executes (S/E=0)
	On     bool  // SCS: true = command ON, false = command OFF
	QU     uint8 // qualifier of command, bits 1..5 of the value octet
}

// DecodeSingleCommand parses one information object: three little-endian IOA
// octets followed by the single command octet.
func DecodeSingleCommand(b []byte) (SingleCommand, error) {
	if len(b) < 4 {
		return SingleCommand{}, ErrMalformedSingleCommand
	}
	ioa := IOA(b[0]) | IOA(b[1])<<8 | IOA(b[2])<<16
	value := b[3]
	return SingleCommand{
		IOA:    ioa,
		Select: value&seBit != 0,
		On:     value&scsBit != 0,
		QU:     (value & quMask) >> quShift,
	}, nil
}

// Encode emits the three little-endian IOA octets followed by the command
// octet, using the same bit layout a 104 slave would forward on the wire.
func (c SingleCommand) Encode() []byte {
	value := byte(c.QU) << quShift & quMask
	if c.Select {
		value |= seBit
	}
	if c.On {
		value |= scsBit
	}
	return []byte{
		byte(c.IOA),
		byte(c.IOA >> 8),
		byte(c.IOA >> 16),
		value,
	}
}

// sameCommandValue reports whether two commands carry the same command value.
// The pairing rule compares the commanded state (SCS ON/OFF); the S/E bit is
// deliberately excluded because it is what distinguishes a select from its
// confirming execute.
func (c SingleCommand) sameCommandValue(other SingleCommand) bool {
	return c.On == other.On
}
