package netsec

import (
	"encoding/binary"
	"testing"
)

const (
	opDirectRead  uint64 = 0x11
	opDirectWrite uint64 = 0x22
	opIndirect    uint64 = 0x33
)

func testPatternSet() PatternSet {
	opcode := Field{Kind: FieldOpcode, Offset: 0, Width: 1}
	return PatternSet{
		MaxDepth: 8,
		Patterns: map[uint64]Pattern{
			opDirectRead: {
				Opcode: opDirectRead,
				Fields: []Field{
					opcode,
					{Kind: FieldAddrRead, Offset: 8, Width: 8},
				},
			},
			opDirectWrite: {
				Opcode: opDirectWrite,
				Fields: []Field{
					opcode,
					{Kind: FieldAddrWrite, Offset: 4, Width: 4},
					{Kind: FieldData, Offset: 8, Width: 4},
				},
			},
			opIndirect: {
				Opcode:   opIndirect,
				Indirect: true,
				Fields: []Field{
					opcode,
					{Kind: FieldCount, Offset: 1, Width: 2},
					{Kind: FieldAddrRead, Offset: 8, Width: 8},
				},
			},
		},
	}
}

func testAuthz() Authz {
	return Authz{
		ReadRegions: []Region{
			{Base: 0x0000, Size: 0x4000},
			{Base: 0x10000, Size: 0x4000},
		},
		WriteRegions: []Region{
			{Base: 0x20000, Size: 0x4000},
		},
	}
}

func word(parts ...struct {
	offset int
	width  int
	value  uint64
}) []byte {
	w := make([]byte, WordSize)
	for _, p := range parts {
		putUint(w[p.offset:], p.value, p.width)
	}
	return w
}

func fieldAt(offset, width int, value uint64) struct {
	offset int
	width  int
	value  uint64
} {
	return struct {
		offset int
		width  int
		value  uint64
	}{offset: offset, width: width, value: value}
}

func putUint(buf []byte, value uint64, width int) {
	switch width {
	case 1:
		buf[0] = byte(value)
	case 2:
		binary.LittleEndian.PutUint16(buf, uint16(value))
	case 4:
		binary.LittleEndian.PutUint32(buf, uint32(value))
	default:
		binary.LittleEndian.PutUint64(buf, value)
	}
}

func newTestValidator(t *testing.T, dev DeviceMemory) *Validator {
	t.Helper()
	v, err := NewValidator(testPatternSet(), testAuthz(), dev)
	if err != nil {
		t.Fatalf("NewValidator: %v", err)
	}
	return v
}
