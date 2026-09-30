package codec

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// personV1 is the "old program": two required fields.
func personV1(t *testing.T) *Schema {
	t.Helper()
	s := NewSchema("person", 1)
	must(t, s.AddField(FieldDef{Number: 1, Name: "name", Type: FBytes, Required: true}))
	must(t, s.AddField(FieldDef{Number: 2, Name: "id", Type: FVarint, Required: true}))
	return s
}

// personV2 is the "new program": adds optional email, repeated tags,
// a score, and tombstones the deleted field 3 (fax).
func personV2(t *testing.T) *Schema {
	t.Helper()
	s := NewSchema("person", 2)
	must(t, s.AddField(FieldDef{Number: 1, Name: "name", Type: FBytes, Required: true}))
	must(t, s.AddField(FieldDef{Number: 2, Name: "id", Type: FVarint, Required: true}))
	must(t, s.ReserveDeleted(3, "fax removed in v2"))
	must(t, s.AddField(FieldDef{Number: 4, Name: "email", Type: FBytes}))
	must(t, s.AddField(FieldDef{Number: 5, Name: "tags", Type: FBytes, Repeated: true}))
	must(t, s.AddField(FieldDef{Number: 6, Name: "score", Type: FFixed64}))
	return s
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func encodePersonV2(t *testing.T) []byte {
	t.Helper()
	m := personV2(t).NewMessage()
	must(t, m.SetString(1, "alice"))
	must(t, m.SetUint64(2, 42))
	must(t, m.SetString(4, "alice@example.com"))
	must(t, m.AppendString(5, "admin"))
	must(t, m.AppendString(5, "owner"))
	must(t, m.SetFloat64(6, 9.5))
	data, err := m.Encode()
	must(t, err)
	return data
}

func TestRoundTripAllTypes(t *testing.T) {
	s := NewSchema("all", 1)
	must(t, s.AddField(FieldDef{Number: 1, Name: "u", Type: FVarint}))
	must(t, s.AddField(FieldDef{Number: 2, Name: "b", Type: FVarint}))
	must(t, s.AddField(FieldDef{Number: 3, Name: "f64", Type: FFixed64}))
	must(t, s.AddField(FieldDef{Number: 4, Name: "f32", Type: FFixed32}))
	must(t, s.AddField(FieldDef{Number: 5, Name: "s", Type: FBytes}))
	must(t, s.AddField(FieldDef{Number: 6, Name: "raw", Type: FBytes}))
	must(t, s.AddField(FieldDef{Number: 7, Name: "many", Type: FVarint, Repeated: true}))

	m := s.NewMessage()
	must(t, m.SetUint64(1, 1<<60))
	must(t, m.SetBool(2, true))
	must(t, m.SetFloat64(3, 3.14159))
	must(t, m.SetFloat32(4, 2.5))
	must(t, m.SetString(5, "hello"))
	must(t, m.SetBytes(6, []byte{0, 1, 2, 255}))
	must(t, m.AppendUint64(7, 7))
	must(t, m.AppendUint64(7, 8))
	must(t, m.AppendUint64(7, 9))

	data, err := m.Encode()
	must(t, err)
	got, diag, err := s.Decode(data)
	must(t, err)
	if diag.UnknownFields != 0 || len(diag.Notes) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diag)
	}
	if v, _ := got.GetUint64(1); v != 1<<60 {
		t.Errorf("u = %d", v)
	}
	if v, _ := got.GetBool(2); !v {
		t.Error("b = false")
	}
	if v, _ := got.GetFloat64(3); v != 3.14159 {
		t.Errorf("f64 = %v", v)
	}
	if v, _ := got.GetFloat32(4); v != 2.5 {
		t.Errorf("f32 = %v", v)
	}
	if v, _ := got.GetString(5); v != "hello" {
		t.Errorf("s = %q", v)
	}
	if v, _ := got.GetBytes(6); !bytes.Equal(v, []byte{0, 1, 2, 255}) {
		t.Errorf("raw = %v", v)
	}
	if vs := got.GetAll(7); len(vs) != 3 || vs[0].Num != 7 || vs[2].Num != 9 {
		t.Errorf("many = %v", vs)
	}
}

func TestUnknownFieldsSurviveTranscode(t *testing.T) {
	data := encodePersonV2(t)

	old := personV1(t)
	m, diag, err := old.Decode(data)
	must(t, err)
	if diag.UnknownFields == 0 {
		t.Fatal("old program should report retained unknown fields")
	}
	if diag.EncodedVersion != 2 || diag.SchemaVersion != 1 {
		t.Fatalf("bad versions in diagnostics: %v", diag)
	}
	found := false
	for _, n := range diag.Notes {
		if strings.Contains(n, "NEWER") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected newer-version diagnostic, got %v", diag.Notes)
	}

	// Old program re-saves: new fields must not be lost.
	resaved, err := m.Encode()
	must(t, err)
	back, _, err := personV2(t).Decode(resaved)
	must(t, err)
	if v, ok := back.GetString(4); !ok || v != "alice@example.com" {
		t.Errorf("email lost after transcode: %q ok=%v", v, ok)
	}
	if vs := back.GetAll(5); len(vs) != 2 || string(vs[0].Bytes) != "admin" || string(vs[1].Bytes) != "owner" {
		t.Errorf("tags lost after transcode: %v", vs)
	}
	if v, _ := back.GetFloat64(6); v != 9.5 {
		t.Errorf("score lost after transcode: %v", v)
	}
	if v, _ := back.GetString(1); v != "alice" {
		t.Errorf("known field corrupted: %q", v)
	}
}

func TestOptionalFieldAdditionIsCompatible(t *testing.T) {
	// Old data (v1) decodes fine under v2: new fields are optional.
	old := personV1(t).NewMessage()
	must(t, old.SetString(1, "bob"))
	must(t, old.SetUint64(2, 7))
	data, err := old.Encode()
	must(t, err)

	m, diag, err := personV2(t).Decode(data)
	must(t, err)
	if _, ok := m.GetString(4); ok {
		t.Error("absent optional field reported present")
	}
	if diag.EncodedVersion != 1 || diag.SchemaVersion != 2 {
		t.Fatalf("bad diagnostics: %v", diag)
	}
}

func TestReservedDeletedNumberRejected(t *testing.T) {
	s := personV2(t)
	err := s.AddField(FieldDef{Number: 3, Name: "nickname", Type: FBytes})
	if err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("reusing deleted number 3 should fail, got %v", err)
	}
	// Reserving a live number also fails.
	if err := s.ReserveDeleted(1, "nope"); err == nil {
		t.Fatal("reserving an in-use number should fail")
	}
}

func TestDuplicateNumberTypeConflictRejected(t *testing.T) {
	s := NewSchema("dup", 1)
	must(t, s.AddField(FieldDef{Number: 1, Name: "a", Type: FVarint}))
	if err := s.AddField(FieldDef{Number: 1, Name: "b", Type: FBytes}); err == nil {
		t.Fatal("conflicting redefinition of number 1 should fail")
	} else if !strings.Contains(err.Error(), "conflicting") {
		t.Fatalf("error should mention conflict: %v", err)
	}
	if err := s.AddField(FieldDef{Number: 1, Name: "a", Type: FVarint}); err == nil {
		t.Fatal("exact duplicate definition should also fail")
	}
	if err := s.AddField(FieldDef{Number: 2, Name: "a", Type: FBytes}); err == nil {
		t.Fatal("duplicate name should fail")
	}
}

func TestMissingRequiredReportsNumbers(t *testing.T) {
	s := personV1(t)
	// Hand-craft a message with only field 2 present.
	m := s.NewMessage()
	must(t, m.SetUint64(2, 9))
	data, err := m.Encode()
	must(t, err)

	_, _, err = s.Decode(data)
	var mre *MissingRequiredError
	if !errors.As(err, &mre) {
		t.Fatalf("expected MissingRequiredError, got %v", err)
	}
	if len(mre.Fields) != 1 || mre.Fields[0].Number != 1 {
		t.Fatalf("should report missing field #1, got %v", mre.Fields)
	}
	if !strings.Contains(err.Error(), "#1") {
		t.Fatalf("error should name the number: %v", err)
	}
}

func TestLengthValidatedBeforeAllocation(t *testing.T) {
	s := personV1(t)
	// version=1, field 1 (bytes) declaring a 1 GiB length, no payload.
	huge := []byte{0x01, 0x0A}
	huge = append(huge, 0x80, 0x80, 0x80, 0x80, 0x04) // uvarint 1<<30
	if _, _, err := s.Decode(huge); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversized declared length should fail with ErrTooLarge, got %v", err)
	}
	// Declared length within cap but beyond actual input.
	short := []byte{0x01, 0x0A, 0x64, 'x'} // field 1, len 100, 1 byte present
	if _, _, err := s.Decode(short); !errors.Is(err, ErrTruncated) {
		t.Fatalf("over-declared length should fail with ErrTruncated, got %v", err)
	}
	// Truncated fixed64 (unknown field 7, wire 1) and truncated varint.
	if _, _, err := s.Decode([]byte{0x01, 0x39, 0x01}); !errors.Is(err, ErrTruncated) {
		t.Fatalf("truncated fixed64, got %v", err)
	}
	if _, _, err := s.Decode([]byte{0x01, 0x10, 0x80}); !errors.Is(err, ErrTruncated) {
		t.Fatalf("truncated varint, got %v", err)
	}
}

func TestWireTypeConflictRejected(t *testing.T) {
	s := personV1(t)
	// Field 1 is declared bytes (wire 2); send it as varint (wire 0).
	bad := []byte{0x01, 0x08, 0x7F}
	if _, _, err := s.Decode(bad); err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("wire/declared type conflict should fail, got %v", err)
	}
}

func TestVersionDiagnostics(t *testing.T) {
	v1 := personV1(t)
	v3 := NewSchema("person", 3)
	must(t, v3.AddField(FieldDef{Number: 1, Name: "name", Type: FBytes, Required: true}))
	must(t, v3.AddField(FieldDef{Number: 2, Name: "id", Type: FVarint, Required: true}))

	m := v1.NewMessage()
	must(t, m.SetString(1, "x"))
	must(t, m.SetUint64(2, 1))
	data, err := m.Encode()
	must(t, err)

	_, diag, err := v3.Decode(data)
	must(t, err)
	if diag.EncodedVersion != 1 || diag.SchemaVersion != 3 {
		t.Fatalf("bad versions: %v", diag)
	}
	if len(diag.Notes) == 0 || !strings.Contains(diag.Notes[0], "older") {
		t.Fatalf("expected older-version note, got %v", diag.Notes)
	}
}

func TestNestedMessage(t *testing.T) {
	addr := NewSchema("address", 1)
	must(t, addr.AddField(FieldDef{Number: 1, Name: "city", Type: FBytes, Required: true}))

	outer := NewSchema("outer", 1)
	must(t, outer.AddField(FieldDef{Number: 1, Name: "addr", Type: FBytes, Required: true}))

	a := addr.NewMessage()
	must(t, a.SetString(1, "Shanghai"))
	o := outer.NewMessage()
	must(t, o.SetNested(1, a))
	data, err := o.Encode()
	must(t, err)

	got, _, err := outer.Decode(data)
	must(t, err)
	inner, _, err := got.GetNested(1, addr)
	must(t, err)
	if city, _ := inner.GetString(1); city != "Shanghai" {
		t.Fatalf("city = %q", city)
	}
}

func TestSetRejectsWrongTypeAndUnknownField(t *testing.T) {
	s := personV1(t)
	m := s.NewMessage()
	if err := m.SetString(2, "not a varint"); err == nil {
		t.Fatal("type-mismatched Set should fail")
	}
	if err := m.SetString(99, "ghost"); err == nil {
		t.Fatal("Set on undefined field should fail")
	}
	if err := m.AppendUint64(2, 1); err == nil {
		t.Fatal("Append on non-repeated field should fail")
	}
}
