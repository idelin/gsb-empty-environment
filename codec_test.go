package lpc

import (
	"encoding/binary"
	"testing"
)

func TestEncodeDecodeRoundTrip(t *testing.T) {
	weights := []uint64{100, 1, 1, 1, 9}
	rep, err := Build(weights, 3)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	c := rep.Code
	msgs := [][]int{
		{},
		{0},
		{0, 0, 0, 1, 4, 2, 3, 0, 4},
		{1, 2, 3, 1, 2, 3, 3, 2, 1, 0},
	}
	for _, msg := range msgs {
		data, err := c.Encode(msg)
		if err != nil {
			t.Fatalf("Encode: %v", err)
		}
		got, err := c.Decode(data, len(msg))
		if err != nil {
			t.Fatalf("Decode: %v", err)
		}
		if !intsEqual(got, msg) {
			t.Fatalf("round trip = %v, want %v", got, msg)
		}
	}
}

func TestDecodeQuotaBounded(t *testing.T) {
	rep, err := Build([]uint64{10, 20, 30, 40}, 2)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	msg := []int{0, 1, 2, 3, 0, 1}
	data, err := rep.Code.Encode(msg)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if _, err := rep.Code.Decode(data, 5); err != ErrOutputLimit {
		t.Fatalf("quota 5: err = %v, want ErrOutputLimit", err)
	}
	got, err := rep.Code.Decode(data, 6)
	if err != nil {
		t.Fatalf("quota 6: %v", err)
	}
	if !intsEqual(got, msg) {
		t.Fatalf("got %v", got)
	}
	// Negative quota is invalid.
	if _, err := rep.Code.Decode(data, -1); err != ErrBadArgument {
		t.Fatalf("negative quota: %v", err)
	}
}

func TestMarshalRoundTripAndFields(t *testing.T) {
	rep, err := Build([]uint64{1, 2, 3, 4, 5}, 3)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	data, err := rep.Code.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(data[0:4]) != Magic || data[4] != FormatVersion || int(data[5]) != 3 {
		t.Fatalf("bad fixed fields: % x", data[:8])
	}
	c2, err := UnmarshalCode(data)
	if err != nil {
		t.Fatalf("UnmarshalCode: %v", err)
	}
	if !intsEqual(c2.Lengths, rep.Code.Lengths) {
		t.Fatalf("lengths %v vs %v", c2.Lengths, rep.Code.Lengths)
	}
	if c2.MaxLen != 3 {
		t.Fatalf("MaxLen = %d", c2.MaxLen)
	}
}

func TestCorruptionDetected(t *testing.T) {
	rep, err := Build([]uint64{1, 2, 3, 4}, 2)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	data, err := rep.Code.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	// Flip a payload bit of the length table.
	bad := append([]byte(nil), data...)
	bad[12] ^= 0x01
	if _, err := UnmarshalCode(bad); err != ErrChecksum && err != ErrBadHeader {
		t.Fatalf("corrupt length byte: %v", err)
	}

	// Flip just the stored checksum.
	bad = append([]byte(nil), data...)
	bad[len(bad)-1] ^= 0xff
	if _, err := UnmarshalCode(bad); err != ErrChecksum {
		t.Fatalf("corrupt checksum: %v", err)
	}

	// Wrong magic.
	bad = append([]byte(nil), data...)
	bad[0] = 'X'
	if _, err := UnmarshalCode(bad); err != ErrBadHeader {
		t.Fatalf("bad magic: %v", err)
	}

	// Wrong version.
	bad = append([]byte(nil), data...)
	bad[4] = 99
	if _, err := UnmarshalCode(bad); err != ErrBadHeader {
		t.Fatalf("bad version: %v", err)
	}

	// Reserved byte non-zero.
	bad = append([]byte(nil), data...)
	bad[6] = 1
	if _, err := UnmarshalCode(bad); err != ErrBadHeader {
		t.Fatalf("reserved: %v", err)
	}

	// Truncation.
	if _, err := UnmarshalCode(data[:len(data)-1]); err != ErrBadHeader {
		t.Fatalf("truncated: %v", err)
	}
}

func TestInvalidLengthTableRejected(t *testing.T) {
	// 4 symbols, L=2, but lengths {1,2,2,2} violate Kraft (1.25) and
	// would collide as code words.
	n := 4
	raw := make([]byte, 11+n+4)
	copy(raw[0:4], Magic)
	raw[4] = FormatVersion
	raw[5] = 2
	binary.BigEndian.PutUint32(raw[7:11], uint32(n))
	raw[11] = 1
	raw[12] = 2
	raw[13] = 2
	raw[14] = 2
	sum := crc32IEEE(raw[:11+n])
	binary.BigEndian.PutUint32(raw[11+n:], sum)
	if _, err := UnmarshalCode(raw); err != ErrBadHeader {
		t.Fatalf("invalid Kraft table: err = %v", err)
	}
}

func TestFrameCorruptionAndPadding(t *testing.T) {
	rep, err := Build([]uint64{10, 20, 30, 40, 50}, 3)
	if err != nil {
		t.Fatal(err)
	}
	msg := []int{0, 1, 2, 3, 4}
	data, err := rep.Code.Encode(msg)
	if err != nil {
		t.Fatal(err)
	}

	// Flip a bit inside the packed payload; checksum must catch it; if the
	// flip lands in padding it is caught by the padding rule.
	bad := append([]byte(nil), data...)
	hdrLen := 11 + len(rep.Code.Lengths) + 4
	// flip the first payload bit
	bad[hdrLen+4] ^= 0x80
	if _, err := rep.Code.Decode(bad, 100); err == nil {
		t.Fatalf("payload bit flip not detected")
	}

	// Claimed count larger than actually encoded -> CRC or bitstream error.
	bad = append([]byte(nil), data...)
	binary.BigEndian.PutUint32(bad[hdrLen:hdrLen+4], uint32(len(msg)+1))
	if _, err := rep.Code.Decode(bad, 100); err == nil {
		t.Fatalf("inflated count not detected")
	}
}

func TestSingleSymbolCodec(t *testing.T) {
	rep, err := Build([]uint64{7}, 0)
	if err != nil {
		t.Fatal(err)
	}
	data, err := rep.Code.Encode([]int{0, 0, 0})
	if err != nil {
		t.Fatal(err)
	}
	got, err := rep.Code.Decode(data, 3)
	if err != nil {
		t.Fatal(err)
	}
	if !intsEqual(got, []int{0, 0, 0}) {
		t.Fatalf("got %v", got)
	}
}
