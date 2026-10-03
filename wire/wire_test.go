package wire

import (
	"bytes"
	"strings"
	"testing"
)

func TestVarintRoundTrip(t *testing.T) {
	cases := []uint64{0, 1, 127, 128, 300, 1<<21 - 1, 1 << 21, 1<<32 - 1, 1 << 32, 1<<63 - 1, 1<<64 - 1}
	for _, want := range cases {
		buf := AppendVarint(nil, want)
		got, n, err := ConsumeVarint(buf)
		if err != nil {
			t.Fatalf("ConsumeVarint(%d): %v", want, err)
		}
		if got != want || n != len(buf) {
			t.Fatalf("round trip %d: got %d, n=%d, len=%d", want, got, n, len(buf))
		}
		if VarintLen(want) != len(buf) {
			t.Fatalf("VarintLen(%d)=%d, encoded=%d", want, VarintLen(want), len(buf))
		}
	}
}

func TestVarintKnownEncodings(t *testing.T) {
	// 1 -> 0x01, 300 -> 0xAC 0x02, 150 -> 0x96 0x01 (canonical protobuf examples)
	if got := AppendVarint(nil, 1); !bytes.Equal(got, []byte{0x01}) {
		t.Fatalf("1 encoded as %x", got)
	}
	if got := AppendVarint(nil, 300); !bytes.Equal(got, []byte{0xAC, 0x02}) {
		t.Fatalf("300 encoded as %x", got)
	}
	if got := AppendVarint(nil, 150); !bytes.Equal(got, []byte{0x96, 0x01}) {
		t.Fatalf("150 encoded as %x", got)
	}
}

func TestVarintTruncated(t *testing.T) {
	// 0x80 promises a continuation byte that never arrives.
	if _, _, err := ConsumeVarint([]byte{0x80}); err != ErrTruncated {
		t.Fatalf("want ErrTruncated, got %v", err)
	}
	if _, _, err := ConsumeVarint(nil); err != ErrTruncated {
		t.Fatalf("empty input: want ErrTruncated, got %v", err)
	}
}

func TestVarintOverflow(t *testing.T) {
	// 10 continuation bytes + more: exceeds 64 bits.
	buf := bytes.Repeat([]byte{0xFF}, 10)
	buf = append(buf, 0x01)
	if _, _, err := ConsumeVarint(buf); err != ErrOverflow {
		t.Fatalf("want ErrOverflow, got %v", err)
	}
	// 10th byte carrying more than 1 bit.
	buf = bytes.Repeat([]byte{0x80}, 9)
	buf = append(buf, 0x02)
	if _, _, err := ConsumeVarint(buf); err != ErrOverflow {
		t.Fatalf("10th byte >1: want ErrOverflow, got %v", err)
	}
	// Exactly 10 bytes, 10th byte = 1: max uint64, legal.
	buf = bytes.Repeat([]byte{0xFF}, 9)
	buf = append(buf, 0x01)
	v, n, err := ConsumeVarint(buf)
	if err != nil || v != 1<<64-1 || n != 10 {
		t.Fatalf("max uint64: v=%d n=%d err=%v", v, n, err)
	}
}

func TestZigZag(t *testing.T) {
	cases := []struct {
		in   int64
		want uint64
	}{
		{0, 0}, {-1, 1}, {1, 2}, {-2, 3}, {2, 4},
		{2147483647, 4294967294}, {-2147483648, 4294967295},
		{1<<63 - 1, 1<<64 - 2}, {-1 << 63, 1<<64 - 1},
	}
	for _, c := range cases {
		if got := ZigZagEncode(c.in); got != c.want {
			t.Fatalf("ZigZagEncode(%d)=%d, want %d", c.in, got, c.want)
		}
		if got := ZigZagDecode(c.want); got != c.in {
			t.Fatalf("ZigZagDecode(%d)=%d, want %d", c.want, got, c.in)
		}
	}
}

func TestFixedRoundTrip(t *testing.T) {
	b32 := AppendFixed32(nil, 0xDEADBEEF)
	v32, n, err := ConsumeFixed32(b32)
	if err != nil || v32 != 0xDEADBEEF || n != 4 {
		t.Fatalf("fixed32: %x n=%d err=%v", v32, n, err)
	}
	b64 := AppendFixed64(nil, 0x0102030405060708)
	v64, n, err := ConsumeFixed64(b64)
	if err != nil || v64 != 0x0102030405060708 || n != 8 {
		t.Fatalf("fixed64: %x n=%d err=%v", v64, n, err)
	}
	if _, _, err := ConsumeFixed32([]byte{1, 2, 3}); err != ErrTruncated {
		t.Fatalf("short fixed32: want ErrTruncated, got %v", err)
	}
	if _, _, err := ConsumeFixed64([]byte{1, 2, 3, 4, 5, 6, 7}); err != ErrTruncated {
		t.Fatalf("short fixed64: want ErrTruncated, got %v", err)
	}
}

func TestParseMessageMixed(t *testing.T) {
	var buf []byte
	buf = AppendVarint(buf, Key(1, WireVarint))
	buf = AppendVarint(buf, 150)
	buf = AppendVarint(buf, Key(2, WireBytes))
	buf = AppendVarint(buf, 3)
	buf = append(buf, "abc"...)
	buf = AppendVarint(buf, Key(3, WireFixed32))
	buf = AppendFixed32(buf, 42)
	buf = AppendVarint(buf, Key(4, WireFixed64))
	buf = AppendFixed64(buf, 1<<60)

	fields, err := ParseMessage(buf)
	if err != nil {
		t.Fatal(err)
	}
	if len(fields) != 4 {
		t.Fatalf("got %d fields", len(fields))
	}
	if fields[0].Number != 1 || fields[0].Varint != 150 {
		t.Fatalf("field 1: %+v", fields[0])
	}
	if fields[1].Number != 2 || string(fields[1].Bytes) != "abc" {
		t.Fatalf("field 2: %+v", fields[1])
	}
	if fields[2].Number != 3 || fields[2].Fixed32 != 42 {
		t.Fatalf("field 3: %+v", fields[2])
	}
	if fields[3].Number != 4 || fields[3].Fixed64 != 1<<60 {
		t.Fatalf("field 4: %+v", fields[3])
	}
}

// TestParseMessageTruncationSafety feeds a valid message and then every
// possible truncation of it; the parser must never panic and must always
// report a bounded error (never silently succeed on cut data).
func TestParseMessageTruncationSafety(t *testing.T) {
	var buf []byte
	buf = AppendVarint(buf, Key(1, WireVarint))
	buf = AppendVarint(buf, 300)
	buf = AppendVarint(buf, Key(2, WireBytes))
	buf = AppendVarint(buf, 5)
	buf = append(buf, "hello"...)
	buf = AppendVarint(buf, Key(3, WireFixed64))
	buf = AppendFixed64(buf, 99)

	if _, err := ParseMessage(buf); err != nil {
		t.Fatalf("full message should parse: %v", err)
	}
	boundaries := fieldBoundaries(buf)
	for cut := 1; cut < len(buf); cut++ { // cut=0 is the empty message: valid
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("panic on truncation to %d bytes: %v", cut, r)
				}
			}()
			fields, err := ParseMessage(buf[:cut])
			if boundaries[cut] {
				if err != nil {
					t.Fatalf("cut=%d on field boundary should parse, got %v", cut, err)
				}
			} else if err == nil {
				t.Fatalf("cut=%d inside a field unexpectedly succeeded (%d fields)", cut, len(fields))
			}
		}()
	}
}

func TestParseMessageLengthOverflow(t *testing.T) {
	// Declares 100 bytes of payload, provides 2.
	var buf []byte
	buf = AppendVarint(buf, Key(7, WireBytes))
	buf = AppendVarint(buf, 100)
	buf = append(buf, 0xAA, 0xBB)
	_, err := ParseMessage(buf)
	fe, ok := err.(*FieldError)
	if !ok || fe.Field != 7 || fe.Err != ErrLengthOverflow {
		t.Fatalf("want FieldError{7, ErrLengthOverflow}, got %v", err)
	}
}

func TestParseMessageInvalidWireType(t *testing.T) {
	buf := AppendVarint(nil, Key(1, WireType(3))) // group start: unsupported
	if _, err := ParseMessage(buf); err == nil || !strings.Contains(err.Error(), "invalid wire type") {
		t.Fatalf("want invalid wire type error, got %v", err)
	}
	buf = AppendVarint(nil, Key(0, WireVarint)) // field number 0: illegal
	if _, err := ParseMessage(buf); err == nil || !strings.Contains(err.Error(), "invalid field number") {
		t.Fatalf("want invalid field number error, got %v", err)
	}
}
