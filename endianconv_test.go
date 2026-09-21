package endianconv

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"math/rand/v2"
	"testing"
)

var (
	be binary.ByteOrder = binary.BigEndian
	le binary.ByteOrder = binary.LittleEndian
)

// buildRecord encodes values into one record per schema, in order ord.
func buildRecord(s Schema, ord binary.ByteOrder, vals ...any) []byte {
	var buf bytes.Buffer
	for i, f := range s.Fields {
		switch f.Kind {
		case KindU8:
			buf.WriteByte(vals[i].(byte))
		case KindU16:
			var b [2]byte
			ord.PutUint16(b[:], vals[i].(uint16))
			buf.Write(b[:])
		case KindU32:
			var b [4]byte
			ord.PutUint32(b[:], vals[i].(uint32))
			buf.Write(b[:])
		case KindU64:
			var b [8]byte
			ord.PutUint64(b[:], vals[i].(uint64))
			buf.Write(b[:])
		case KindF32:
			var b [4]byte
			ord.PutUint32(b[:], math.Float32bits(vals[i].(float32)))
			buf.Write(b[:])
		case KindF64:
			var b [8]byte
			ord.PutUint64(b[:], math.Float64bits(vals[i].(float64)))
			buf.Write(b[:])
		case KindBytes:
			b := vals[i].([]byte)
			if len(b) != f.Size {
				panic("bad Bytes size")
			}
			buf.Write(b)
		case KindVarBytes:
			p := vals[i].([]byte)
			var h [8]byte
			putUint(h[:f.Size], f.Size, ord, uint64(len(p)))
			buf.Write(h[:f.Size])
			buf.Write(p)
		}
	}
	return buf.Bytes()
}

func TestRoundTripRestoresAllBytes(t *testing.T) {
	s := Schema{Fields: []Field{
		U16(), U32(), F64(), Bytes(3), VarBytes(2, 1024), U8(), F32(), VarBytes(1, 255),
	}}
	payload1 := []byte{0xDE, 0xAD, 0xBE, 0xEF, 0x00, 0x01}
	payload2 := []byte{0x7F, 0x80, 0xFF}
	rec := buildRecord(s, be,
		uint16(0x1234), uint32(0xCAFEBABE), math.Float64frombits(0x7FF4000000000001),
		[]byte{9, 8, 7}, payload1, byte(0x42),
		math.Float32frombits(0x7FC00001), payload2)

	mid, err := s.ConvertRecord(rec, be, le, 0)
	if err != nil {
		t.Fatal(err)
	}
	back, err := s.ConvertRecord(mid, le, be, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(rec, back) {
		t.Fatalf("double conversion did not restore bytes:\n got %x\nwant %x", back, rec)
	}
	// And the LE encoding must actually differ where multi-byte fields are.
	if bytes.Equal(rec, mid) {
		t.Fatal("conversion was a no-op")
	}
}

func TestNaNPayloadBitsPreserved(t *testing.T) {
	nan64 := []uint64{
		0x7FF8000000000001, // quiet NaN with payload
		0x7FF0000000000001, // signaling NaN
		0xFFF80000000ABCDE, // negative quiet NaN with payload
		0x7FFFFFFFFFFFFFFF, // all payload bits set
	}
	nan32 := []uint32{
		0x7FC00001, 0x7F800001, 0xFFC12345, 0x7FFFFFFF,
	}
	s := Schema{Fields: []Field{F64(), F32()}}
	for _, n64 := range nan64 {
		for _, n32 := range nan32 {
			rec := buildRecord(s, be, math.Float64frombits(n64), math.Float32frombits(n32))
			mid, err := s.ConvertRecord(rec, be, le, 0)
			if err != nil {
				t.Fatal(err)
			}
			// Bits must be exactly reversed, not normalized through a float.
			if got := le.Uint64(mid[:8]); got != n64 {
				t.Fatalf("f64 bits changed: got %#x want %#x", got, n64)
			}
			if got := le.Uint32(mid[8:12]); got != n32 {
				t.Fatalf("f32 bits changed: got %#x want %#x", got, n32)
			}
			back, err := s.ConvertRecord(mid, le, be, 0)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(rec, back) {
				t.Fatalf("NaN bits lost in round trip: got %x want %x", back, rec)
			}
		}
	}
}

func TestVarBytesOnlyHeaderConverted(t *testing.T) {
	s := Schema{Fields: []Field{VarBytes(2, 1024)}}
	// Payload bytes deliberately look like multi-byte numbers; they must
	// pass through untouched.
	payload := []byte{0x00, 0x01, 0x02, 0x03, 0xFE, 0xFF}
	rec := buildRecord(s, be, payload)

	out, err := s.ConvertRecord(rec, be, le, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := le.Uint16(out[:2]); got != uint16(len(payload)) {
		t.Fatalf("header not converted: got %d", got)
	}
	if !bytes.Equal(out[2:], payload) {
		t.Fatalf("payload modified: got %x want %x", out[2:], payload)
	}
}

func TestInPlaceConversion(t *testing.T) {
	s := Schema{Fields: []Field{U16(), F64(), VarBytes(2, 64), U32()}}
	rec := buildRecord(s, be, uint16(0xBEEF), 3.14159, []byte{1, 2, 3, 4, 5}, uint32(0xDEADBEEF))
	want, err := s.ConvertRecord(rec, be, le, 0)
	if err != nil {
		t.Fatal(err)
	}
	buf := bytes.Clone(rec)
	n, err := s.Convert(buf, buf, be, le, 0) // exact same region: in-place
	if err != nil {
		t.Fatal(err)
	}
	if n != len(want) || !bytes.Equal(buf, want) {
		t.Fatalf("in-place result differs: got %x want %x", buf, want)
	}
	// In-place round trip restores everything.
	if _, err := s.Convert(buf, buf, le, be, 0); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(buf, rec) {
		t.Fatalf("in-place round trip failed: got %x want %x", buf, rec)
	}
}

func TestPartialOverlapRejected(t *testing.T) {
	s := Schema{Fields: []Field{U32(), U16()}}
	src := buildRecord(s, be, uint32(1), uint16(2))
	backing := make([]byte, len(src)+2)
	copy(backing, src)
	dst := backing[1 : len(src)+1] // shifted by one: partial overlap
	if _, err := s.Convert(dst, backing[:len(src)], be, le, 0); !errors.Is(err, ErrOverlap) {
		t.Fatalf("want ErrOverlap, got %v", err)
	}
	// Fully disjoint buffers are fine.
	out := make([]byte, len(src))
	if _, err := s.Convert(out, src, be, le, 0); err != nil {
		t.Fatal(err)
	}
}

func TestShortBuffer(t *testing.T) {
	s := Schema{Fields: []Field{U64()}}
	src := buildRecord(s, be, uint64(1))
	if _, err := s.Convert(make([]byte, 4), src, be, le, 0); !errors.Is(err, ErrShortBuffer) {
		t.Fatalf("want ErrShortBuffer, got %v", err)
	}
}

func TestDecoderArbitraryChunking(t *testing.T) {
	s := Schema{Fields: []Field{U16(), VarBytes(2, 100), F64()}}
	r1 := buildRecord(s, le, uint16(7), []byte("hello"), 2.5)
	r2 := buildRecord(s, le, uint16(8), []byte("world!!"), -0.25)
	stream := append(bytes.Clone(r1), r2...)

	for _, chunk := range []int{1, 2, 3, 5, 7, 64} {
		d := NewDecoder(s, le, 0)
		var got [][]byte
		for i := 0; i < len(stream); i += chunk {
			end := min(i+chunk, len(stream))
			d.Feed(stream[i:end])
			for {
				rec, err := d.Next()
				if errors.Is(err, ErrNeedMoreData) {
					break
				}
				if err != nil {
					t.Fatalf("chunk=%d: %v", chunk, err)
				}
				got = append(got, rec)
			}
		}
		if len(got) != 2 || !bytes.Equal(got[0], r1) || !bytes.Equal(got[1], r2) {
			t.Fatalf("chunk=%d: got %x", chunk, got)
		}
	}
}

func TestDecoderDistinguishesNeedMoreFromBadData(t *testing.T) {
	s := Schema{Fields: []Field{U16(), VarBytes(2, 10)}}

	// Incomplete but valid prefix -> ErrNeedMoreData.
	d := NewDecoder(s, le, 0)
	d.Feed([]byte{0x01}) // half of the U16
	if _, err := d.Next(); !errors.Is(err, ErrNeedMoreData) {
		t.Fatalf("want ErrNeedMoreData, got %v", err)
	}
	d.Feed([]byte{0x00, 0x05, 0x00}) // u16 done, header says 5 bytes, none yet
	if _, err := d.Next(); !errors.Is(err, ErrNeedMoreData) {
		t.Fatalf("want ErrNeedMoreData, got %v", err)
	}

	// Header declares more than the quota -> ErrRecordTooLarge, returned
	// as soon as the header arrives, without the payload.
	d2 := NewDecoder(s, le, 0)
	d2.Feed([]byte{0x01, 0x00, 0xFF, 0xFF}) // u16=1, length=65535 > max 10
	if _, err := d2.Next(); !errors.Is(err, ErrRecordTooLarge) {
		t.Fatalf("want ErrRecordTooLarge, got %v", err)
	}
	if _, err := d2.Next(); errors.Is(err, ErrNeedMoreData) {
		t.Fatal("bad data must not be reported as need-more-data")
	}
}

func TestQuotaCheckedBeforeAllocation(t *testing.T) {
	// A 4-byte length header can declare up to 4 GiB. With a small quota
	// the decoder must reject it from the header alone.
	s := Schema{Fields: []Field{VarBytes(4, 1<<20)}}
	d := NewDecoder(s, le, 1<<20)
	var hdr [4]byte
	le.PutUint32(hdr[:], 1<<31) // 2 GiB declared
	d.Feed(hdr[:])
	if _, err := d.Next(); !errors.Is(err, ErrRecordTooLarge) {
		t.Fatalf("want ErrRecordTooLarge, got %v", err)
	}
	if d.Buffered() != 4 {
		t.Fatalf("bad record must not be consumed, buffered=%d", d.Buffered())
	}
}

func TestRandomRoundTrips(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	for iter := 0; iter < 2000; iter++ {
		var fields []Field
		var vals []any
		nf := 1 + rng.IntN(6)
		for j := 0; j < nf; j++ {
			switch rng.IntN(8) {
			case 0:
				fields = append(fields, U8())
				vals = append(vals, byte(rng.Uint32()))
			case 1:
				fields = append(fields, U16())
				vals = append(vals, uint16(rng.Uint32()))
			case 2:
				fields = append(fields, U32())
				vals = append(vals, rng.Uint32())
			case 3:
				fields = append(fields, U64())
				vals = append(vals, rng.Uint64())
			case 4:
				fields = append(fields, F32())
				vals = append(vals, math.Float32frombits(rng.Uint32()))
			case 5:
				fields = append(fields, F64())
				vals = append(vals, math.Float64frombits(rng.Uint64()))
			case 6:
				n := rng.IntN(8)
				fields = append(fields, Bytes(n))
				b := make([]byte, n)
				rngUint8Fill(rng, b)
				vals = append(vals, b)
			case 7:
				n := rng.IntN(32)
				fields = append(fields, VarBytes(2, 64))
				b := make([]byte, n)
				rngUint8Fill(rng, b)
				vals = append(vals, b)
			}
		}
		s := Schema{Fields: fields}
		from, to := be, le
		if rng.IntN(2) == 0 {
			from, to = to, from
		}
		rec := buildRecord(s, from, vals...)
		mid, err := s.ConvertRecord(rec, from, to, 0)
		if err != nil {
			t.Fatalf("iter %d: %v", iter, err)
		}
		back, err := s.ConvertRecord(mid, to, from, 0)
		if err != nil {
			t.Fatalf("iter %d: %v", iter, err)
		}
		if !bytes.Equal(rec, back) {
			t.Fatalf("iter %d: round trip mismatch\n got %x\nwant %x", iter, back, rec)
		}
	}
}

func rngUint8Fill(rng *rand.Rand, b []byte) {
	for i := range b {
		b[i] = byte(rng.Uint32())
	}
}
