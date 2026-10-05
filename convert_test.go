package rowconv

import (
	"testing"
)

func i64(v int64) *int64 { return &v }
func i32(v int32) *int32 { return &v }

func strVal(s string) *Value { v := Str(s); return &v }

func testSchemas() (Schema, Schema) {
	source := Schema{Fields: []Field{
		{Name: "id", Kind: KindInt},
		{Name: "amount", Kind: KindDecimal, Scale: i32(3)},
		{Name: "note", Kind: KindString},
		{Name: "blob", Kind: KindBytes},
		{Name: "created", Kind: KindTimestamp},
	}}
	target := Schema{Fields: []Field{
		{Name: "id", Kind: KindInt, Required: true, Min: i64(0)},
		{Name: "total", Kind: KindDecimal, Scale: i32(2)},
		{Name: "note", Kind: KindString, MaxBytes: i64(8), Default: strVal("n/a")},
		{Name: "blob", Kind: KindBytes, MaxBytes: i64(4)},
		{Name: "created", Kind: KindTimestamp},
	}}
	return source, target
}

func testConverter(t *testing.T, cfg Config) *Converter {
	source, target := testSchemas()
	c, err := NewConverter(source, target, map[string]string{"total": "amount"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func goodRow() map[string]Value {
	return map[string]Value{
		"id":      Int(7),
		"amount":  Dec(12345, 3), // 12.345
		"note":    Str("hi"),
		"blob":    Bytes([]byte{1, 2}),
		"created": TsRaw("2024-02-29T12:00:00Z"),
	}
}

func TestConvertRowOK(t *testing.T) {
	c := testConverter(t, Config{DecimalMode: ModeHalfEven})
	out, err := c.ConvertRow(goodRow())
	if err != nil {
		t.Fatal(err)
	}
	if out["id"].Int != 7 {
		t.Errorf("id = %v", out["id"])
	}
	// 12.345 -> scale 2: tie, 1234 even -> 1234.
	if out["total"].Dec.Coef != 1234 || out["total"].Dec.Scale != 2 {
		t.Errorf("total = %+v", out["total"].Dec)
	}
	if out["created"].TsMicros != 1709208000000000 {
		t.Errorf("created = %d", out["created"].TsMicros)
	}
	if out["created"].TsIsRaw {
		t.Errorf("converted timestamp must be micros form")
	}
}

func TestAtomicFailure(t *testing.T) {
	c := testConverter(t, Config{}) // reject mode
	row := goodRow()                // amount 12.345 cannot narrow exactly
	out, err := c.ConvertRow(row)
	if err == nil || err.Code != CodeInexact || err.Field != "total" {
		t.Fatalf("want inexact on total, got %v", err)
	}
	if out != nil {
		t.Fatalf("no partial row may be returned, got %v", out)
	}
}

func TestNullVsMissingAndDefault(t *testing.T) {
	c := testConverter(t, Config{DecimalMode: ModeHalfEven})
	// Missing note -> default applies.
	row := goodRow()
	delete(row, "note")
	out, err := c.ConvertRow(row)
	if err != nil {
		t.Fatal(err)
	}
	if out["note"].Str != "n/a" {
		t.Errorf("missing note: want default, got %v", out["note"])
	}
	// Explicit null on nullable field -> null, NOT the default.
	row = goodRow()
	row["note"] = Null()
	out, err = c.ConvertRow(row)
	if err != nil {
		t.Fatal(err)
	}
	if out["note"].Kind != KindNull {
		t.Errorf("explicit null must stay null, got %v", out["note"])
	}
	// Explicit null on required field -> error.
	row = goodRow()
	row["id"] = Null()
	if _, err = c.ConvertRow(row); err == nil || err.Code != CodeNullNotAllowed {
		t.Fatalf("want null_not_allowed, got %v", err)
	}
	// Missing required field without default -> error.
	row = goodRow()
	delete(row, "id")
	if _, err = c.ConvertRow(row); err == nil || err.Code != CodeMissingRequired {
		t.Fatalf("want missing_required, got %v", err)
	}
	// Missing nullable field without default -> null.
	row = goodRow()
	delete(row, "blob")
	out, err = c.ConvertRow(row)
	if err != nil {
		t.Fatal(err)
	}
	if out["blob"].Kind != KindNull {
		t.Errorf("missing nullable field: want null, got %v", out["blob"])
	}
}

func TestInvalidUTF8(t *testing.T) {
	c := testConverter(t, Config{DecimalMode: ModeHalfEven})
	row := goodRow()
	row["note"] = Str("\xff\xfe")
	if _, err := c.ConvertRow(row); err == nil || err.Code != CodeInvalidUTF8 || err.Field != "note" {
		t.Fatalf("want invalid_utf8 on note, got %v", err)
	}
}

func TestRangeAndLength(t *testing.T) {
	c := testConverter(t, Config{DecimalMode: ModeHalfEven})
	row := goodRow()
	row["id"] = Int(-1)
	if _, err := c.ConvertRow(row); err == nil || err.Code != CodeOutOfRange {
		t.Fatalf("negative id: want out_of_range, got %v", err)
	}
	row = goodRow()
	row["note"] = Str("123456789") // 9 bytes > 8
	if _, err := c.ConvertRow(row); err == nil || err.Code != CodeTooLong {
		t.Fatalf("long note: want too_long, got %v", err)
	}
	row = goodRow()
	row["blob"] = Bytes([]byte{1, 2, 3, 4, 5})
	if _, err := c.ConvertRow(row); err == nil || err.Code != CodeTooLong {
		t.Fatalf("long blob: want too_long, got %v", err)
	}
}

func TestTypeAndKindMismatch(t *testing.T) {
	c := testConverter(t, Config{DecimalMode: ModeHalfEven})
	row := goodRow()
	row["id"] = Str("seven")
	if _, err := c.ConvertRow(row); err == nil || err.Code != CodeTypeMismatch {
		t.Fatalf("want type_mismatch, got %v", err)
	}
}

func TestBatchPreservesOrder(t *testing.T) {
	c := testConverter(t, Config{DecimalMode: ModeHalfEven})
	bad := goodRow()
	bad["id"] = Int(-5)
	rows := []map[string]Value{goodRow(), bad, goodRow()}
	results := c.ConvertBatch(rows)
	if len(results) != 3 {
		t.Fatalf("want 3 results, got %d", len(results))
	}
	for i, r := range results {
		if r.Index != i {
			t.Fatalf("result %d has index %d", i, r.Index)
		}
	}
	if !results[0].OK || results[0].Row == nil {
		t.Errorf("row 0 should succeed")
	}
	if results[1].OK || results[1].Err == nil || results[1].Err.Code != CodeOutOfRange {
		t.Errorf("row 1 should fail with out_of_range, got %+v", results[1])
	}
	if results[1].Row != nil {
		t.Errorf("failed row must not carry a partial row")
	}
	if !results[2].OK {
		t.Errorf("row 2 should succeed despite row 1 failing")
	}
}

func TestBadDefault(t *testing.T) {
	source := Schema{Fields: []Field{{Name: "n", Kind: KindString}}}
	bad := Str("this-default-is-way-too-long")
	target := Schema{Fields: []Field{
		{Name: "n", Kind: KindString, MaxBytes: i64(4), Default: &bad},
	}}
	c, err := NewConverter(source, target, nil, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.ConvertRow(map[string]Value{}); err == nil || err.Code != CodeBadDefault {
		t.Fatalf("want bad_default, got %v", err)
	}
}
