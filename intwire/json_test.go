package intwire

import (
	"bytes"
	"io"
	"math/big"
	"strings"
	"testing"
)

func TestJSONEnvelopeRoundTrip(t *testing.T) {
	vals := []Value{
		mustU(t, 8, 255),
		mustS(t, 16, -32768),
		mustU(t, 64, ^uint64(0)),
		mustS(t, 64, -9223372036854775808),
		NewBig(big.NewInt(-1234567890123456789)),
	}
	for _, v := range vals {
		raw, err := v.MarshalJSON()
		if err != nil {
			t.Fatal(err)
		}
		var got Value
		if err := got.UnmarshalJSON(raw); err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
		if !got.Equal(v) {
			t.Fatalf("got %v want %v", got.Decimal(), v.Decimal())
		}
	}
}

// Values beyond the 53-bit mantissa of a float64 must survive the JSON
// bridge exactly, because the exact value travels as a string tag.
func TestJSON53BitBoundary(t *testing.T) {
	for _, u := range []uint64{1<<53 - 1, 1 << 53, 1<<53 + 1, 1<<53 + 2, ^uint64(0)} {
		v := mustU(t, 64, u)
		raw, err := v.MarshalJSON()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), `"v":"`) {
			t.Fatalf("value not string-tagged: %s", raw)
		}
		var got Value
		if err := got.UnmarshalJSON(raw); err != nil {
			t.Fatal(err)
		}
		if got.U != u {
			t.Fatalf("53-bit loss: got %d want %d", got.U, u)
		}
	}
}

// Raw lexeme, exact value and width constraint are preserved separately.
func TestRawLexemePreserved(t *testing.T) {
	v, err := ParseJSONValue([]byte(`1e400`))
	if err != nil {
		t.Fatal(err)
	}
	if v.Raw != "1e400" {
		t.Fatalf("raw lexeme lost: %q", v.Raw)
	}
	ref, ok := new(big.Int).SetString("1"+strings.Repeat("0", 400), 10)
	if !ok {
		t.Fatal("ref")
	}
	if v.B.Cmp(ref) != 0 {
		t.Fatal("exact value mismatch")
	}
	if v.Width != 0 || v.Kind != KindBig {
		t.Fatal("width constraint wrong")
	}
}

// A huge-exponent lexeme is classified as integer and checked against the
// quota BEFORE any big arithmetic — and never through a host float.
func TestHugeExponentQuota(t *testing.T) {
	v, err := ParseJSONValue([]byte(`1e400`)) // within quota
	if err != nil {
		t.Fatal(err)
	}
	if v.B.BitLen() == 0 {
		t.Fatal("empty")
	}
	if _, err := ParseJSONValue([]byte(`1e999999999`)); err == nil { // way over quota
		t.Fatal("oversize exponent accepted")
	}
	if v, err := ParseJSONValue([]byte(`2.5e3`)); err != nil || v.B.Int64() != 2500 {
		t.Fatalf("2.5e3 = 2500 must parse: %v %v", v.Decimal(), err)
	}
}

func TestNonIntegerLexemeRejected(t *testing.T) {
	for _, s := range []string{`1.5`, `1e-3`, `3.141e2`, `-0.001`, `1.00001e4`} {
		if _, err := ParseJSONValue([]byte(s)); err == nil {
			t.Fatalf("%s accepted as integer", s)
		}
	}
	for _, s := range []string{`1.0`, `2.50e1`, `100e-2`, `-4e0`, `0e-999999999`} {
		v, err := ParseJSONValue([]byte(s))
		if err != nil {
			t.Fatalf("%s: %v", s, err)
		}
		if v.B == nil {
			t.Fatalf("%s: nil value", s)
		}
	}
}

// Streaming bridge: a sequence of envelopes and bare numbers over one
// stream, decoded without any float64 materialization.
func TestStreamBridge(t *testing.T) {
	var buf bytes.Buffer
	enc := NewStreamEncoder(&buf)
	vals := []Value{
		mustU(t, 64, ^uint64(0)),
		mustS(t, 8, -5),
		NewBig(new(big.Int).Lsh(big.NewInt(1), 200)),
	}
	for _, v := range vals {
		if err := enc.WriteValue(v); err != nil {
			t.Fatal(err)
		}
	}
	buf.WriteString("123456789012345678901234567890\n") // bare number lexeme
	buf.WriteString("\"-77\"\n")                        // quoted lexeme

	dec := NewStreamDecoder(&buf)
	for i, want := range vals {
		got, err := dec.ReadValue()
		if err != nil {
			t.Fatalf("value %d: %v", i, err)
		}
		if !got.Equal(want) {
			t.Fatalf("value %d: got %v want %v", i, got.Decimal(), want.Decimal())
		}
	}
	bare, err := dec.ReadValue()
	if err != nil || bare.B.String() != "123456789012345678901234567890" {
		t.Fatalf("bare number: %v %v", bare.Decimal(), err)
	}
	quoted, err := dec.ReadValue()
	if err != nil || quoted.B.Int64() != -77 {
		t.Fatalf("quoted: %v %v", quoted.Decimal(), err)
	}
	if _, err := dec.ReadValue(); err != io.EOF {
		t.Fatalf("want EOF, got %v", err)
	}
}

func TestJSONNegativeIntoUnsignedRejected(t *testing.T) {
	var v Value
	if err := v.UnmarshalJSON([]byte(`{"k":"u","w":8,"v":"-1"}`)); err == nil {
		t.Fatal("negative accepted into unsigned field")
	}
	if err := v.UnmarshalJSON([]byte(`{"k":"u","w":8,"v":"256"}`)); err == nil {
		t.Fatal("overflow accepted into u8 field")
	}
}

func TestJSONMalformedRejected(t *testing.T) {
	for _, s := range []string{
		`{"k":"u","w":8}`,               // missing value
		`{"k":"x","w":8,"v":"1"}`,       // unknown kind
		`{"k":"big","w":8,"v":"1"}`,     // big with width
		`{"k":"u","w":7,"v":"1"}`,       // unsupported width
		`{"k":"u","w":8,"v":"1","x":1}`, // unknown field
		`{"k":"u","w":8,"v":""}`,        // empty value
		`{"k":"u","w":8,"v":"12a"}`,     // bad digits
	} {
		var v Value
		if err := v.UnmarshalJSON([]byte(s)); err == nil {
			t.Fatalf("%s accepted", s)
		}
	}
}
