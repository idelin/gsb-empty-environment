package netsec

import (
	"errors"
	"testing"
)

const (
	tenantA = "tenant-a"
	tenantB = "tenant-b"
)

// insn encodes one 8-byte BPF instruction slot.
func insn(op, dst, src uint8, off int16, imm int32) []byte {
	b := make([]byte, 8)
	b[0] = op
	b[1] = dst&0x0f | src<<4
	b[2] = byte(off)
	b[3] = byte(off >> 8)
	b[4] = byte(imm)
	b[5] = byte(imm >> 8)
	b[6] = byte(imm >> 16)
	b[7] = byte(imm >> 24)
	return b
}

// ldImm64 emits a well-formed LD_IMM64 pair: wide load plus zeroed slot
// carrying the high 32 bits of the immediate.
func ldImm64(dst, src uint8, imm int32) []byte {
	out := insn(OpLDImm64, dst, src, 0, imm)
	return append(out, make([]byte, 8)...)
}

func concat(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// exitInsn is BPF_EXIT (0x95).
func exitInsn() []byte { return insn(0x95, 0, 0, 0, 0) }

func mapObj(owner string, typ MapType) Object {
	return Object{Owner: owner, Kind: KindMap, MapType: typ, InnerFD: -1}
}

func validator(table TableResolver) *Validator {
	return &Validator{Tenant: tenantA, Res: table}
}

func TestLegalProgram(t *testing.T) {
	table := TableResolver{
		10: mapObj(tenantA, MapTypeHash),
		11: mapObj(tenantA, MapTypeArray),
	}
	code := concat(
		ldImm64(1, PseudoMapFD, 10),
		ldImm64(2, PseudoMapValue, 11),
		insn(0x07, 1, 0, 0, 1), // r1 += 1
		exitInsn(),
	)
	if err := validator(table).ValidateProgram(code); err != nil {
		t.Fatalf("legal program rejected: %v", err)
	}
}

func TestLegalClosureWithProgArrayAndMapInMap(t *testing.T) {
	table := TableResolver{
		10: {Owner: tenantA, Kind: KindMap, MapType: MapTypeProgArray, InnerFD: -1, ProgFDs: []int{20, 21}},
		20: {Owner: tenantA, Kind: KindProg},
		21: {Owner: tenantA, Kind: KindProg},
		11: {Owner: tenantA, Kind: KindMap, MapType: MapTypeArrayOfMap, InnerFD: 12},
		12: mapObj(tenantA, MapTypeHash),
	}
	code := concat(
		ldImm64(1, PseudoMapFD, 10),
		ldImm64(1, PseudoMapFD, 11),
		exitInsn(),
	)
	if err := validator(table).ValidateProgram(code); err != nil {
		t.Fatalf("legal closure rejected: %v", err)
	}
}

func TestHiddenInstructionInSecondSlot(t *testing.T) {
	table := TableResolver{10: mapObj(tenantA, MapTypeHash)}
	// Attacker hides a full instruction (opcode 0x18 referencing fd 10)
	// inside the second slot of an LD_IMM64.
	second := insn(OpLDImm64, 1, PseudoMapFD, 0, 10)
	code := concat(insn(OpLDImm64, 1, 0, 0, 0), second, exitInsn())

	err := validator(table).ValidateProgram(code)
	if !errors.Is(err, ErrBadWideSlot) {
		t.Fatalf("want ErrBadWideSlot, got %v", err)
	}
}

func TestHiddenRegisterAndOffsetInSecondSlot(t *testing.T) {
	table := TableResolver{10: mapObj(tenantA, MapTypeHash)}
	code := concat(insn(OpLDImm64, 1, 0, 0, 0), insn(0, 3, 0, 8, 0), exitInsn())
	if err := validator(table).ValidateProgram(code); !errors.Is(err, ErrBadWideSlot) {
		t.Fatalf("nonzero dst/off in second slot: want ErrBadWideSlot, got %v", err)
	}
}

func TestCrossTenantMapFD(t *testing.T) {
	table := TableResolver{10: mapObj(tenantB, MapTypeHash)}
	code := concat(ldImm64(1, PseudoMapFD, 10), exitInsn())
	if err := validator(table).ValidateProgram(code); !errors.Is(err, ErrCrossTenant) {
		t.Fatalf("want ErrCrossTenant, got %v", err)
	}
}

func TestCrossTenantMapValue(t *testing.T) {
	table := TableResolver{10: mapObj(tenantB, MapTypeArray)}
	code := concat(ldImm64(1, PseudoMapValue, 10), exitInsn())
	if err := validator(table).ValidateProgram(code); !errors.Is(err, ErrCrossTenant) {
		t.Fatalf("want ErrCrossTenant, got %v", err)
	}
}

func TestCrossTenantTailCall(t *testing.T) {
	table := TableResolver{
		10: {Owner: tenantA, Kind: KindMap, MapType: MapTypeProgArray, InnerFD: -1, ProgFDs: []int{20, 99}},
		20: {Owner: tenantA, Kind: KindProg},
		99: {Owner: tenantB, Kind: KindProg},
	}
	code := concat(ldImm64(1, PseudoMapFD, 10), exitInsn())
	if err := validator(table).ValidateProgram(code); !errors.Is(err, ErrCrossTenant) {
		t.Fatalf("want ErrCrossTenant, got %v", err)
	}
}

func TestCrossTenantMapInMapTemplate(t *testing.T) {
	table := TableResolver{
		10: {Owner: tenantA, Kind: KindMap, MapType: MapTypeHashOfMaps, InnerFD: 30},
		30: mapObj(tenantB, MapTypeHash),
	}
	code := concat(ldImm64(1, PseudoMapFD, 10), exitInsn())
	if err := validator(table).ValidateProgram(code); !errors.Is(err, ErrCrossTenant) {
		t.Fatalf("want ErrCrossTenant, got %v", err)
	}
}

func TestCrossTenantMapInMapEntry(t *testing.T) {
	table := TableResolver{
		10: {Owner: tenantA, Kind: KindMap, MapType: MapTypeArrayOfMap, InnerFD: 11, MapFDs: []int{40}},
		11: mapObj(tenantA, MapTypeHash),
		40: mapObj(tenantB, MapTypeHash),
	}
	code := concat(ldImm64(1, PseudoMapFD, 10), exitInsn())
	if err := validator(table).ValidateProgram(code); !errors.Is(err, ErrCrossTenant) {
		t.Fatalf("want ErrCrossTenant, got %v", err)
	}
}

func TestUnknownFD(t *testing.T) {
	code := concat(ldImm64(1, PseudoMapFD, 77), exitInsn())
	if err := validator(TableResolver{}).ValidateProgram(code); !errors.Is(err, ErrUnknownFD) {
		t.Fatalf("want ErrUnknownFD, got %v", err)
	}
}

func TestTruncatedLDImm64(t *testing.T) {
	code := insn(OpLDImm64, 1, PseudoMapFD, 0, 10) // missing second slot
	if err := validator(TableResolver{}).ValidateProgram(code); !errors.Is(err, ErrTruncated) {
		t.Fatalf("want ErrTruncated, got %v", err)
	}
}

func TestOddLengthStream(t *testing.T) {
	if err := validator(TableResolver{}).ValidateProgram(make([]byte, 12)); !errors.Is(err, ErrTruncated) {
		t.Fatalf("want ErrTruncated, got %v", err)
	}
}

func TestDecodeWideImmediate(t *testing.T) {
	code := concat(insn(OpLDImm64, 1, 0, 0, 0x55667788), insn(0, 0, 0, 0, 0x11223344), exitInsn())
	insns, err := Decode(code)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(insns) != 3 {
		t.Fatalf("want 3 slots, got %d", len(insns))
	}
	if insns[1] != (Insn{Imm: 0x11223344}) {
		t.Fatalf("second slot = %+v, want only Imm preserved", insns[1])
	}
}
