package wire

import (
	"errors"
	"math"
	"testing"
)

func TestVarintRoundTrip(t *testing.T) {
	cases := []uint64{0, 1, 127, 128, 300, 1<<21 - 1, 1 << 21, 1<<35 - 1, 1 << 35,
		math.MaxUint32, math.MaxUint64 - 1, math.MaxUint64}
	for _, want := range cases {
		b := NewBuffer()
		b.AppendVarint(want)
		got, err := NewReader(b.Bytes()).ReadVarint()
		if err != nil {
			t.Fatalf("ReadVarint(%d): %v", want, err)
		}
		if got != want {
			t.Errorf("varint round trip: got %d, want %d", got, want)
		}
	}
}

func TestVarintTruncation(t *testing.T) {
	b := NewBuffer()
	b.AppendVarint(math.MaxUint64) // 10 bytes
	full := b.Bytes()
	if len(full) != 10 {
		t.Fatalf("max uint64 varint should be 10 bytes, got %d", len(full))
	}
	for i := 0; i < len(full); i++ {
		if _, err := NewReader(full[:i]).ReadVarint(); !errors.Is(err, ErrTruncated) {
			t.Errorf("prefix len %d: got %v, want ErrTruncated", i, err)
		}
	}
}

func TestVarintOverflow(t *testing.T) {
	// 11 continuation bytes: exceeds the 10-byte varint limit.
	tooLong := make([]byte, 11)
	for i := range tooLong {
		tooLong[i] = 0x80
	}
	if _, err := NewReader(tooLong).ReadVarint(); !errors.Is(err, ErrOverflow) {
		t.Errorf("11-byte varint: got %v, want ErrOverflow", err)
	}
	// 10th byte carrying more than 1 payload bit.
	over := append([]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}, 0x02)
	if _, err := NewReader(over).ReadVarint(); !errors.Is(err, ErrOverflow) {
		t.Errorf("10th byte 0x02: got %v, want ErrOverflow", err)
	}
}

func TestZigZagRoundTrip(t *testing.T) {
	cases := []int64{0, -1, 1, -2, 2, math.MinInt32, math.MaxInt32, math.MinInt64, math.MaxInt64}
	for _, want := range cases {
		b := NewBuffer()
		b.AppendZigZag(want)
		got, err := NewReader(b.Bytes()).ReadZigZag()
		if err != nil {
			t.Fatalf("ReadZigZag(%d): %v", want, err)
		}
		if got != want {
			t.Errorf("zigzag round trip: got %d, want %d", got, want)
		}
	}
	// Known mapping from the protobuf spec: 0->0, -1->1, 1->2, -2->3.
	if EncodeZigZag(-1) != 1 || EncodeZigZag(1) != 2 || DecodeZigZag(3) != -2 {
		t.Error("zigzag mapping mismatch with protobuf spec")
	}
}

func TestFixedRoundTrip(t *testing.T) {
	b := NewBuffer()
	b.AppendFixed32(0xdeadbeef)
	b.AppendFixed64(0x0123456789abcdef)
	b.AppendFloat32(3.14)
	b.AppendFloat64(-2.718281828)
	r := NewReader(b.Bytes())
	if v, err := r.ReadFixed32(); err != nil || v != 0xdeadbeef {
		t.Errorf("fixed32: got %#x, err %v", v, err)
	}
	if v, err := r.ReadFixed64(); err != nil || v != 0x0123456789abcdef {
		t.Errorf("fixed64: got %#x, err %v", v, err)
	}
	if v, err := r.ReadFixed32(); err != nil || math.Float32frombits(v) != 3.14 {
		t.Errorf("float32: got %v, err %v", math.Float32frombits(v), err)
	}
	if v, err := r.ReadFixed64(); err != nil || math.Float64frombits(v) != -2.718281828 {
		t.Errorf("float64: got %v, err %v", math.Float64frombits(v), err)
	}
	if !r.Done() {
		t.Error("reader should be exhausted")
	}
}

func TestFixedTruncation(t *testing.T) {
	if _, err := NewReader([]byte{1, 2, 3}).ReadFixed32(); !errors.Is(err, ErrTruncated) {
		t.Errorf("short fixed32: got %v, want ErrTruncated", err)
	}
	if _, err := NewReader([]byte{1, 2, 3, 4, 5, 6, 7}).ReadFixed64(); !errors.Is(err, ErrTruncated) {
		t.Errorf("short fixed64: got %v, want ErrTruncated", err)
	}
}

func TestReadBytesLengthOverrun(t *testing.T) {
	b := NewBuffer()
	b.AppendVarint(100) // declares 100 bytes
	b.AppendBytes([]byte("short"))
	// AppendBytes added its own length prefix; craft manually instead.
	raw := append([]byte{100}, []byte("short")...)
	_ = b
	if _, err := NewReader(raw).ReadBytes(); !errors.Is(err, ErrTruncated) {
		t.Errorf("declared length exceeds buffer: got %v, want ErrTruncated", err)
	}
}

func TestReadTagValidation(t *testing.T) {
	// Field number 0 is illegal.
	if _, _, err := NewReader([]byte{0x00}).ReadTag(); !errors.Is(err, ErrInvalidTag) {
		t.Errorf("field number 0: got %v, want ErrInvalidTag", err)
	}
	// Wire type 4 (end-group) is unsupported.
	if _, _, err := NewReader([]byte{0x0c}).ReadTag(); !errors.Is(err, ErrInvalidTag) {
		t.Errorf("wire type 4: got %v, want ErrInvalidTag", err)
	}
	// Valid: field 1, varint.
	num, wt, err := NewReader([]byte{0x08}).ReadTag()
	if err != nil || num != 1 || wt != Varint {
		t.Errorf("tag 0x08: got (%d, %v, %v)", num, wt, err)
	}
}

func TestSkipUnknownFields(t *testing.T) {
	b := NewBuffer()
	b.AppendTag(1, Varint)
	b.AppendVarint(42)
	b.AppendTag(2, Fixed64)
	b.AppendFixed64(9)
	b.AppendTag(3, LengthDelim)
	b.AppendBytes([]byte("payload"))
	b.AppendTag(4, Fixed32)
	b.AppendFixed32(7)
	r := NewReader(b.Bytes())
	for !r.Done() {
		_, wt, err := r.ReadTag()
		if err != nil {
			t.Fatal(err)
		}
		if err := r.Skip(wt); err != nil {
			t.Fatal(err)
		}
	}
	if !r.Done() {
		t.Error("skip should consume the whole stream")
	}
}
