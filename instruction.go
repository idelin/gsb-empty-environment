package seccomp

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Instruction is the Go representation of the kernel's
//
//	struct sock_filter { __u16 code; __u8 jt; __u8 jf; __u32 k; };
//
// It is exactly 8 bytes on the wire (see Encode / Decode).
type Instruction struct {
	// Code is the opcode (class | size/mode/op | source).
	Code uint16
	// JT is the jump offset taken when a conditional jump is true.
	JT uint8
	// JF is the jump offset taken when a conditional jump is false.
	JF uint8
	// K is the generic constant / offset / scratch index operand.
	K uint32
}

// SockFilter is an alias for Instruction for hosts that prefer the kernel
// type name.
type SockFilter = Instruction

// Len returns the jump target index for offset off at program counter pc:
// cBPF offsets skip off instructions after the current one, so
// target = pc + 1 + off.
func JumpTarget(pc, off int) int { return pc + 1 + off }

// Encode appends the canonical 8-byte little-endian sock_filter encoding to
// dst and returns the extended slice.
func (insn Instruction) Encode(dst []byte) []byte {
	var buf [InsnSize]byte
	binary.LittleEndian.PutUint16(buf[0:2], insn.Code)
	buf[2] = insn.JT
	buf[3] = insn.JF
	binary.LittleEndian.PutUint32(buf[4:8], insn.K)
	return append(dst, buf[:]...)
}

// Bytes returns the canonical 8-byte little-endian encoding.
func (insn Instruction) Bytes() []byte {
	return insn.Encode(make([]byte, 0, InsnSize))
}

// DecodeOne decodes a single 8-byte sock_filter record.
func DecodeOne(b []byte) (Instruction, error) {
	if len(b) < InsnSize {
		return Instruction{}, fmt.Errorf("seccomp: short sock_filter record: need %d bytes, got %d", InsnSize, len(b))
	}
	return Instruction{
		Code: binary.LittleEndian.Uint16(b[0:2]),
		JT:   b[2],
		JF:   b[3],
		K:    binary.LittleEndian.Uint32(b[4:8]),
	}, nil
}

// Decode decodes a concatenated array of real 8-byte sock_filter records.
// The byte length must be a positive multiple of 8.
func Decode(b []byte) ([]Instruction, error) {
	if len(b) == 0 {
		return nil, errors.New("seccomp: empty sock_filter array")
	}
	if len(b)%InsnSize != 0 {
		return nil, fmt.Errorf("seccomp: sock_filter byte length %d is not a multiple of %d", len(b), InsnSize)
	}
	out := make([]Instruction, len(b)/InsnSize)
	for i := range out {
		insn, err := DecodeOne(b[i*InsnSize:])
		if err != nil {
			return nil, err
		}
		out[i] = insn
	}
	return out, nil
}

// EncodeAll encodes a whole program as concatenated 8-byte records.
func EncodeAll(insns []Instruction) []byte {
	out := make([]byte, 0, len(insns)*InsnSize)
	for _, insn := range insns {
		out = insn.Encode(out)
	}
	return out
}

// Stmt builds a non-jump statement (BPF_STMT equivalent): jt=jf=0.
func Stmt(code uint16, k uint32) Instruction {
	return Instruction{Code: code, K: k}
}

// Jump builds a conditional jump (BPF_JUMP equivalent).
func Jump(code uint16, k uint32, jt, jf uint8) Instruction {
	return Instruction{Code: code, JT: jt, JF: jf, K: k}
}

// Class returns the instruction class.
func (insn Instruction) Class() uint16 { return insn.Code & classMask }

// IsJump reports whether the instruction is a JMP class instruction.
func (insn Instruction) IsJump() bool { return insn.Class() == ClassJMP }

// IsReturn reports whether the instruction is a RET class instruction.
func (insn Instruction) IsReturn() bool { return insn.Class() == ClassRET }
