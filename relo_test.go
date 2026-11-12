package netsec

import (
	"errors"
	"strings"
	"testing"
)

const (
	idU8     uint32 = 1
	idU32    uint32 = 2
	idU64    uint32 = 3
	idInner  uint32 = 4
	idMiddle uint32 = 5
	idOuter  uint32 = 6
)

func testInt(name string, size uint32) Type {
	return Type{Name: name, Kind: KindInt, Size: size}
}

func localNestedBTF() *BTF {
	b := NewBTF()
	b.Add(idU8, testInt("u8", 1))
	b.Add(idU32, testInt("u32", 4))
	b.Add(idU64, testInt("u64", 8))
	b.Add(idInner, Type{
		Name: "inner", Kind: KindStruct, Size: 16,
		Members: []Member{
			{Name: "x", TypeID: idU32, BitOffset: 0},
			{Name: "y", TypeID: idU64, BitOffset: 64},
		},
	})
	b.Add(idMiddle, Type{
		Name: "middle", Kind: KindStruct, Size: 24,
		Members: []Member{
			{Name: "pad", TypeID: idU8, BitOffset: 0},
			{Name: "inner", TypeID: idInner, BitOffset: 64},
		},
	})
	b.Add(idOuter, Type{
		Name: "outer", Kind: KindStruct, Size: 40,
		Members: []Member{
			{Name: "head", TypeID: idU32, BitOffset: 0},
			{Name: "middle", TypeID: idMiddle, BitOffset: 64},
		},
	})
	return b
}

func targetNestedBTF() *BTF {
	b := NewBTF()
	b.Add(101, testInt("u8", 1))
	b.Add(102, testInt("u32", 4))
	b.Add(103, testInt("u64", 8))
	b.Add(104, Type{
		Name: "inner", Kind: KindStruct, Size: 24,
		Members: []Member{
			{Name: "x", TypeID: 102, BitOffset: 0},
			{Name: "y", TypeID: 103, BitOffset: 128},
		},
	})
	b.Add(105, Type{
		Name: "middle", Kind: KindStruct, Size: 32,
		Members: []Member{
			{Name: "pad", TypeID: 101, BitOffset: 0},
			{Name: "inner", TypeID: 104, BitOffset: 64},
		},
	})
	b.Add(106, Type{
		Name: "outer", Kind: KindStruct, Size: 64,
		Members: []Member{
			{Name: "head", TypeID: 102, BitOffset: 0},
			{Name: "middle", TypeID: 105, BitOffset: 128},
		},
	})
	return b
}

func TestValidateNestedFieldOffset(t *testing.T) {
	local := localNestedBTF()
	target := targetNestedBTF()
	relo := Relocation{
		Kind:        RelocFieldByteOffset,
		LocalTypeID: idOuter,
		Access:      "1:1:1",
		AccessSize:  8,
	}

	patch, err := ValidateRelocation(local, target, relo)
	if err != nil {
		t.Fatalf("ValidateRelocation: %v", err)
	}
	if patch.Offset != 40 {
		t.Fatalf("offset = %d, want 40", patch.Offset)
	}
	if patch.BitOffset != 0 {
		t.Fatalf("bit offset = %d, want 0", patch.BitOffset)
	}
	if patch.Size != 8 {
		t.Fatalf("size = %d, want 8", patch.Size)
	}
	if !patch.Exists {
		t.Fatal("Exists = false, want true")
	}
}

func TestValidateRejectsOutOfBoundsAccessIndex(t *testing.T) {
	local := localNestedBTF()
	target := targetNestedBTF()
	relo := Relocation{
		Kind:        RelocFieldByteOffset,
		LocalTypeID: idOuter,
		Access:      "1:1:2",
		AccessSize:  8,
	}

	_, err := ValidateRelocation(local, target, relo)
	if !errors.Is(err, ErrIndexOutOfBounds) {
		t.Fatalf("error = %v, want ErrIndexOutOfBounds", err)
	}
}

func TestValidateRejectsIncompatibleFieldSize(t *testing.T) {
	local := localNestedBTF()
	target := targetNestedBTF()
	// Keep the field at the same offset but make the target member a u8.
	target.Add(104, Type{
		Name: "inner", Kind: KindStruct, Size: 24,
		Members: []Member{
			{Name: "x", TypeID: 102, BitOffset: 0},
			{Name: "y", TypeID: 101, BitOffset: 128},
		},
	})
	relo := Relocation{
		Kind:        RelocFieldByteOffset,
		LocalTypeID: idOuter,
		Access:      "1:1:1",
		AccessSize:  8,
	}

	_, err := ValidateRelocation(local, target, relo)
	if !errors.Is(err, ErrTypeIncompatible) {
		t.Fatalf("error = %v, want ErrTypeIncompatible", err)
	}
}

func TestValidateRejectsConflictingCandidates(t *testing.T) {
	local := localNestedBTF()
	target := targetNestedBTF()
	// A second, structurally valid target type with the same root name but a
	// different member offset. The relocation result must be identical for all
	// candidates, otherwise it is ambiguous.
	target.Add(107, Type{
		Name: "outer", Kind: KindStruct, Size: 72,
		Members: []Member{
			{Name: "head", TypeID: 102, BitOffset: 0},
			{Name: "middle", TypeID: 105, BitOffset: 192},
		},
	})
	relo := Relocation{
		Kind:        RelocFieldByteOffset,
		LocalTypeID: idOuter,
		Access:      "1:1:1",
		AccessSize:  8,
	}

	_, err := ValidateRelocation(local, target, relo)
	if !errors.Is(err, ErrAmbiguousCandidates) {
		t.Fatalf("error = %v, want ErrAmbiguousCandidates", err)
	}
}

func TestValidateAcceptsConsistentCandidates(t *testing.T) {
	local := localNestedBTF()
	target := targetNestedBTF()
	target.Add(107, Type{
		Name: "outer", Kind: KindStruct, Size: 64,
		Members: []Member{
			{Name: "head", TypeID: 102, BitOffset: 0},
			{Name: "middle", TypeID: 105, BitOffset: 128},
		},
	})
	relo := Relocation{
		Kind:        RelocFieldByteOffset,
		LocalTypeID: idOuter,
		Access:      "1:1:1",
		AccessSize:  8,
	}

	patch, err := ValidateRelocation(local, target, relo)
	if err != nil {
		t.Fatalf("ValidateRelocation: %v", err)
	}
	if patch.Offset != 40 {
		t.Fatalf("offset = %d, want 40", patch.Offset)
	}
	if len(patch.TargetTypeIDs) != 2 {
		t.Fatalf("target candidates = %v, want two", patch.TargetTypeIDs)
	}
}

func bitFieldBTF(bitOffset, bitSize uint32) *BTF {
	b := NewBTF()
	b.Add(1, testInt("u32", 4))
	b.Add(2, Type{
		Name: "flags", Kind: KindStruct, Size: 4,
		Members: []Member{
			{Name: "enabled", TypeID: 1, BitOffset: bitOffset, BitSize: bitSize},
		},
	})
	return b
}

func TestValidateBitField(t *testing.T) {
	local := bitFieldBTF(0, 8)
	target := bitFieldBTF(8, 8)
	relo := Relocation{
		Kind:        RelocFieldByteOffset,
		LocalTypeID: 2,
		Access:      "0",
		AccessSize:  1,
	}

	patch, err := ValidateRelocation(local, target, relo)
	if err != nil {
		t.Fatalf("ValidateRelocation: %v", err)
	}
	if patch.Offset != 1 || patch.BitOffset != 0 {
		t.Fatalf("offset = %d bit %d, want byte 1 bit 0", patch.Offset, patch.BitOffset)
	}
	if patch.Size != 1 {
		t.Fatalf("size = %d, want 1", patch.Size)
	}
}

func TestValidateBitFieldNibbleAccess(t *testing.T) {
	local := bitFieldBTF(0, 4)
	target := bitFieldBTF(4, 4)
	relo := Relocation{
		Kind:        RelocFieldByteOffset,
		LocalTypeID: 2,
		Access:      "0",
		AccessSize:  1,
	}

	patch, err := ValidateRelocation(local, target, relo)
	if err != nil {
		t.Fatalf("ValidateRelocation: %v", err)
	}
	if patch.Offset != 0 || patch.BitOffset != 4 {
		t.Fatalf("offset = %d bit %d, want byte 0 bit 4", patch.Offset, patch.BitOffset)
	}
}

func TestValidateRejectsBitFieldAccessWithInsufficientCoverage(t *testing.T) {
	local := bitFieldBTF(0, 16)
	target := bitFieldBTF(4, 16)
	relo := Relocation{
		Kind:        RelocFieldByteOffset,
		LocalTypeID: 2,
		Access:      "0",
		AccessSize:  1,
	}

	_, err := ValidateRelocation(local, target, relo)
	if !errors.Is(err, ErrAccessOutOfBounds) {
		t.Fatalf("error = %v, want ErrAccessOutOfBounds", err)
	}
}

func TestValidateRejectsU8BitFieldReadAsU64(t *testing.T) {
	local := NewBTF()
	local.Add(1, testInt("u8", 1))
	local.Add(2, Type{
		Name: "flags", Kind: KindStruct, Size: 16,
		Members: []Member{{Name: "enabled", TypeID: 1, BitOffset: 0, BitSize: 8}},
	})
	target := NewBTF()
	target.Add(101, testInt("u8", 1))
	target.Add(102, Type{
		Name: "flags", Kind: KindStruct, Size: 16,
		Members: []Member{{Name: "enabled", TypeID: 101, BitOffset: 0, BitSize: 8}},
	})
	relo := Relocation{
		Kind:        RelocFieldByteOffset,
		LocalTypeID: 2,
		Access:      "0",
		AccessSize:  8,
	}

	_, err := ValidateRelocation(local, target, relo)
	if !errors.Is(err, ErrAccessOutOfBounds) {
		t.Fatalf("error = %v, want ErrAccessOutOfBounds", err)
	}
}

func TestValidateRejectsFieldCrossingStruct(t *testing.T) {
	local := NewBTF()
	local.Add(1, testInt("u64", 8))
	local.Add(2, Type{
		Name: "record", Kind: KindStruct, Size: 16,
		Members: []Member{{Name: "value", TypeID: 1, BitOffset: 0}},
	})
	target := NewBTF()
	target.Add(101, testInt("u64", 8))
	target.Add(102, Type{
		Name: "record", Kind: KindStruct, Size: 20,
		Members: []Member{{Name: "value", TypeID: 101, BitOffset: 128}},
	})
	relo := Relocation{
		Kind:        RelocFieldByteOffset,
		LocalTypeID: 2,
		Access:      "0",
		AccessSize:  8,
	}

	_, err := ValidateRelocation(local, target, relo)
	if !errors.Is(err, ErrAccessOutOfBounds) {
		t.Fatalf("error = %v, want ErrAccessOutOfBounds", err)
	}
}

func TestValidateRejectsLocalMemberOutsideContainer(t *testing.T) {
	local := NewBTF()
	local.Add(1, testInt("u64", 8))
	local.Add(2, Type{
		Name: "record", Kind: KindStruct, Size: 4,
		Members: []Member{{Name: "value", TypeID: 1, BitOffset: 0}},
	})
	target := NewBTF()
	target.Add(101, testInt("u64", 8))
	target.Add(102, Type{
		Name: "record", Kind: KindStruct, Size: 16,
		Members: []Member{{Name: "value", TypeID: 101, BitOffset: 0}},
	})
	relo := Relocation{
		Kind:        RelocFieldByteOffset,
		LocalTypeID: 2,
		Access:      "0",
		AccessSize:  8,
	}

	_, err := ValidateRelocation(local, target, relo)
	if !errors.Is(err, ErrAccessOutOfBounds) {
		t.Fatalf("error = %v, want ErrAccessOutOfBounds", err)
	}
}

func TestExplicitTargetCandidatesDisambiguate(t *testing.T) {
	local := localNestedBTF()
	target := targetNestedBTF()
	target.Add(107, Type{
		Name: "outer", Kind: KindStruct, Size: 72,
		Members: []Member{
			{Name: "head", TypeID: 102, BitOffset: 0},
			{Name: "middle", TypeID: 105, BitOffset: 192},
		},
	})
	relo := Relocation{
		Kind:          RelocFieldByteOffset,
		LocalTypeID:   idOuter,
		Access:        "1:1:1",
		AccessSize:    8,
		TargetTypeIDs: []uint32{106},
	}

	patch, err := ValidateRelocation(local, target, relo)
	if err != nil {
		t.Fatalf("ValidateRelocation: %v", err)
	}
	if patch.Offset != 40 {
		t.Fatalf("offset = %d, want 40", patch.Offset)
	}
	if patch.TargetTypeID != 106 {
		t.Fatalf("target type = %d, want 106", patch.TargetTypeID)
	}
}

func TestValidateWithoutTargetCandidate(t *testing.T) {
	local := localNestedBTF()
	target := NewBTF()
	target.Add(101, testInt("u32", 4))
	relo := Relocation{
		Kind:        RelocFieldByteOffset,
		LocalTypeID: idOuter,
		Access:      "1:1:1",
		AccessSize:  8,
	}

	_, err := ValidateRelocation(local, target, relo)
	if !errors.Is(err, ErrNoTargetCandidate) {
		t.Fatalf("error = %v, want ErrNoTargetCandidate", err)
	}
}

func TestFieldNotFoundOnlyAllowedForExistenceCheck(t *testing.T) {
	local := NewBTF()
	local.Add(1, testInt("u64", 8))
	local.Add(2, Type{
		Name: "record", Kind: KindStruct, Size: 16,
		Members: []Member{{Name: "value", TypeID: 1, BitOffset: 0}},
	})
	target := NewBTF()
	target.Add(101, testInt("u64", 8))
	target.Add(102, Type{
		Name: "record", Kind: KindStruct, Size: 16,
		Members: []Member{{Name: "replacement", TypeID: 101, BitOffset: 0}},
	})

	existsRelo := Relocation{
		Kind:        RelocFieldExists,
		LocalTypeID: 2,
		Access:      "0",
	}
	patch, err := ValidateRelocation(local, target, existsRelo)
	if err != nil {
		t.Fatalf("existence relocation: %v", err)
	}
	if patch.Exists {
		t.Fatal("Exists = true, want false")
	}

	offsetRelo := Relocation{
		Kind:        RelocFieldByteOffset,
		LocalTypeID: 2,
		Access:      "0",
		AccessSize:  8,
	}
	_, err = ValidateRelocation(local, target, offsetRelo)
	if !errors.Is(err, ErrFieldNotFound) {
		t.Fatalf("offset relocation error = %v, want ErrFieldNotFound", err)
	}
}

func TestValidateRejectsMissingAccessWidth(t *testing.T) {
	local := localNestedBTF()
	target := targetNestedBTF()
	relo := Relocation{
		Kind:        RelocFieldByteOffset,
		LocalTypeID: idOuter,
		Access:      "1:1:1",
	}

	_, err := ValidateRelocation(local, target, relo)
	if !errors.Is(err, ErrInvalidRelocation) {
		t.Fatalf("error = %v, want ErrInvalidRelocation", err)
	}
}

func TestValidateByteSizeAndSigned(t *testing.T) {
	local := NewBTF()
	local.Add(1, Type{Name: "s32", Kind: KindInt, Size: 4, IntEncoding: IntSigned})
	local.Add(2, Type{
		Name: "record", Kind: KindStruct, Size: 8,
		Members: []Member{{Name: "value", TypeID: 1, BitOffset: 0}},
	})
	target := NewBTF()
	target.Add(101, Type{Name: "s64", Kind: KindInt, Size: 8, IntEncoding: IntSigned})
	target.Add(102, Type{
		Name: "record", Kind: KindStruct, Size: 16,
		Members: []Member{{Name: "value", TypeID: 101, BitOffset: 0}},
	})

	sizePatch, err := ValidateRelocation(local, target, Relocation{
		Kind:        RelocFieldByteSize,
		LocalTypeID: 2,
		Access:      "0",
	})
	if err != nil {
		t.Fatalf("byte size relocation: %v", err)
	}
	if sizePatch.Size != 8 {
		t.Fatalf("size = %d, want 8", sizePatch.Size)
	}

	signedPatch, err := ValidateRelocation(local, target, Relocation{
		Kind:        RelocFieldSigned,
		LocalTypeID: 2,
		Access:      "0",
	})
	if err != nil {
		t.Fatalf("signed relocation: %v", err)
	}
	if !signedPatch.Signed {
		t.Fatal("Signed = false, want true")
	}
}

func TestValidateRejectsMalformedAccess(t *testing.T) {
	local := localNestedBTF()
	target := targetNestedBTF()
	relo := Relocation{
		Kind:        RelocFieldByteOffset,
		LocalTypeID: idOuter,
		Access:      "0:x:1",
		AccessSize:  4,
	}

	_, err := ValidateRelocation(local, target, relo)
	if !errors.Is(err, ErrInvalidAccess) {
		t.Fatalf("error = %v, want ErrInvalidAccess", err)
	}
}

func TestValidateAllReportsRelocationIndex(t *testing.T) {
	local := localNestedBTF()
	target := targetNestedBTF()
	relos := []Relocation{
		{Kind: RelocFieldByteOffset, LocalTypeID: idOuter, Access: "1:1:1", AccessSize: 8},
		{Kind: RelocFieldByteOffset, LocalTypeID: idOuter, Access: "1:1:9", AccessSize: 8},
	}

	_, err := Validate(local, target, relos)
	if !errors.Is(err, ErrIndexOutOfBounds) {
		t.Fatalf("error = %v, want ErrIndexOutOfBounds", err)
	}
	if got := err.Error(); !strings.HasPrefix(got, "relocation 1") {
		t.Fatalf("error %q does not identify relocation 1", got)
	}

	patches, err := Validate(local, target, relos[:1])
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(patches) != 1 || patches[0].Relocation != 0 || patches[0].Offset != 40 {
		t.Fatalf("unexpected patches: %+v", patches)
	}
}
