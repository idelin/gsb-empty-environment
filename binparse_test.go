package binparse

import (
	"strings"
	"testing"
)

// buildSample returns an encoded file with two records exercising every
// known tag, an unknown field, and two levels of nesting.
func buildSample(order ByteOrder) []byte {
	enc := NewEncoder(order)
	enc.Record().
		ID(42).
		Name("hello").
		Flags(0xBEEF).
		Unknown(0x7F, []byte{0xDE, 0xAD, 0x01}).
		Nested(func(n *RecordEncoder) {
			n.ID(7).Nested(func(inner *RecordEncoder) {
				inner.Name("deep")
			})
		})
	enc.Record().Flags(1)
	return enc.Bytes()
}

func fieldByTag(b Body, tag uint8) *Field {
	for i := range b.Fields {
		if b.Fields[i].Tag == tag {
			return &b.Fields[i]
		}
	}
	return nil
}

func testRoundTrip(t *testing.T, order ByteOrder) {
	data := buildSample(order)
	f, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if f.Order != order {
		t.Errorf("Order = %v, want %v", f.Order, order)
	}
	if f.Version != Version {
		t.Errorf("Version = %d, want %d", f.Version, Version)
	}
	if len(f.Records) != 2 {
		t.Fatalf("len(Records) = %d, want 2", len(f.Records))
	}

	body := f.Records[0].Body
	if got, want := body.Bitmap, uint32(bitID|bitName|bitFlags|bitNested); got != want {
		t.Errorf("Bitmap = %#08x, want %#08x", got, want)
	}
	if len(body.Fields) != 5 {
		t.Fatalf("len(Fields) = %d, want 5", len(body.Fields))
	}
	if got := fieldByTag(body, TagID).ID; got != 42 {
		t.Errorf("ID = %d, want 42", got)
	}
	if got := fieldByTag(body, TagName).Name; got != "hello" {
		t.Errorf("Name = %q, want %q", got, "hello")
	}
	if got := fieldByTag(body, TagFlags).Flags; got != 0xBEEF {
		t.Errorf("Flags = %#x, want 0xbeef", got)
	}

	unk := fieldByTag(body, 0x7F)
	if unk == nil {
		t.Fatal("unknown field missing")
	}
	if len(unk.Raw) != 3 || unk.Raw[0] != 0xDE || unk.Raw[1] != 0xAD || unk.Raw[2] != 0x01 {
		t.Errorf("unknown Raw = %x, want dead01", unk.Raw)
	}

	nested := fieldByTag(body, TagNested).Nested
	if nested == nil {
		t.Fatal("nested body missing")
	}
	if got := fieldByTag(*nested, TagID).ID; got != 7 {
		t.Errorf("nested ID = %d, want 7", got)
	}
	inner := fieldByTag(*nested, TagNested).Nested
	if inner == nil {
		t.Fatal("inner nested body missing")
	}
	if got := fieldByTag(*inner, TagName).Name; got != "deep" {
		t.Errorf("inner Name = %q, want %q", got, "deep")
	}

	if got := fieldByTag(f.Records[1].Body, TagFlags).Flags; got != 1 {
		t.Errorf("record 1 Flags = %d, want 1", got)
	}
}

func TestRoundTripLittleEndian(t *testing.T) { testRoundTrip(t, LittleEndian) }
func TestRoundTripBigEndian(t *testing.T)    { testRoundTrip(t, BigEndian) }

// TestPositions checks reported offsets against a hand-computed layout.
func TestPositions(t *testing.T) {
	enc := NewEncoder(LittleEndian)
	enc.Record().ID(1).Name("hi")
	enc.Record().Flags(9)
	data := enc.Bytes()

	f, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	// Record 0: length prefix at 10, body at 14.
	// body = bitmap(4) + ID(1+2+4) + Name(1+2+2) = 16 bytes.
	r0 := f.Records[0]
	if r0.Offset != 10 || r0.Length != 16 {
		t.Errorf("record 0 = {Offset:%d Length:%d}, want {10 16}", r0.Offset, r0.Length)
	}
	if r0.Body.Offset != 14 || r0.Body.End != 30 {
		t.Errorf("body 0 = {Offset:%d End:%d}, want {14 30}", r0.Body.Offset, r0.Body.End)
	}
	if got := fieldByTag(r0.Body, TagID).Offset; got != 18 {
		t.Errorf("ID offset = %d, want 18", got)
	}
	if got := fieldByTag(r0.Body, TagName).Offset; got != 25 {
		t.Errorf("Name offset = %d, want 25", got)
	}

	// Record 1: length prefix at 30, body = bitmap(4) + Flags(1+2+2) = 9.
	r1 := f.Records[1]
	if r1.Offset != 30 || r1.Length != 9 {
		t.Errorf("record 1 = {Offset:%d Length:%d}, want {30 9}", r1.Offset, r1.Length)
	}
	if r1.Body.End != len(data) {
		t.Errorf("body 1 end = %d, want %d (file size)", r1.Body.End, len(data))
	}
}

// TestTruncation parses every proper prefix of a valid file and requires
// an error (and, implicitly, no panic) for each.
func TestTruncation(t *testing.T) {
	for _, order := range []ByteOrder{LittleEndian, BigEndian} {
		full := buildSample(order)
		for i := 0; i < len(full); i++ {
			if _, err := Parse(full[:i]); err == nil {
				t.Fatalf("order %d: prefix of %d bytes parsed without error", order, i)
			}
		}
		if _, err := Parse(full); err != nil {
			t.Fatalf("order %d: full file: %v", order, err)
		}
	}
}

// expectErr parses data and requires an *Error of the given kind.
func expectErr(t *testing.T, data []byte, kind ErrKind) *Error {
	t.Helper()
	_, err := Parse(data)
	if err == nil {
		t.Fatalf("expected %v, got nil error", kind)
	}
	perr, ok := err.(*Error)
	if !ok {
		t.Fatalf("error type = %T, want *Error", err)
	}
	if perr.Kind != kind {
		t.Fatalf("error kind = %v, want %v (err: %v)", perr.Kind, kind, perr)
	}
	return perr
}

func TestHeaderErrors(t *testing.T) {
	full := buildSample(LittleEndian)

	bad := append([]byte(nil), full...)
	bad[0] = 'X'
	expectErr(t, bad, ErrBadMagic)

	bad = append([]byte(nil), full...)
	bad[4] = 0x09
	expectErr(t, bad, ErrBadByteOrder)

	bad = append([]byte(nil), full...)
	bad[5] = 99
	expectErr(t, bad, ErrUnsupportedVersion)

	expectErr(t, append(append([]byte(nil), full...), 0x00), ErrTrailingData)
}

// TestLengthMismatch corrupts a fixed-size field's length.
func TestLengthMismatch(t *testing.T) {
	data := buildSample(LittleEndian)
	f, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	idOff := fieldByTag(f.Records[0].Body, TagID).Offset
	data[idOff+1] = 3 // length low byte (LE): 4 -> 3
	expectErr(t, data, ErrLengthMismatch)
}

// TestFieldOverrunsBody inflates a field length past its record body.
func TestFieldOverrunsBody(t *testing.T) {
	data := buildSample(LittleEndian)
	f, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	nameOff := fieldByTag(f.Records[0].Body, TagName).Offset
	data[nameOff+1] = 0xFF // length low byte (LE): 5 -> 255
	expectErr(t, data, ErrOutOfBounds)
}

// TestNestedOverrunsParent inflates an inner field so it would cross the
// nested body's end while staying inside the outer record.
func TestNestedOverrunsParent(t *testing.T) {
	data := buildSample(LittleEndian)
	f, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	nested := fieldByTag(f.Records[0].Body, TagNested).Nested
	innerID := fieldByTag(*nested, TagID)
	// Inner ID length 4 -> 40: beyond the nested body, inside the record.
	data[innerID.Offset+1] = 40
	perr := expectErr(t, data, ErrOutOfBounds)
	if perr.Offset != innerID.Offset {
		t.Errorf("error offset = %d, want %d", perr.Offset, innerID.Offset)
	}
}

// TestRecordOverrunsFile inflates a record's length prefix past EOF.
func TestRecordOverrunsFile(t *testing.T) {
	data := buildSample(LittleEndian)
	data[10] = 0xFF // record 0 body length low byte (LE)
	expectErr(t, data, ErrTruncated)
}

func TestBitmapErrors(t *testing.T) {
	full := buildSample(LittleEndian)
	f, err := Parse(full)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	bitmapOff := f.Records[0].Body.Offset

	// Reserved bit set.
	bad := append([]byte(nil), full...)
	bad[bitmapOff+3] |= 0x80 // high byte of LE uint32
	expectErr(t, bad, ErrBitmap)

	// Field present but its declaration bit cleared.
	bad = append([]byte(nil), full...)
	bad[bitmapOff] &^= byte(bitID)
	expectErr(t, bad, ErrBitmap)

	// Declared bit never satisfied: retag the ID field as unknown.
	bad = append([]byte(nil), full...)
	idOff := fieldByTag(f.Records[0].Body, TagID).Offset
	bad[idOff] = 0x55
	expectErr(t, bad, ErrBitmap)

	// Duplicate known field: clone the ID field bytes after itself.
	clone := make([]byte, 0, len(full)+7)
	clone = append(clone, full[:idOff+7]...)
	clone = append(clone, full[idOff:idOff+7]...)
	clone = append(clone, full[idOff+7:]...)
	// Grow record 0's length prefix by 7 to keep bounds consistent.
	recLen := int(full[10]) + 7
	clone[10] = byte(recLen)
	expectErr(t, clone, ErrBitmap)
}

// TestUnknownFieldPayloadIsOpaque hides bytes that look like a valid
// field inside an unknown field's payload; the parser must skip them.
func TestUnknownFieldPayloadIsOpaque(t *testing.T) {
	enc := NewEncoder(BigEndian)
	enc.Record().
		Unknown(0x40, []byte{byte(TagID), 0x00, 0x04, 0xAA, 0xBB, 0xCC, 0xDD}).
		ID(9)
	f, err := Parse(enc.Bytes())
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	body := f.Records[0].Body
	if len(body.Fields) != 2 {
		t.Fatalf("len(Fields) = %d, want 2", len(body.Fields))
	}
	if got := fieldByTag(body, TagID).ID; got != 9 {
		t.Errorf("ID = %d, want 9", got)
	}
}

func TestNestingDepthLimit(t *testing.T) {
	enc := NewEncoder(LittleEndian)
	rec := enc.Record()
	depth := maxDepth + 5
	var wrap func(r *RecordEncoder, n int)
	wrap = func(r *RecordEncoder, n int) {
		if n == 0 {
			r.ID(1)
			return
		}
		r.Nested(func(child *RecordEncoder) { wrap(child, n-1) })
	}
	wrap(rec, depth)
	err := func() error {
		_, err := Parse(enc.Bytes())
		return err
	}()
	if err == nil {
		t.Fatal("expected depth-limit error, got nil")
	}
	perr, ok := err.(*Error)
	if !ok || perr.Kind != ErrNestingTooDeep {
		t.Fatalf("error = %v, want kind %v", err, ErrNestingTooDeep)
	}
	if !strings.Contains(perr.Error(), "nesting too deep") {
		t.Errorf("error message %q lacks kind text", perr.Error())
	}
}

// TestErrorOffsets points each error at the byte that caused it.
func TestErrorOffsets(t *testing.T) {
	full := buildSample(LittleEndian)
	f, err := Parse(full)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	body := f.Records[0].Body

	bad := append([]byte(nil), full...)
	idOff := fieldByTag(body, TagID).Offset
	bad[idOff+1] = 3
	if got := expectErr(t, bad, ErrLengthMismatch).Offset; got != idOff {
		t.Errorf("length-mismatch offset = %d, want %d", got, idOff)
	}

	bad = append([]byte(nil), full...)
	nameOff := fieldByTag(body, TagName).Offset
	bad[nameOff+1] = 0xFF
	if got := expectErr(t, bad, ErrOutOfBounds).Offset; got != nameOff {
		t.Errorf("out-of-bounds offset = %d, want %d", got, nameOff)
	}
}
