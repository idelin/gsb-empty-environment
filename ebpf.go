package netsec

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// BPF opcode fragments and pseudo-fd sources used by the loader.
const (
	// OpLDImm64 is BPF_LD | BPF_IMM | BPF_DW: loads a 64-bit immediate
	// and always occupies two consecutive instruction slots.
	OpLDImm64 = 0x18

	// Pseudo map sources carried in the src_reg nibble of OpLDImm64.
	PseudoMapFD    = 1 // BPF_PSEUDO_MAP_FD
	PseudoMapValue = 2 // BPF_PSEUDO_MAP_VALUE
)

// MapType mirrors the kernel's bpf_map_type values relevant to
// reference-closure validation.
type MapType uint32

const (
	MapTypeUnspec     MapType = 0
	MapTypeHash       MapType = 1
	MapTypeArray      MapType = 2
	MapTypeProgArray  MapType = 3
	MapTypeArrayOfMap MapType = 12
	MapTypeHashOfMaps MapType = 13
)

// IsMapInMap reports whether the map type embeds an inner map template.
func (t MapType) IsMapInMap() bool {
	return t == MapTypeArrayOfMap || t == MapTypeHashOfMaps
}

// Insn is one decoded 8-byte BPF instruction slot.
type Insn struct {
	Opcode uint8
	Dst    uint8
	Src    uint8
	Off    int16
	Imm    int32
}

// IsPseudoMapRef reports whether the instruction is an LD_IMM64 whose
// immediate is a map file descriptor (direct or value-relative).
func (i Insn) IsPseudoMapRef() bool {
	return i.Opcode == OpLDImm64 && (i.Src == PseudoMapFD || i.Src == PseudoMapValue)
}

var (
	ErrTruncated   = errors.New("netsec: instruction stream truncated")
	ErrBadWideSlot = errors.New("netsec: LD_IMM64 second slot must have zero opcode, registers and offset")
)

// Decode parses raw BPF bytecode into instruction slots. LD_IMM64 consumes
// two slots; the second slot is returned as a zero Insn and must carry a
// zero opcode, zero registers and a zero offset in the encoding — anything
// else is an attempt to smuggle a hidden instruction past the validator.
func Decode(code []byte) ([]Insn, error) {
	if len(code) == 0 || len(code)%8 != 0 {
		return nil, fmt.Errorf("%w: length %d is not a positive multiple of 8", ErrTruncated, len(code))
	}
	raw := make([]Insn, 0, len(code)/8)
	for off := 0; off < len(code); off += 8 {
		raw = append(raw, Insn{
			Opcode: code[off],
			Dst:    code[off+1] & 0x0f,
			Src:    code[off+1] >> 4,
			Off:    int16(binary.LittleEndian.Uint16(code[off+2:])),
			Imm:    int32(binary.LittleEndian.Uint32(code[off+4:])),
		})
	}

	insns := make([]Insn, 0, len(raw))
	for i := 0; i < len(raw); i++ {
		ins := raw[i]
		insns = append(insns, ins)
		if ins.Opcode != OpLDImm64 {
			continue
		}
		if i+1 >= len(raw) {
			return nil, fmt.Errorf("%w: LD_IMM64 at slot %d has no second slot", ErrTruncated, i)
		}
		second := raw[i+1]
		if second.Opcode != 0 || second.Dst != 0 || second.Src != 0 || second.Off != 0 {
			return nil, fmt.Errorf("%w: slot %d hides opcode=0x%02x dst=%d src=%d off=%d",
				ErrBadWideSlot, i+1, second.Opcode, second.Dst, second.Src, second.Off)
		}
		insns = append(insns, Insn{Imm: second.Imm})
		i++
	}
	return insns, nil
}
