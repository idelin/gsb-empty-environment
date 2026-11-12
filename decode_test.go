package netsec

import (
	"errors"
	"testing"
)

// mustContainer is the schema used by the offset-table tests:
//
//	field 0: uint32 (4 fixed bytes)
//	field 1: List[uint64, max 8] (variable)
//
// Its fixed part is therefore 8 bytes: 4 bytes of uint32 plus a 4-byte offset.
func mustTestContainer() *ContainerType {
	return MustContainerType([]Field{
		{Name: "index", Type: Uint32Type},
		{Name: "items", Type: MustListType(Uint64Type, 8)},
	})
}

func validContainerBytes() []byte {
	// index=7, items=[11, 22].
	b := make([]byte, 24)
	putUint32(b[0:4], 7)
	putUint32(b[4:8], 8) // first offset equals the fixed-part length.
	putUint64LE(b[8:16], 11)
	putUint64LE(b[16:24], 22)
	return b
}

func putUint64LE(b []byte, v uint64) {
	for i := 0; i < 8; i++ {
		b[i] = byte(v >> (8 * i))
	}
}

func TestDecodeContainerOK(t *testing.T) {
	v, err := Decode(mustTestContainer(), validContainerBytes())
	if err != nil {
		t.Fatalf("valid container rejected: %v", err)
	}
	index, err := v.FieldByName("index")
	if err != nil {
		t.Fatalf("index field: %v", err)
	}
	gotIndex, err := index.Uint64()
	if err != nil {
		t.Fatalf("index uint64: %v", err)
	}
	if gotIndex != 7 {
		t.Fatalf("index = %d, want 7", gotIndex)
	}
	items, err := v.FieldByName("items")
	if err != nil {
		t.Fatalf("items field: %v", err)
	}
	elems, err := items.Elements()
	if err != nil {
		t.Fatalf("items elements: %v", err)
	}
	if len(elems) != 2 {
		t.Fatalf("items len = %d, want 2", len(elems))
	}
	first, _ := elems[0].Uint64()
	second, _ := elems[1].Uint64()
	if first != 11 || second != 22 {
		t.Fatalf("items = [%d %d], want [11 22]", first, second)
	}
}

func TestOffsetBackwardRejected(t *testing.T) {
	// Two variable fields produce two offsets; make the second one smaller.
	container := MustContainerType([]Field{
		{Name: "a", Type: MustListType(Uint8Type, 16)},
		{Name: "b", Type: MustListType(Uint8Type, 16)},
	})
	b := []byte{
		8, 0, 0, 0, // offset of a = fixed part length 8
		4, 0, 0, 0, // offset of b illegally moves backward into the fixed part
		0xAA, // a
		0xBB, // b
	}
	_, err := Decode(container, b)
	if !errors.Is(err, ErrOffsetOrder) {
		t.Fatalf("backward/equal offset: want ErrOffsetOrder, got %v", err)
	}
}

func TestOffsetOutOfRangeRejected(t *testing.T) {
	container := MustContainerType([]Field{
		{Name: "a", Type: MustListType(Uint8Type, 16)},
		{Name: "b", Type: MustListType(Uint8Type, 16)},
	})
	b := []byte{
		8, 0, 0, 0, // first offset is the legal fixed-part length 8
		100, 0, 0, 0, // second offset points beyond the 10-byte input
		0xAA,
		0xBB,
	}
	_, err := Decode(container, b)
	if !errors.Is(err, ErrOffsetRange) {
		t.Fatalf("out-of-range offset: want ErrOffsetRange, got %v", err)
	}
}

func TestFirstOffsetMustEqualFixedPart(t *testing.T) {
	b := validContainerBytes()
	putUint32(b[4:8], 12) // fixed part is 8 bytes, so 12 is illegal.
	_, err := Decode(mustTestContainer(), b)
	if !errors.Is(err, ErrOffsetFirst) {
		t.Fatalf("bad first offset: want ErrOffsetFirst, got %v", err)
	}
}

func TestTrailingBytesRejected(t *testing.T) {
	// Top-level surplus bytes past a fixed-size value trip the size guard.
	if _, err := Decode(Uint32Type, []byte{1, 2, 3, 4, 5}); !errors.Is(err, ErrMaxSize) {
		t.Fatalf("fixed value trailing byte: want ErrMaxSize, got %v", err)
	}

	// Container with one fixed and one variable field. Appending a byte that no
	// offset reaches leaves an unreachable trailing byte.
	container := MustContainerType([]Field{
		{Name: "index", Type: Uint32Type},
		{Name: "items", Type: MustListType(Uint16Type, 8)},
	})
	good := []byte{
		0x07, 0x00, 0x00, 0x00, // index = 7
		0x08, 0x00, 0x00, 0x00, // items offset = fixed part length
		0xAA, 0x00, 0xBB, 0x00, // two uint16 elements
	}
	if _, err := Decode(container, good); err != nil {
		t.Fatalf("valid container rejected: %v", err)
	}
	withHole := append(append([]byte{}, good...), 0xCC)
	// The odd surplus byte makes the fixed-element list misalign, which strict
	// decoding rejects rather than silently truncating.
	if _, err := Decode(container, withHole); !errors.Is(err, ErrTruncated) &&
		!errors.Is(err, ErrTrailingBytes) && !errors.Is(err, ErrLengthExceeded) {
		t.Fatalf("container trailing byte: want a strict-length error, got %v", err)
	}

	// A self-delimited bitlist followed by a surplus byte at the top level is
	// caught by the maximum-size/strict boundary checks.
	bl := MustBitlistType(8)
	legalBitlist := []byte{0x03} // two payload bits plus delimiter, fully consumed.
	if _, err := Decode(bl, legalBitlist); err != nil {
		t.Fatalf("legal bitlist rejected: %v", err)
	}
	// A zero final byte cannot be a legal bitlist delimiter.
	if _, err := Decode(bl, []byte{0x03, 0x00}); !errors.Is(err, ErrBitlistDelimiter) {
		t.Fatalf("bitlist trailing zero byte: want ErrBitlistDelimiter, got %v", err)
	}

	// A self-delimiting bitlist as the final container field proves the strict
	// boundary check: its delimiter fixes its length, so an appended byte is an
	// unreachable trailing byte rather than an extra list element.
	bitlistContainer := MustContainerType([]Field{
		{Name: "index", Type: Uint32Type},
		{Name: "flags", Type: MustBitlistType(8)},
	})
	bitlistGood := []byte{
		0x01, 0x00, 0x00, 0x00, // index
		0x08, 0x00, 0x00, 0x00, // flags offset = fixed part length
		0x03, // bitlist: two payload bits plus delimiter
	}
	if _, err := Decode(bitlistContainer, bitlistGood); err != nil {
		t.Fatalf("valid bitlist container rejected: %v", err)
	}
	bitlistHole := append(append([]byte{}, bitlistGood...), 0xCC)
	if _, err := Decode(bitlistContainer, bitlistHole); !errors.Is(err, ErrTrailingBytes) {
		t.Fatalf("bitlist container trailing byte: want ErrTrailingBytes, got %v", err)
	}
}

func TestEmptyListAccepted(t *testing.T) {
	// An empty variable-element list serializes to zero bytes on the wire.
	empty, err := Decode(MustListType(Uint32Type, 4), []byte{})
	if err != nil {
		t.Fatalf("empty standalone list rejected: %v", err)
	}
	elems, err := empty.Elements()
	if err != nil {
		t.Fatalf("empty list elements: %v", err)
	}
	if len(elems) != 0 {
		t.Fatalf("empty list len = %d, want 0", len(elems))
	}

	// The same empty list nested in a container occupies zero variable bytes;
	// its offset still points at the fixed-part boundary.
	b := make([]byte, 8)
	putUint32(b[0:4], 7)
	putUint32(b[4:8], 8)
	v, err := Decode(mustTestContainer(), b)
	if err != nil {
		t.Fatalf("container with empty list rejected: %v", err)
	}
	items, err := v.FieldByName("items")
	if err != nil {
		t.Fatalf("items field: %v", err)
	}
	elems, err = items.Elements()
	if err != nil {
		t.Fatalf("nested empty list elements: %v", err)
	}
	if len(elems) != 0 {
		t.Fatalf("nested empty list len = %d, want 0", len(elems))
	}
}

func TestListLengthLimitEnforced(t *testing.T) {
	// Fixed-size element list: bound of two elements (max wire size 8 bytes).
	b := make([]byte, 12)
	putUint32(b[0:4], 1)
	putUint32(b[4:8], 2)
	putUint32(b[8:12], 3)
	if _, err := Decode(MustListType(Uint32Type, 2), b); !errors.Is(err, ErrMaxSize) {
		t.Fatalf("oversized fixed list: want ErrMaxSize, got %v", err)
	}

	// With fixed-size elements an over-long encoding necessarily also exceeds
	// the type's byte budget; either sentinel is an acceptable rejection.
	boundTwo := MustListType(Uint8Type, 2)
	if _, err := Decode(boundTwo, make([]byte, 2)); err != nil {
		t.Fatalf("two-byte list against bound 2: %v", err)
	}
	_, err := Decode(boundTwo, make([]byte, 3))
	if !errors.Is(err, ErrMaxSize) && !errors.Is(err, ErrLengthExceeded) {
		t.Fatalf("over-bound fixed list: want ErrMaxSize or ErrLengthExceeded, got %v", err)
	}

	// Variable-element list: the first offset claims three offsets against a
	// bound of two; the byte budget alone does not reject this input.
	varList := MustListType(MustListType(Uint8Type, 4), 2)
	outer := []byte{
		12, 0, 0, 0, // three offset slots -> three elements, bound is two
		14, 0, 0, 0,
		15, 0, 0, 0,
		0xAA,
		0xBB,
	}
	if _, err := Decode(varList, outer); !errors.Is(err, ErrLengthExceeded) {
		t.Fatalf("oversized variable list: want ErrLengthExceeded, got %v", err)
	}
}

func TestBitlistDelimiterRequired(t *testing.T) {
	bl := MustBitlistType(8)
	if _, err := Decode(bl, []byte{0x00}); !errors.Is(err, ErrBitlistDelimiter) {
		t.Fatalf("zero final byte: want ErrBitlistDelimiter, got %v", err)
	}
	if _, err := Decode(bl, nil); !errors.Is(err, ErrBitlistDelimiter) {
		t.Fatalf("empty bitlist: want ErrBitlistDelimiter, got %v", err)
	}

	// 0x01 = delimiter at bit 0, i.e. a zero-length bit list, which is legal.
	v, err := Decode(bl, []byte{0x01})
	if err != nil {
		t.Fatalf("legal empty-valued bitlist rejected: %v", err)
	}
	bits, err := v.Bits()
	if err != nil {
		t.Fatalf("bits: %v", err)
	}
	if len(bits) != 0 {
		t.Fatalf("bitlist 0x01 has %d payload bits, want 0", len(bits))
	}

	// 8 payload bits need an extra delimiter byte; 9 bits exceeds the bound.
	full := MustBitlistType(8)
	if _, err := Decode(full, []byte{0xFF, 0x01}); err != nil {
		t.Fatalf("8-bit bitlist rejected: %v", err)
	}
	if _, err := Decode(full, []byte{0xFF, 0x02}); !errors.Is(err, ErrLengthExceeded) {
		t.Fatalf("9-bit bitlist: want ErrLengthExceeded, got %v", err)
	}
}

func TestUnionSelectorEnforced(t *testing.T) {
	union := MustUnionType([]Type{Uint8Type, Uint16Type})

	if _, err := Decode(union, []byte{0x02, 0x00}); !errors.Is(err, ErrUnionSelector) {
		t.Fatalf("selector 2: want ErrUnionSelector, got %v", err)
	}
	if _, err := Decode(union, []byte{0xFF}); !errors.Is(err, ErrUnionSelector) {
		t.Fatalf("selector 255: want ErrUnionSelector, got %v", err)
	}

	v, err := Decode(union, []byte{0x01, 0x34, 0x12})
	if err != nil {
		t.Fatalf("legal union rejected: %v", err)
	}
	selector, err := v.Selector()
	if err != nil || selector != 1 {
		t.Fatalf("selector = %d err=%v, want 1", selector, err)
	}
	option, err := v.Option()
	if err != nil {
		t.Fatalf("option: %v", err)
	}
	n, err := option.Uint64()
	if err != nil || n != 0x1234 {
		t.Fatalf("option value = %d err=%v, want 0x1234", n, err)
	}
}

func TestUnionVariableMemberOffset(t *testing.T) {
	union := MustUnionType([]Type{
		Uint8Type,
		MustListType(Uint8Type, 4),
	})
	// selector 1, relative offset 4 (the only legal offset), then two list bytes.
	good := []byte{0x01, 0x04, 0x00, 0x00, 0x00, 0xAA, 0xBB}
	v, err := Decode(union, good)
	if err != nil {
		t.Fatalf("variable union member rejected: %v", err)
	}
	option, err := v.Option()
	if err != nil {
		t.Fatalf("option: %v", err)
	}
	elems, err := option.Elements()
	if err != nil {
		t.Fatalf("elements: %v", err)
	}
	if len(elems) != 2 {
		t.Fatalf("union list len = %d, want 2", len(elems))
	}

	bad := []byte{0x01, 0x09, 0x00, 0x00, 0x00, 0xAA, 0xBB}
	if _, err := Decode(union, bad); !errors.Is(err, ErrOffsetFirst) {
		t.Fatalf("bad union offset: want ErrOffsetFirst, got %v", err)
	}
}

func TestBoolStrict(t *testing.T) {
	if _, err := Decode(BoolType, []byte{0x00}); err != nil {
		t.Fatalf("false rejected: %v", err)
	}
	if _, err := Decode(BoolType, []byte{0x01}); err != nil {
		t.Fatalf("true rejected: %v", err)
	}
	if _, err := Decode(BoolType, []byte{0x02}); !errors.Is(err, ErrBoolValue) {
		t.Fatalf("bool 2: want ErrBoolValue, got %v", err)
	}
}

func TestNestedContainerRoundTrip(t *testing.T) {
	inner := MustContainerType([]Field{
		{Name: "a", Type: Uint16Type},
		{Name: "b", Type: MustListType(Uint8Type, 4)},
	})
	outer := MustContainerType([]Field{
		{Name: "tag", Type: Uint8Type},
		{Name: "inner", Type: inner},
	})
	// inner fixed part: uint16 (2) + offset (4) = 6; inner payload one byte.
	// outer fixed part: uint8 (1) + inner offset (4) = 5.
	b := []byte{
		0x09,                   // tag
		0x05, 0x00, 0x00, 0x00, // inner offset = outer fixed part length
		0x2A, 0x00, // inner.a = 42
		0x06, 0x00, 0x00, 0x00, // inner.b offset = inner fixed part length
		0x7F, // inner.b single byte
	}
	v, err := Decode(outer, b)
	if err != nil {
		t.Fatalf("nested container rejected: %v", err)
	}
	innerVal, err := v.FieldByName("inner")
	if err != nil {
		t.Fatalf("inner field: %v", err)
	}
	a, err := innerVal.FieldByName("a")
	if err != nil {
		t.Fatalf("inner.a: %v", err)
	}
	na, _ := a.Uint64()
	if na != 42 {
		t.Fatalf("inner.a = %d, want 42", na)
	}
	bv, err := innerVal.FieldByName("b")
	if err != nil {
		t.Fatalf("inner.b: %v", err)
	}
	elems, err := bv.Elements()
	if err != nil || len(elems) != 1 {
		t.Fatalf("inner.b elements = %d err=%v, want 1", len(elems), err)
	}
}

func TestBitvectorPaddingRejected(t *testing.T) {
	bv := MustBitvectorType(5) // one byte, top 3 bits must be zero.
	if _, err := Decode(bv, []byte{0x1F}); err != nil {
		t.Fatalf("legal bitvector rejected: %v", err)
	}
	if _, err := Decode(bv, []byte{0x20}); !errors.Is(err, ErrTrailingBytes) {
		t.Fatalf("set padding bit: want ErrTrailingBytes, got %v", err)
	}
}

func TestUnionVariableEmptyMember(t *testing.T) {
	union := MustUnionType([]Type{
		Uint8Type,
		MustListType(Uint8Type, 4),
	})
	// selector 1, offset 4, no payload bytes: an empty variable member.
	good := []byte{0x01, 0x04, 0x00, 0x00, 0x00}
	v, err := Decode(union, good)
	if err != nil {
		t.Fatalf("empty union member rejected: %v", err)
	}
	option, err := v.Option()
	if err != nil {
		t.Fatalf("option: %v", err)
	}
	elems, err := option.Elements()
	if err != nil || len(elems) != 0 {
		t.Fatalf("empty member elements = %d err=%v, want 0", len(elems), err)
	}
}

func TestMalformedFirstOffset(t *testing.T) {
	// Variable-size elements produce an offset table; uint8 lists do not.
	list := MustListType(MustListType(Uint8Type, 4), 16)
	// First offset must be a positive multiple of 4.
	if _, err := Decode(list, []byte{0x03, 0x00, 0x00, 0x00, 0xAA}); !errors.Is(err, ErrOffsetFirst) {
		t.Fatalf("non-multiple-of-4 offset: want ErrOffsetFirst, got %v", err)
	}
}

func TestUnionTruncatedAndTrailing(t *testing.T) {
	union := MustUnionType([]Type{
		Uint8Type,
		MustListType(Uint8Type, 4),
	})
	// Selector alone is truncated for either member.
	if _, err := Decode(union, []byte{0x00}); !errors.Is(err, ErrTruncated) {
		t.Fatalf("fixed member missing body: want ErrTruncated, got %v", err)
	}
	if _, err := Decode(union, []byte{0x01}); !errors.Is(err, ErrTruncated) {
		t.Fatalf("variable member missing body: want ErrTruncated, got %v", err)
	}
	good := []byte{0x01, 0x04, 0x00, 0x00, 0x00, 0xAA, 0xBB}
	if _, err := Decode(union, good); err != nil {
		t.Fatalf("legal variable union rejected: %v", err)
	}
}

func TestEmptyInputRejected(t *testing.T) {
	if _, err := Decode(Uint32Type, nil); !errors.Is(err, ErrTruncated) {
		t.Fatalf("empty fixed value: want ErrTruncated, got %v", err)
	}
	if _, err := Decode(MustUnionType([]Type{Uint8Type}), nil); !errors.Is(err, ErrTruncated) {
		t.Fatalf("empty union: want ErrTruncated, got %v", err)
	}
	// An empty variable-element list is itself a legal zero-length value.
	if _, err := Decode(MustListType(MustListType(Uint8Type, 4), 4), nil); err != nil {
		t.Fatalf("empty variable list rejected: %v", err)
	}
}
