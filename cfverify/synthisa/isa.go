// Package synthisa provides a small synthetic variable-length instruction set
// used to exercise the cfverify package.
//
// Instructions (all multi-byte operands are little-endian):
//
//	0x00           HALT        1 byte, no successors
//	0x01           NOP         1 byte, fall-through successor at pc+1
//	0x02 rel8      JMP         2 bytes, direct successor at pc+int8(rel8)
//	0x03 lo hi     CALLI       3 bytes, fall-through at pc+3 and one permitted
//	                          indirect target at Base + uint16(lo|hi<<8)
//	0x04           BAD         1 byte, always fails to decode
//
// The decoder is deterministic and only performs length/truncation checks;
// address-bounds checks are left to cfverify so it can report them against
// the full image policy.
package synthisa

import (
	"errors"
	"fmt"

	"example.org/domainreissue/q007/cfverify"
)

const (
	OpHalt  byte = 0x00
	OpNop   byte = 0x01
	OpJmp   byte = 0x02
	OpCalli byte = 0x03
	OpBad   byte = 0x04
)

// Instruction lengths in bytes.
const (
	LenHalt  = 1
	LenNop   = 1
	LenJmp   = 2
	LenCalli = 3
	LenBad   = 1
)

// MaxLen is the length of the longest encodable instruction.
const MaxLen = LenCalli

// VersionByte is the conventional fixed version byte placed at image offset
// zero by images built through this package.
const VersionByte byte = 0xA7

var errTruncated = errors.New("synthisa: truncated instruction")

// Decode implements cfverify.Decoder for the synthetic ISA.
func Decode(image []byte, pc uint64) (cfverify.DecodedInstr, error) {
	// With the zero-based decoder, absolute PC and image offset coincide.
	offset := pc
	if offset >= uint64(len(image)) {
		return cfverify.DecodedInstr{}, fmt.Errorf("synthisa: pc %d outside image length %d", pc, len(image))
	}
	op := image[offset]

	switch op {
	case OpHalt:
		return cfverify.DecodedInstr{
			PC:    pc,
			Bytes: span(pc, LenHalt),
		}, nil
	case OpNop:
		return cfverify.DecodedInstr{
			PC:         pc,
			Bytes:      span(pc, LenNop),
			Successors: []uint64{pc + LenNop},
		}, nil
	case OpJmp:
		if offset+LenJmp > uint64(len(image)) {
			return cfverify.DecodedInstr{}, errTruncated
		}
		rel := int8(image[offset+1])
		target := pc + uint64(int64(rel))
		return cfverify.DecodedInstr{
			PC:         pc,
			Bytes:      span(pc, LenJmp),
			Successors: []uint64{target},
		}, nil
	case OpCalli:
		if offset+LenCalli > uint64(len(image)) {
			return cfverify.DecodedInstr{}, errTruncated
		}
		targetOffset := uint16(image[offset+1]) | uint16(image[offset+2])<<8
		return cfverify.DecodedInstr{
			PC:              pc,
			Bytes:           span(pc, LenCalli),
			Successors:      []uint64{pc + LenCalli},
			IndirectTargets: []uint64{uint64(targetOffset)},
		}, nil
	case OpBad:
		return cfverify.DecodedInstr{}, errors.New("synthisa: deliberately undecodable opcode 0x04")
	default:
		return cfverify.DecodedInstr{}, fmt.Errorf("synthisa: unknown opcode 0x%02x at pc %d", op, pc)
	}
}

// DecodeAtBase decodes images whose first byte lives at absolute address base.
// The returned function is still deterministic for a fixed base.
func DecodeAtBase(base uint64) cfverify.Decoder {
	return func(image []byte, pc uint64) (cfverify.DecodedInstr, error) {
		if pc < base || pc > base+uint64(len(image)) {
			return cfverify.DecodedInstr{}, fmt.Errorf("synthisa: pc %d outside image based at %d", pc, base)
		}
		offset := pc - base
		if offset >= uint64(len(image)) {
			return cfverify.DecodedInstr{}, fmt.Errorf("synthisa: offset %d outside image length %d", offset, len(image))
		}
		op := image[offset]
		switch op {
		case OpHalt:
			return cfverify.DecodedInstr{PC: pc, Bytes: span(pc, LenHalt)}, nil
		case OpNop:
			return cfverify.DecodedInstr{
				PC:         pc,
				Bytes:      span(pc, LenNop),
				Successors: []uint64{pc + LenNop},
			}, nil
		case OpJmp:
			if offset+LenJmp > uint64(len(image)) {
				return cfverify.DecodedInstr{}, errTruncated
			}
			rel := int8(image[offset+1])
			return cfverify.DecodedInstr{
				PC:         pc,
				Bytes:      span(pc, LenJmp),
				Successors: []uint64{pc + uint64(int64(rel))},
			}, nil
		case OpCalli:
			if offset+LenCalli > uint64(len(image)) {
				return cfverify.DecodedInstr{}, errTruncated
			}
			targetOffset := uint16(image[offset+1]) | uint16(image[offset+2])<<8
			return cfverify.DecodedInstr{
				PC:              pc,
				Bytes:           span(pc, LenCalli),
				Successors:      []uint64{pc + LenCalli},
				IndirectTargets: []uint64{base + uint64(targetOffset)},
			}, nil
		case OpBad:
			return cfverify.DecodedInstr{}, errors.New("synthisa: deliberately undecodable opcode 0x04")
		default:
			return cfverify.DecodedInstr{}, fmt.Errorf("synthisa: unknown opcode 0x%02x at pc %d", op, pc)
		}
	}
}

// Jmp encodes a JMP instruction with the given signed relative displacement.
func Jmp(rel int8) []byte {
	return []byte{OpJmp, byte(rel)}
}

// Calli encodes a CALLI instruction targeting image offset target.
func Calli(target uint16) []byte {
	return []byte{OpCalli, byte(target), byte(target >> 8)}
}

// Image prefixes instructions with the fixed version byte.
func Image(instructions ...[]byte) []byte {
	size := 1
	for _, ins := range instructions {
		size += len(ins)
	}
	out := make([]byte, 0, size)
	out = append(out, VersionByte)
	for _, ins := range instructions {
		out = append(out, ins...)
	}
	return out
}

// Ins wraps a single-byte opcode as an instruction chunk for Image.
func Ins(op byte) []byte {
	return []byte{op}
}

func span(pc uint64, length int) cfverify.Interval {
	return cfverify.Interval{Start: pc, End: pc + uint64(length)}
}
