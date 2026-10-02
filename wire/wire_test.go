package wire

import (
	"math"
	"strings"
	"testing"
)

func TestVarintRoundTrip(t *testing.T) {
	vals := []uint64{0, 1, 127, 128, 300, 1<<21 - 1, 1 << 21, 1<<35 - 1, 1 << 35,
		math.MaxUint32, math.MaxUint64 - 1, math.MaxUint64}
	for _, want := range vals {
		buf := AppendVarint(nil, want)
		got, err := NewReader(buf).ReadVarint()
		if err != nil {
			t.Fatalf("ReadVarint(%d): %v", want, err)
		}
		if got != want {
			t.Errorf("varint round trip: got %d, want %d", got, want)
		}
	}
}

func TestVarintTruncated(t *testing.T) {
	// 0x80 promises a continuation byte that never arrives.
	_, err := NewReader([]byte{0x80}).ReadVarint()
	if CodeOf(err) != ErrTruncated {
		t.Fatalf("got %v, want code %s", err, ErrTruncated)
	}
	// Truncated mid-way through a multi-byte varint.
	_, err = NewReader([]byte{0xff, 0xff, 0x80}).ReadVarint()
	if CodeOf(err) != ErrTruncated {
		t.Fatalf("got %v, want code %s", err, ErrTruncated)
	}
}

func TestVarintOverflow(t *testing.T) {
	// 11 continuation bytes: longer than MaxVarintBytes.
	buf := []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x7f}
	_, err := NewReader(buf).ReadVarint()
	if CodeOf(err) != ErrOverflow {
		t.Fatalf("got %v, want code %s", err, ErrOverflow)
	}
	// 10th byte carrying payload bits above bit 63.
	buf = []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x02}
	_, err = NewReader(buf).ReadVarint()
	if CodeOf(err) != ErrOverflow {
		t.Fatalf("got %v, want code %s", err, ErrOverflow)
	}
	// Exactly 10 bytes with a legal 10th byte is fine (max uint64).
	buf = []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01}
	v, err := NewReader(buf).ReadVarint()
	if err != nil || v != math.MaxUint64 {
		t.Fatalf("max varint: got %d, err %v", v, err)
	}
}

func TestZigZag(t *testing.T) {
	cases := []struct {
		dec int64
		enc uint64
	}{
		{0, 0}, {-1, 1}, {1, 2}, {-2, 3}, {2, 4},
		{math.MaxInt64, math.MaxUint64 - 1},
		{math.MinInt64, math.MaxUint64},
	}
	for _, c := range cases {
		if got := EncodeZigZag64(c.dec); got != c.enc {
			t.Errorf("EncodeZigZag64(%d) = %d, want %d", c.dec, got, c.enc)
		}
		if got := ZigZag64(c.enc); got != c.dec {
			t.Errorf("ZigZag64(%d) = %d, want %d", c.enc, got, c.dec)
		}
	}
	if got := ZigZag32(1); got != -1 {
		t.Errorf("ZigZag32(1) = %d, want -1", got)
	}
}

func TestFixedRoundTrip(t *testing.T) {
	r := NewReader(AppendFixed32(nil, 0xdeadbeef))
	if v, err := r.ReadFixed32(); err != nil || v != 0xdeadbeef {
		t.Fatalf("fixed32: got %#x, err %v", v, err)
	}
	r = NewReader(AppendFixed64(nil, 0x0102030405060708))
	if v, err := r.ReadFixed64(); err != nil || v != 0x0102030405060708 {
		t.Fatalf("fixed64: got %#x, err %v", v, err)
	}
}

func TestFixedTruncated(t *testing.T) {
	if _, err := NewReader([]byte{1, 2, 3}).ReadFixed32(); CodeOf(err) != ErrTruncated {
		t.Fatalf("fixed32 short: %v", err)
	}
	if _, err := NewReader([]byte{1, 2, 3, 4, 5, 6, 7}).ReadFixed64(); CodeOf(err) != ErrTruncated {
		t.Fatalf("fixed64 short: %v", err)
	}
}

func TestLengthDelimitedBoundsGuard(t *testing.T) {
	// Declares 100 bytes, buffer holds 2.
	buf := append([]byte{100}, 0xaa, 0xbb)
	_, err := NewReader(buf).ReadLengthDelimited()
	if CodeOf(err) != ErrNegativeLength {
		t.Fatalf("got %v, want code %s", err, ErrNegativeLength)
	}
	// Zero-length payload is legal.
	buf = []byte{0}
	b, err := NewReader(buf).ReadLengthDelimited()
	if err != nil || len(b) != 0 {
		t.Fatalf("empty payload: %v %v", b, err)
	}
}

func TestReadKeyValidation(t *testing.T) {
	// Wire types 3, 4, 6, 7 are reserved/invalid.
	for _, wt := range []byte{3, 4, 6, 7} {
		_, _, err := NewReader([]byte{1<<3 | wt}).ReadKey()
		if CodeOf(err) != ErrInvalidWire {
			t.Fatalf("wire type %d: got %v, want %s", wt, err, ErrInvalidWire)
		}
	}
	// Field number 0 is illegal.
	_, _, err := NewReader([]byte{0x00}).ReadKey()
	if CodeOf(err) != ErrBadFieldNumber {
		t.Fatalf("field 0: got %v, want %s", err, ErrBadFieldNumber)
	}
	// Valid key: field 15, fixed32.
	num, wt, err := NewReader([]byte{15<<3 | 5}).ReadKey()
	if err != nil || num != 15 || wt != WireFixed32 {
		t.Fatalf("valid key: %d %v %v", num, wt, err)
	}
}

func TestDecodeMessageNestedAndPacked(t *testing.T) {
	inner := NewEncoder().Varint(1, 42).String(2, "deep")
	mid := NewEncoder().Message(1, inner).ZigZag(2, -7)
	top := NewEncoder().
		Varint(1, 1).
		Message(2, mid).
		PackedVarints(3, 1, 2, 300, 4).
		PackedFixed32(4, 10, 20, 30).
		Fixed64(5, 0xfeedfacecafebeef)

	m, err := DecodeMessage(top.Bytes(), DefaultMaxDepth)
	if err != nil {
		t.Fatalf("DecodeMessage: %v", err)
	}
	if got := m.ByNumber(1)[0].U64; got != 1 {
		t.Errorf("field 1: got %d", got)
	}
	midF := m.ByNumber(2)[0]
	if midF.Message == nil {
		t.Fatal("field 2: nested message not detected")
	}
	innerF := midF.Message.ByNumber(1)[0]
	if innerF.Message == nil {
		t.Fatal("field 2.1: doubly nested message not detected")
	}
	if got := innerF.Message.ByNumber(1)[0].U64; got != 42 {
		t.Errorf("field 2.1.1: got %d", got)
	}
	if got := string(innerF.Message.ByNumber(2)[0].Bytes); got != "deep" {
		t.Errorf("field 2.1.2: got %q", got)
	}
	if got := ZigZag64(midF.Message.ByNumber(2)[0].U64); got != -7 {
		t.Errorf("field 2.2: got %d", got)
	}
	pv, err := m.ByNumber(3)[0].PackedVarints()
	if err != nil || len(pv) != 4 || pv[2] != 300 {
		t.Errorf("packed varints: %v %v", pv, err)
	}
	pf, err := m.ByNumber(4)[0].PackedFixed32()
	if err != nil || len(pf) != 3 || pf[1] != 20 {
		t.Errorf("packed fixed32: %v %v", pf, err)
	}
	if got := m.ByNumber(5)[0].U64F; got != 0xfeedfacecafebeef {
		t.Errorf("field 5: got %#x", got)
	}
}

func TestDecodeMessageDepthLimit(t *testing.T) {
	// Build a message nested 8 levels deep.
	enc := NewEncoder().Varint(1, 9)
	for i := 0; i < 7; i++ {
		enc = NewEncoder().Message(1, enc)
	}
	if _, err := DecodeMessage(enc.Bytes(), 3); CodeOf(err) != ErrDepthLimit {
		t.Fatalf("got %v, want code %s", err, ErrDepthLimit)
	}
	if _, err := DecodeMessage(enc.Bytes(), DefaultMaxDepth); err != nil {
		t.Fatalf("default depth should suffice: %v", err)
	}
}

func TestDecodeMessageTruncatedField(t *testing.T) {
	// A valid message, then a key promising a fixed64 with only 3 bytes left.
	good := NewEncoder().Varint(1, 7).Bytes()
	bad := append(good, byte(2<<3|int(WireFixed64)), 0x01, 0x02, 0x03)
	_, err := DecodeMessage(bad, DefaultMaxDepth)
	if CodeOf(err) != ErrTruncated {
		t.Fatalf("got %v, want code %s", err, ErrTruncated)
	}
}

func TestMidFieldTruncation(t *testing.T) {
	// key(1,string) len=5 "hello" key(2,varint) 1: cut inside the string.
	full := NewEncoder().String(1, "hello").Varint(2, 1).Bytes()
	_, err := DecodeMessage(full[:4], DefaultMaxDepth)
	if CodeOf(err) != ErrNegativeLength {
		t.Fatalf("got %v, want code %s", err, ErrNegativeLength)
	}
	var out struct {
		S string `pb:"1,string"`
		N uint64 `pb:"2,varint"`
	}
	if err := Unmarshal(full[:4], &out); CodeOf(err) != ErrNegativeLength {
		t.Fatalf("unmarshal: got %v, want code %s", err, ErrNegativeLength)
	}
}

func TestEveryPrefixFailsSafely(t *testing.T) {
	full := NewEncoder().
		Varint(1, 12345).
		String(2, "hello world").
		Fixed64(3, 99).
		Message(4, NewEncoder().ZigZag(1, -3).PackedVarints(2, 5, 6, 7)).
		Bytes()
	// A prefix ending exactly on a field boundary is itself a valid
	// message, so decoding may succeed; it must never panic and any
	// failure must be a classified wire error.
	for i := 0; i < len(full); i++ {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("prefix %d/%d panicked: %v", i, len(full), r)
				}
			}()
			_, err := DecodeMessage(full[:i], DefaultMaxDepth)
			if err == nil {
				return
			}
			switch CodeOf(err) {
			case ErrTruncated, ErrNegativeLength, ErrInvalidWire, ErrBadFieldNumber, ErrOverflow:
			default:
				t.Fatalf("prefix %d: unexpected code %s", i, CodeOf(err))
			}
		}()
	}
}

// ---- reflection engine ----

type innerMsg struct {
	ID   int64  `pb:"1,zigzag"`
	Name string `pb:"2,string"`
}

type outerMsg struct {
	Seq     uint64     `pb:"1,varint"`
	Delta   int64      `pb:"2,zigzag"`
	Flag    bool       `pb:"3,bool"`
	Ratio   float64    `pb:"4,fixed64"`
	Tag     uint32     `pb:"5,fixed32"`
	Label   string     `pb:"6,string"`
	Raw     []byte     `pb:"7,bytes"`
	Inner   *innerMsg  `pb:"8,msg"`
	Items   []innerMsg `pb:"9,msg"`
	Numbers []int32    `pb:"10,packed"`
}

func encodeOuter() []byte {
	mkInner := func(id int64, name string) *Encoder {
		return NewEncoder().ZigZag(1, id).String(2, name)
	}
	return NewEncoder().
		Varint(1, 777).
		ZigZag(2, -123456).
		Varint(3, 1).
		Fixed64(4, math.Float64bits(3.5)).
		Fixed32(5, 0xabc123).
		String(6, "root").
		ByteField(7, []byte{0xde, 0xad}).
		Message(8, mkInner(-1, "ptr")).
		Message(9, mkInner(1, "a")).
		Message(9, mkInner(2, "b")).
		PackedVarints(10,
			EncodeZigZag64(-5), EncodeZigZag64(0), EncodeZigZag64(5)).
		Bytes()
}

func TestUnmarshalReflection(t *testing.T) {
	var out outerMsg
	if err := Unmarshal(encodeOuter(), &out); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if out.Seq != 777 || out.Delta != -123456 || !out.Flag {
		t.Errorf("scalars: %+v", out)
	}
	if out.Ratio != 3.5 || out.Tag != 0xabc123 {
		t.Errorf("fixed: %v %#x", out.Ratio, out.Tag)
	}
	if out.Label != "root" || string(out.Raw) != "\xde\xad" {
		t.Errorf("bytes/string: %q %x", out.Label, out.Raw)
	}
	if out.Inner == nil || out.Inner.ID != -1 || out.Inner.Name != "ptr" {
		t.Errorf("nested ptr: %+v", out.Inner)
	}
	if len(out.Items) != 2 || out.Items[0].Name != "a" || out.Items[1].ID != 2 {
		t.Errorf("repeated msg: %+v", out.Items)
	}
	if len(out.Numbers) != 3 || out.Numbers[0] != -5 || out.Numbers[2] != 5 {
		t.Errorf("packed: %v", out.Numbers)
	}
}

func TestUnmarshalSkipsUnknownFields(t *testing.T) {
	buf := NewEncoder().
		Varint(1, 5).        // known
		Varint(50, 999).     // unknown varint
		String(51, "ghost"). // unknown length-delimited
		Fixed32(52, 1).      // unknown fixed32
		Fixed64(53, 2).      // unknown fixed64
		Bytes()
	var out struct {
		Seq uint64 `pb:"1,varint"`
	}
	if err := Unmarshal(buf, &out); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if out.Seq != 5 {
		t.Errorf("Seq = %d, want 5", out.Seq)
	}
}

func TestUnmarshalTypeMismatch(t *testing.T) {
	// Field 1 declared as fixed32 in the tag but sent as varint.
	buf := NewEncoder().Varint(1, 5).Bytes()
	var out struct {
		Seq uint32 `pb:"1,fixed32"`
	}
	if err := Unmarshal(buf, &out); CodeOf(err) != ErrTypeMismatch {
		t.Fatalf("got %v, want code %s", err, ErrTypeMismatch)
	}
}

func TestUnmarshalBadTargets(t *testing.T) {
	var notPtr struct{}
	if err := Unmarshal(nil, notPtr); CodeOf(err) != ErrUnsupported {
		t.Fatalf("non-pointer: got %v", err)
	}
	if err := Unmarshal(nil, nil); CodeOf(err) != ErrUnsupported {
		t.Fatalf("nil: got %v", err)
	}
	var badTag struct {
		X uint64 `pb:"0,varint"`
	}
	if err := Unmarshal(nil, &badTag); CodeOf(err) != ErrUnsupported {
		t.Fatalf("bad tag: got %v", err)
	}
}

func TestUnmarshalTruncatedNested(t *testing.T) {
	full := encodeOuter()
	for i := len(full) - 1; i >= 0; i-- {
		var out outerMsg
		err := Unmarshal(full[:i], &out)
		if err == nil {
			continue // prefix ended on a field boundary: valid message
		}
		if !strings.Contains(err.Error(), "wire:") {
			t.Fatalf("prefix %d: foreign error %v", i, err)
		}
	}
}
