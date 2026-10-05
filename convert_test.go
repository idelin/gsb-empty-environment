package rowconv

import (
	"encoding/json"
	"math/big"
	"strings"
	"testing"
)

func decVal(coeff string, scale int) *Value {
	c, _ := new(big.Int).SetString(coeff, 10)
	return &Value{Kind: TypeDecimal, Dec: &Decimal{Coefficient: c, Scale: scale}}
}

func intVal(n int64) *Value  { return &Value{Kind: TypeInt, Int: n} }
func strVal(s string) *Value { return &Value{Kind: TypeString, Str: s} }

func testSchemas() (src, tgt *Schema) {
	src = &Schema{Fields: []Field{
		{Name: "id", Type: TypeInt, Bits: 64},
		{Name: "price", Type: TypeDecimal, Scale: 4},
		{Name: "name", Type: TypeString},
		{Name: "blob", Type: TypeBytes},
		{Name: "ts", Type: TypeTimestamp},
		{Name: "nick", Type: TypeString},
	}}
	tgt = &Schema{Fields: []Field{
		{Name: "id", Type: TypeInt, Bits: 32, Required: true},
		{Name: "price", Type: TypeDecimal, Precision: 12, Scale: 2, Required: true},
		{Name: "name", Type: TypeString, MaxBytes: 10, Required: true},
		{Name: "blob", Type: TypeBytes, MaxBytes: 4, Nullable: true},
		{Name: "ts", Type: TypeTimestamp, Required: true},
		{Name: "nick", Type: TypeString, MaxBytes: 10, Nullable: true,
			Default: strVal("anonymous")},
	}}
	return src, tgt
}

func testMapping() map[string]string {
	return map[string]string{
		"id": "id", "price": "price", "name": "name",
		"blob": "blob", "ts": "ts", "nick": "nick",
	}
}

func goodRow() Row {
	return Row{
		"id":    intVal(7),
		"price": decVal("1234500", 4), // 123.4500
		"name":  strVal("alice"),
		"blob":  &Value{Kind: TypeBytes, Bytes: []byte{0, 1, 2}},
		"ts":    &Value{Kind: TypeTimestamp, Micros: 1709221800123456},
		"nick":  strVal("al"),
	}
}

func newTestConverter(t *testing.T, mode RoundingMode) *Converter {
	t.Helper()
	src, tgt := testSchemas()
	c, err := NewConverter(src, tgt, testMapping(), Options{Rounding: mode})
	if err != nil {
		t.Fatalf("NewConverter: %v", err)
	}
	return c
}

func TestConvertRowHappyPath(t *testing.T) {
	c := newTestConverter(t, Reject)
	out, err := c.ConvertRow(goodRow())
	if err != nil {
		t.Fatal(err)
	}
	if out["id"].Int != 7 {
		t.Errorf("id = %v", out["id"].Int)
	}
	if out["price"].Dec.Coefficient.String() != "12345" || out["price"].Dec.Scale != 2 {
		t.Errorf("price = %v", out["price"].Dec)
	}
	if out["ts"].Micros != 1709221800123456 {
		t.Errorf("ts = %v", out["ts"].Micros)
	}
}

func TestIntOverflow(t *testing.T) {
	c := newTestConverter(t, Reject)
	for _, n := range []int64{2147483647, -2147483648} {
		row := goodRow()
		row["id"] = intVal(n)
		if _, err := c.ConvertRow(row); err != nil {
			t.Errorf("id %d should fit int32: %v", n, err)
		}
	}
	for _, n := range []int64{2147483648, -2147483649, 5000000000} {
		row := goodRow()
		row["id"] = intVal(n)
		if _, err := c.ConvertRow(row); err == nil ||
			!strings.Contains(err.Error(), "overflows int32") {
			t.Errorf("id %d: expected int32 overflow error, got %v", n, err)
		}
	}
}

func TestDecimalInexactRejectedByDefault(t *testing.T) {
	c := newTestConverter(t, Reject)
	row := goodRow()
	row["price"] = decVal("1234567", 4) // 123.4567, not representable at scale 2
	if _, err := c.ConvertRow(row); err == nil ||
		!strings.Contains(err.Error(), "cannot be represented exactly") {
		t.Errorf("expected inexact decimal error, got %v", err)
	}
}

func TestDecimalTiesToEven(t *testing.T) {
	c := newTestConverter(t, TiesToEven)
	row := goodRow()
	row["price"] = decVal("-1234550", 4) // -123.455 -> tie -> -123.46
	out, err := c.ConvertRow(row)
	if err != nil {
		t.Fatal(err)
	}
	if got := out["price"].Dec.Coefficient.String(); got != "-12346" {
		t.Errorf("price = %s, want -12346", got)
	}
}

func TestDecimalPrecisionOverflow(t *testing.T) {
	c := newTestConverter(t, Reject)
	row := goodRow()
	row["price"] = decVal("123456789012300", 4) // 12345678901.2300 -> 13 digits at scale 2
	if _, err := c.ConvertRow(row); err == nil ||
		!strings.Contains(err.Error(), "exceeds precision 12") {
		t.Errorf("expected precision overflow, got %v", err)
	}
}

func TestInvalidUTF8(t *testing.T) {
	c := newTestConverter(t, Reject)
	row := goodRow()
	row["name"] = strVal(string([]byte{'a', 0xff, 0xfe}))
	if _, err := c.ConvertRow(row); err == nil ||
		!strings.Contains(err.Error(), "invalid UTF-8") {
		t.Errorf("expected invalid UTF-8 error, got %v", err)
	}
}

func TestStringAndBytesLimits(t *testing.T) {
	c := newTestConverter(t, Reject)
	row := goodRow()
	row["name"] = strVal("héllo") // 6 bytes, within maxBytes 10
	if _, err := c.ConvertRow(row); err != nil {
		t.Errorf("6-byte string within maxBytes 10 should pass: %v", err)
	}
	row["name"] = strVal(strings.Repeat("x", 11))
	if _, err := c.ConvertRow(row); err == nil ||
		!strings.Contains(err.Error(), "exceeds maxBytes 10") {
		t.Errorf("expected string maxBytes error, got %v", err)
	}
	row = goodRow()
	row["blob"] = &Value{Kind: TypeBytes, Bytes: []byte{1, 2, 3, 4, 5}}
	if _, err := c.ConvertRow(row); err == nil ||
		!strings.Contains(err.Error(), "exceeds maxBytes 4") {
		t.Errorf("expected bytes maxBytes error, got %v", err)
	}
}

func TestNullMissingAndDefault(t *testing.T) {
	c := newTestConverter(t, Reject)

	// Explicit null with default -> default is used.
	row := goodRow()
	row["nick"] = nil
	out, err := c.ConvertRow(row)
	if err != nil {
		t.Fatal(err)
	}
	if out["nick"].Str != "anonymous" {
		t.Errorf("nick = %v, want default", out["nick"])
	}

	// Explicit null on nullable field without default -> null output.
	row = goodRow()
	row["blob"] = nil
	out, err = c.ConvertRow(row)
	if err != nil {
		t.Fatal(err)
	}
	v, present := out["blob"]
	if !present || v != nil {
		t.Errorf("blob should be explicit null, got %v (present=%v)", v, present)
	}

	// Explicit null on required field without default -> error.
	row = goodRow()
	row["name"] = nil
	if _, err := c.ConvertRow(row); err == nil ||
		!strings.Contains(err.Error(), "null not allowed") {
		t.Errorf("expected null-not-allowed error, got %v", err)
	}

	// Missing required field -> error (distinct from null).
	row = goodRow()
	delete(row, "name")
	if _, err := c.ConvertRow(row); err == nil ||
		!strings.Contains(err.Error(), "required field missing") {
		t.Errorf("expected missing-required error, got %v", err)
	}

	// Missing field with default -> default is used.
	row = goodRow()
	delete(row, "nick")
	out, err = c.ConvertRow(row)
	if err != nil {
		t.Fatal(err)
	}
	if out["nick"].Str != "anonymous" {
		t.Errorf("nick = %v, want default", out["nick"])
	}
}

func TestRowConversionIsAtomic(t *testing.T) {
	c := newTestConverter(t, Reject)
	row := goodRow()
	row["id"] = intVal(5000000000) // overflows int32; other fields are fine
	out, err := c.ConvertRow(row)
	if err == nil {
		t.Fatal("expected error")
	}
	if out != nil {
		t.Errorf("failed row must not leak partial output, got %v", out)
	}
}

func TestBatchPreservesOrder(t *testing.T) {
	c := newTestConverter(t, Reject)
	bad := goodRow()
	bad["id"] = intVal(5000000000)
	rows := []Row{goodRow(), bad, goodRow(), bad}
	results := c.ConvertBatch(rows)
	if len(results) != 4 {
		t.Fatalf("got %d results, want 4", len(results))
	}
	wantOK := []bool{true, false, true, false}
	for i, res := range results {
		if res.Index != i {
			t.Errorf("result %d has index %d", i, res.Index)
		}
		if ok := res.Err == nil; ok != wantOK[i] {
			t.Errorf("result %d ok=%v, want %v (err=%v)", i, ok, wantOK[i], res.Err)
		}
		if res.Err != nil && res.Row != nil {
			t.Errorf("result %d: failed row leaked partial output", i)
		}
	}
}

func TestDecodeRowNullVsMissing(t *testing.T) {
	src, _ := testSchemas()
	row, err := DecodeRow(src, map[string]json.RawMessage{
		"id":   json.RawMessage("7"),
		"nick": json.RawMessage("null"),
	}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	v, present := row["nick"]
	if !present || v != nil {
		t.Error("JSON null must decode to explicit null (present, nil)")
	}
	if _, present := row["name"]; present {
		t.Error("absent key must stay missing")
	}
	if row["id"].Int != 7 {
		t.Errorf("id = %v", row["id"].Int)
	}
}

func TestSchemaJSONRoundTrip(t *testing.T) {
	const doc = `{"fields":[
		{"name":"ts","type":"timestamp","required":true,"default":1700000000000000},
		{"name":"price","type":"decimal","scale":2,"default":{"coefficient":"0","scale":2}}
	]}`
	var s Schema
	if err := json.Unmarshal([]byte(doc), &s); err != nil {
		t.Fatal(err)
	}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	if s.Fields[0].Default.Micros != 1700000000000000 {
		t.Errorf("ts default = %v", s.Fields[0].Default.Micros)
	}
	if s.Fields[1].Default.Dec.Coefficient.Sign() != 0 || s.Fields[1].Default.Dec.Scale != 2 {
		t.Errorf("price default = %v", s.Fields[1].Default.Dec)
	}
}

func TestMappingValidation(t *testing.T) {
	src, tgt := testSchemas()
	if _, err := NewConverter(src, tgt, map[string]string{"nope": "id"}, Options{}); err == nil {
		t.Error("unknown source field should be rejected")
	}
	if _, err := NewConverter(src, tgt, map[string]string{"id": "nope"}, Options{}); err == nil {
		t.Error("unknown target field should be rejected")
	}
	if _, err := NewConverter(src, tgt, map[string]string{"id": "price"}, Options{}); err == nil {
		t.Error("int -> decimal mapping should be rejected")
	}
	if _, err := NewConverter(src, tgt, testMapping(), Options{Rounding: "floor"}); err == nil {
		t.Error("unknown rounding mode should be rejected")
	}
}
