package netsec

import (
	"errors"
	"testing"
)

// TestDepthLimit chains tables deeper than PatternSet.MaxDepth.
func TestDepthLimit(t *testing.T) {
	dev := NewSimDevice(0x30000)
	// Two tables at 0x10000 and 0x11000 that never self-reach; each has one
	// entry pointing at the next.
	t0 := 0x10000
	t1 := 0x11000
	if err := dev.Load(uint64(t0), buildIndirectWord(uint64(t1), 1)); err != nil {
		t.Fatal(err)
	}
	if err := dev.Load(uint64(t1), buildWriteWord(0x20000, 1)); err != nil {
		t.Fatal(err)
	}
	set := testPatternSet()
	set.MaxDepth = 1
	v, err := NewValidator(set, testAuthz(), dev)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := v.Prepare([][]byte{buildIndirectWord(uint64(t0), 1)}); !errors.Is(err, ErrDepth) {
		t.Fatalf("err = %v, want ErrDepth", err)
	}
}

// TestCrossPageAccess rejects an access whose width crosses out of an
// authorized region even when its starting address is inside.
func TestCrossPageAccess(t *testing.T) {
	authz := Authz{
		ReadRegions:  []Region{{Base: 0x10000, Size: 0x2000}},
		WriteRegions: []Region{{Base: 0x20000, Size: 4}},
	}
	dev := NewSimDevice(0x30000)
	v, err := NewValidator(testPatternSet(), authz, dev)
	if err != nil {
		t.Fatal(err)
	}
	// 4-byte write starting at the last byte of a 4-byte region.
	bad := buildWriteWord(0x20003, 1)
	if _, err := v.Prepare([][]byte{bad}); !errors.Is(err, ErrWriteDenied) {
		t.Fatalf("err = %v, want ErrWriteDenied", err)
	}
}

// TestPatternValidation rejects malformed pattern sets at construction.
func TestPatternValidation(t *testing.T) {
	dev := NewSimDevice(0x30000)
	cases := map[string]PatternSet{
		"missing opcode": {Patterns: map[uint64]Pattern{
			1: {Opcode: 1, Fields: []Field{{Kind: FieldAddrRead, Offset: 0, Width: 1}}},
		}},
		"overlapping fields": {Patterns: map[uint64]Pattern{
			1: {Opcode: 1, Fields: []Field{
				{Kind: FieldOpcode, Offset: 0, Width: 2},
				{Kind: FieldData, Offset: 1, Width: 1},
			}},
		}},
		"indirect missing count": {Patterns: map[uint64]Pattern{
			1: {Opcode: 1, Indirect: true, Fields: []Field{
				{Kind: FieldOpcode, Offset: 0, Width: 1},
				{Kind: FieldAddrRead, Offset: 8, Width: 8},
			}},
		}},
		"direct with count": {Patterns: map[uint64]Pattern{
			1: {Opcode: 1, Fields: []Field{
				{Kind: FieldOpcode, Offset: 0, Width: 1},
				{Kind: FieldCount, Offset: 1, Width: 1},
			}},
		}},
		"bad width": {Patterns: map[uint64]Pattern{
			1: {Opcode: 1, Fields: []Field{
				{Kind: FieldOpcode, Offset: 0, Width: 3},
			}},
		}},
	}
	for name, set := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := NewValidator(set, testAuthz(), dev); !errors.Is(err, ErrInvalidPattern) {
				t.Fatalf("err = %v, want ErrInvalidPattern", err)
			}
		})
	}
}

// TestValidationErrorPath confirms nested table addresses are reported.
func TestValidationErrorPath(t *testing.T) {
	dev := NewSimDevice(0x30000)
	const table uint64 = 0x10000
	illegal := buildWriteWord(0x50000, 1)
	if err := dev.Load(table, illegal); err != nil {
		t.Fatal(err)
	}
	v := newTestValidator(t, dev)
	_, err := v.Prepare([][]byte{buildIndirectWord(table, 1)})
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("err = %v, want ValidationError", err)
	}
	if len(ve.Path) != 1 || ve.Path[0] != table {
		t.Fatalf("Path = %#x, want [%#x]", ve.Path, table)
	}
}
