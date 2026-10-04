// Package imgcodec encodes an image as a sequence of three instruction
// types and finds the cheapest such sequence for a whole image:
//
//	Literal: emit Len raw pixels.            Cost: LitHeader + Len.
//	Repeat:  emit Len copies of one pixel.   Cost: RepHeader + 1.
//	Ref:     copy Len pixels from the previous row at the same column.
//	         Cost: RefHeader. Len <= MaxRefLen and a reference never
//	         crosses a row boundary.
//
// Encode computes a minimum-cost instruction sequence by dynamic
// programming. Marshal renders instructions to the wire format and
// Decode strictly validates and decodes it back to pixels.
//
// Wire format (all integers little-endian):
//
//	Literal: [0x00][len u16][payload len bytes]
//	Repeat:  [0x01][len u16][value byte]
//	Ref:     [0x02][len u16]
//
// With DefaultParams the header costs match the wire overhead exactly
// (3, 3+1, 3 bytes), so Cost equals len(Marshal(instrs)).
package imgcodec

import (
	"encoding/binary"
	"fmt"
)

// Op identifies an instruction type.
type Op byte

const (
	OpLiteral Op = iota
	OpRepeat
	OpRef
)

func (o Op) String() string {
	switch o {
	case OpLiteral:
		return "literal"
	case OpRepeat:
		return "repeat"
	case OpRef:
		return "ref"
	}
	return fmt.Sprintf("op(0x%02x)", byte(o))
}

// Instr is a single encoding instruction producing Len pixels.
type Instr struct {
	Op   Op
	Len  int    // pixels produced, always >= 1
	Val  byte   // OpRepeat: the repeated pixel value
	Data []byte // OpLiteral: exactly Len raw pixels
}

func (in Instr) String() string {
	switch in.Op {
	case OpRepeat:
		return fmt.Sprintf("repeat(%d x 0x%02x)", in.Len, in.Val)
	case OpRef:
		return fmt.Sprintf("ref(%d)", in.Len)
	default:
		return fmt.Sprintf("literal(%d)", in.Len)
	}
}

// Params describes the image geometry and the cost model.
type Params struct {
	Width, Height int
	LitHeader     int // fixed cost of a literal instruction, payload extra
	RepHeader     int // fixed cost of a repeat instruction, plus 1 value byte
	RefHeader     int // fixed cost of a reference instruction
	MaxRefLen     int // maximum pixels one reference may copy
}

// DefaultParams returns costs matching the wire-format overhead.
func DefaultParams(width, height int) Params {
	return Params{Width: width, Height: height, LitHeader: 3, RepHeader: 3, RefHeader: 3, MaxRefLen: 64}
}

// maxWireLen is the largest length encodable in the u16 length field.
const maxWireLen = 1<<16 - 1

// Validate checks the geometry and cost parameters.
func (p Params) Validate() error {
	if p.Width < 1 || p.Height < 1 {
		return fmt.Errorf("imgcodec: invalid dimensions %dx%d", p.Width, p.Height)
	}
	if p.LitHeader < 0 || p.RepHeader < 0 || p.RefHeader < 0 {
		return fmt.Errorf("imgcodec: header costs must be non-negative")
	}
	if p.MaxRefLen < 1 || p.MaxRefLen > maxWireLen {
		return fmt.Errorf("imgcodec: MaxRefLen %d out of range [1, %d]", p.MaxRefLen, maxWireLen)
	}
	return nil
}

// Cost returns the total encoding cost of instrs under p.
func (p Params) Cost(instrs []Instr) int {
	total := 0
	for _, in := range instrs {
		switch in.Op {
		case OpLiteral:
			total += p.LitHeader + in.Len
		case OpRepeat:
			total += p.RepHeader + 1
		case OpRef:
			total += p.RefHeader
		}
	}
	return total
}

// InstrError reports an invalid instruction passed to Marshal.
type InstrError struct {
	Index  int
	Reason string
}

func (e *InstrError) Error() string {
	return fmt.Sprintf("imgcodec: instruction %d: %s", e.Index, e.Reason)
}

// Marshal renders instrs to the wire format.
func Marshal(instrs []Instr) ([]byte, error) {
	size := 0
	for _, in := range instrs {
		size += 3
		switch in.Op {
		case OpLiteral:
			size += in.Len
		case OpRepeat:
			size++
		}
	}
	buf := make([]byte, 0, size)
	var hdr [3]byte
	for i, in := range instrs {
		if in.Len < 1 || in.Len > maxWireLen {
			return nil, &InstrError{Index: i, Reason: fmt.Sprintf("length %d out of range [1, %d]", in.Len, maxWireLen)}
		}
		hdr[0] = byte(in.Op)
		binary.LittleEndian.PutUint16(hdr[1:], uint16(in.Len))
		switch in.Op {
		case OpLiteral:
			if len(in.Data) != in.Len {
				return nil, &InstrError{Index: i, Reason: fmt.Sprintf("literal payload has %d bytes, want %d", len(in.Data), in.Len)}
			}
			buf = append(buf, hdr[:]...)
			buf = append(buf, in.Data...)
		case OpRepeat:
			buf = append(buf, hdr[:]...)
			buf = append(buf, in.Val)
		case OpRef:
			buf = append(buf, hdr[:]...)
		default:
			return nil, &InstrError{Index: i, Reason: fmt.Sprintf("unknown opcode 0x%02x", byte(in.Op))}
		}
	}
	return buf, nil
}

// EncodeImage finds an optimal encoding of pix and returns its wire form.
func EncodeImage(pix []byte, p Params) ([]byte, error) {
	instrs, err := Encode(pix, p)
	if err != nil {
		return nil, err
	}
	return Marshal(instrs)
}
