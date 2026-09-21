package endconv

import (
	"bytes"
	"encoding/binary"
	"math/rand"
	"testing"
)

func appendU16(buf []byte, bo binary.ByteOrder, v uint16) []byte {
	var tmp [2]byte
	bo.PutUint16(tmp[:], v)
	return append(buf, tmp[:]...)
}

func appendU32(buf []byte, bo binary.ByteOrder, v uint32) []byte {
	var tmp [4]byte
	bo.PutUint32(tmp[:], v)
	return append(buf, tmp[:]...)
}

func appendU64(buf []byte, bo binary.ByteOrder, v uint64) []byte {
	var tmp [8]byte
	bo.PutUint64(tmp[:], v)
	return append(buf, tmp[:]...)
}

func reverseBytes(b []byte) []byte {
	out := make([]byte, len(b))
	for i := range b {
		out[i] = b[len(b)-1-i]
	}
	return out
}

func randomRecord(rng *rand.Rand, s Schema, dir Direction) []byte {
	bo := sourceOrder(dir)
	buf := make([]byte, 0, 64)
	for _, f := range s {
		switch f.Kind {
		case U8:
			buf = append(buf, byte(rng.Intn(256)))
		case U16:
			var v [2]byte
			rng.Read(v[:])
			buf = appendU16(buf, bo, binary.BigEndian.Uint16(v[:]))
		case U32:
			var v [4]byte
			rng.Read(v[:])
			buf = appendU32(buf, bo, binary.BigEndian.Uint32(v[:]))
		case U64:
			var v [8]byte
			rng.Read(v[:])
			buf = appendU64(buf, bo, binary.BigEndian.Uint64(v[:]))
		case F32:
			var v [4]byte
			rng.Read(v[:])
			buf = appendU32(buf, bo, binary.BigEndian.Uint32(v[:]))
		case F64:
			var v [8]byte
			rng.Read(v[:])
			buf = appendU64(buf, bo, binary.BigEndian.Uint64(v[:]))
		case Blob:
			p := make([]byte, f.N)
			rng.Read(p)
			buf = append(buf, p...)
		case VarBlob:
			n := rng.Intn(12)
			p := make([]byte, n)
			rng.Read(p)
			buf = appendU16(buf, bo, uint16(n))
			buf = append(buf, p...)
		}
	}
	return buf
}

// TestRoundTripRandom: converting twice restores every byte, both
// directions, with and without in-place conversion.
func TestRoundTripRandom(t *testing.T) {
	schema := Schema{
		{Kind: U16}, {Kind: U8}, {Kind: F32}, {Kind: F64},
		{Kind: Blob, N: 5}, {Kind: VarBlob}, {Kind: U32},
		{Kind: VarBlob}, {Kind: U64},
	}
	rng := rand.New(rand.NewSource(42))
	for iter := 0; iter < 500; iter++ {
		dir := BigToLittle
		back := LittleToBig
		if iter%2 == 1 {
			dir, back = LittleToBig, BigToLittle
		}
		src := randomRecord(rng, schema, dir)
		original := append([]byte(nil), src...)

		dst := make([]byte, len(src))
		n, err := Convert(dst, src, schema, dir)
		if err != nil {
			t.Fatalf("first Convert: %v", err)
		}
		if n != len(src) {
			t.Fatalf("Convert wrote %d bytes, want %d", n, len(src))
		}

		again := make([]byte, len(dst))
		if _, err := Convert(again, dst, schema, back); err != nil {
			t.Fatalf("second Convert: %v", err)
		}
		if !bytes.Equal(again, original) {
			t.Fatalf("round trip mismatch\norig: %x\ngot:  %x", original, again)
		}

		buf := append([]byte(nil), original...)
		if err := ConvertInPlace(buf, schema, dir); err != nil {
			t.Fatalf("in-place forward: %v", err)
		}
		if err := ConvertInPlace(buf, schema, back); err != nil {
			t.Fatalf("in-place back: %v", err)
		}
		if !bytes.Equal(buf, original) {
			t.Fatalf("in-place round trip mismatch\norig: %x\ngot:  %x", original, buf)
		}
	}
}

// TestNaNBitsPreserved: quiet and signaling NaNs with non-trivial
// payloads must keep every bit through both conversion directions.
func TestNaNBitsPreserved(t *testing.T) {
	f32Payloads := []uint32{
		0x7fc00001, // quiet NaN, payload bit set
		0x7fa00000, // signaling NaN
		0x7fa0b0c0, // signaling NaN with payload
		0xffc12345, // negative quiet NaN with payload
	}
	f64Payloads := []uint64{
		0x7ff8000000000001,
		0x7ff0000000000001, // signaling NaN
		0x7ff8000000000100,
		0xfff800000000abc0,
	}
	schema := Schema{{Kind: F32}, {Kind: F64}}

	for _, a := range f32Payloads {
		for _, b := range f64Payloads {
			src := make([]byte, 12)
			binary.BigEndian.PutUint32(src[0:4], a)
			binary.BigEndian.PutUint64(src[4:12], b)

			dst := make([]byte, 12)
			if _, err := Convert(dst, src, schema, BigToLittle); err != nil {
				t.Fatal(err)
			}
			if got := binary.LittleEndian.Uint32(dst[0:4]); got != a {
				t.Fatalf("f32 NaN bits: got %08x want %08x", got, a)
			}
			if got := binary.LittleEndian.Uint64(dst[4:12]); got != b {
				t.Fatalf("f64 NaN bits: got %016x want %016x", got, b)
			}

			again := make([]byte, 12)
			if _, err := Convert(again, dst, schema, LittleToBig); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(again, src) {
				t.Fatalf("NaN double conversion mismatch: %x vs %x", again, src)
			}
		}
	}
}

// TestVarBlobPayloadUntouched: only the 16-bit length header changes
// endianness; the payload bytes are copied verbatim even when they look
// like multi-byte numbers.
func TestVarBlobPayloadUntouched(t *testing.T) {
	schema := Schema{{Kind: U16}, {Kind: VarBlob}, {Kind: U32}}
	payload := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06}
	src := []byte{
		0x12, 0x34, // u16 BE
		0x00, uint8(len(payload)), // VarBlob header BE: 6
	}
	src = append(src, payload...)
	src = append(src, 0xa1, 0xb2, 0xc3, 0xd4) // u32 BE

	dst := make([]byte, len(src))
	if _, err := Convert(dst, src, schema, BigToLittle); err != nil {
		t.Fatal(err)
	}
	want := []byte{
		0x34, 0x12, // u16 LE
		uint8(len(payload)), 0x00, // header LE
	}
	want = append(want, payload...)
	want = append(want, 0xd4, 0xc3, 0xb2, 0xa1)
	if !bytes.Equal(dst, want) {
		t.Fatalf("varblob conversion:\ngot:  %x\nwant: %x", dst, want)
	}
}

// TestInPlaceMixedFields: in-place conversion of a record containing
// every field kind must match separate-buffer conversion.
func TestInPlaceMixedFields(t *testing.T) {
	schema := Schema{
		{Kind: U8}, {Kind: U16}, {Kind: F32}, {Kind: Blob, N: 3},
		{Kind: VarBlob}, {Kind: F64}, {Kind: U64},
	}
	rng := rand.New(rand.NewSource(7))
	src := randomRecord(rng, schema, BigToLittle)

	inPlace := append([]byte(nil), src...)
	if err := ConvertInPlace(inPlace, schema, BigToLittle); err != nil {
		t.Fatal(err)
	}
	dst := make([]byte, len(src))
	if _, err := Convert(dst, src, schema, BigToLittle); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(inPlace, dst) {
		t.Fatalf("in-place differs from out-of-place:\nip: %x\nop: %x", inPlace, dst)
	}
}

// TestPartialOverlapRejected: partially overlapping src/dst must fail
// with ErrOverlap without modifying anything.
func TestPartialOverlapRejected(t *testing.T) {
	schema := Schema{{Kind: U32}, {Kind: U16}}
	recLen := 6
	backing := make([]byte, recLen*2)
	for i := range backing {
		backing[i] = byte(i + 1)
	}
	snapshot := append([]byte(nil), backing...)

	src := backing[0:recLen]
	dst := backing[1 : 1+recLen]
	if _, err := Convert(dst, src, schema, BigToLittle); err != ErrOverlap {
		t.Fatalf("want ErrOverlap, got %v", err)
	}
	if !bytes.Equal(backing, snapshot) {
		t.Fatalf("backing array modified after overlap rejection:\n%x\n%x", backing, snapshot)
	}

	// Fully disjoint slices in one larger array are fine.
	disjoint := backing[recLen : recLen*2]
	if _, err := Convert(disjoint, src, schema, BigToLittle); err != nil {
		t.Fatalf("disjoint conversion: %v", err)
	}
}

// TestShortBuffers: truncated input reports ErrShortRecord; undersized
// destination reports ErrShortBuffer.
func TestShortBuffers(t *testing.T) {
	schema := Schema{{Kind: U32}, {Kind: VarBlob}}
	src := []byte{0x00, 0x00, 0x00, 0x01, 0x00, 0x05, 1, 2}
	if _, err := RecordLength(src, schema, BigToLittle); err != ErrShortRecord {
		t.Fatalf("want ErrShortRecord, got %v", err)
	}
	complete := append(src, 3, 4, 5)
	n, err := RecordLength(complete, schema, BigToLittle)
	if err != nil || n != len(complete) {
		t.Fatalf("RecordLength = (%d, %v)", n, err)
	}
	dst := make([]byte, len(complete)-1)
	if _, err := Convert(dst, complete, schema, BigToLittle); err != ErrShortBuffer {
		t.Fatalf("want ErrShortBuffer, got %v", err)
	}
}

// TestChunkedStreaming: feed one byte at a time across two records;
// Next must return NeedMoreData until each record completes and the
// converted output must equal one-shot conversion.
func TestChunkedStreaming(t *testing.T) {
	schema := Schema{
		{Kind: U16}, {Kind: F64}, {Kind: U8},
		{Kind: VarBlob}, {Kind: U32},
	}
	rng := rand.New(rand.NewSource(99))
	r1 := randomRecord(rng, schema, BigToLittle)
	r2 := randomRecord(rng, schema, BigToLittle)
	stream := append(append([]byte(nil), r1...), r2...)

	p, err := NewParser(schema, BigToLittle, Limits{MaxRecordBytes: 4096})
	if err != nil {
		t.Fatal(err)
	}

	emitted := 0
	var outputs [][]byte
	for i := range stream {
		if err := p.Write(stream[i : i+1]); err != nil {
			t.Fatalf("Write byte %d: %v", i, err)
		}
		out, status, perr := p.Next()
		if perr != nil {
			t.Fatalf("Next byte %d: %v", i, perr)
		}
		if i < len(r1)-1 && status == RecordReady {
			t.Fatalf("record ready too early at byte %d", i)
		}
		if status == NeedMoreData && out != nil {
			t.Fatal("NeedMoreData returned non-nil record")
		}
		if status == RecordReady {
			emitted++
			outputs = append(outputs, out)
		}
	}
	if emitted != 2 {
		t.Fatalf("want 2 records, got %d", emitted)
	}
	for idx, src := range [][]byte{r1, r2} {
		want := make([]byte, len(src))
		if _, err := Convert(want, src, schema, BigToLittle); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(outputs[idx], want) {
			t.Fatalf("record %d mismatch:\ngot:  %x\nwant: %x", idx, outputs[idx], want)
		}
	}
}

// TestNeedMoreDataVsBadData: a VarBlob header that declares a payload
// beyond the quota is bad data as soon as the header bytes arrive,
// even though no payload bytes exist yet. Nothing may be allocated
// from the bogus length.
func TestNeedMoreDataVsBadData(t *testing.T) {
	schema := Schema{{Kind: U8}, {Kind: VarBlob}}
	p, err := NewParser(schema, BigToLittle, Limits{
		MaxRecordBytes:  100,
		MaxPayloadBytes: 10,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Only the u8 + the 2-byte header; header claims 60000 bytes.
	if err := p.Write([]byte{0x01, 0xea, 0x60}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	rec, status, err := p.Next()
	if err != ErrPayloadQuota {
		t.Fatalf("want ErrPayloadQuota, got status=%d err=%v", status, err)
	}
	if rec != nil || status == RecordReady {
		t.Fatal("no record may be produced for an over-quota header")
	}

	// A genuinely incomplete (but valid) header returns NeedMoreData.
	p2, _ := NewParser(schema, BigToLittle, Limits{MaxRecordBytes: 100, MaxPayloadBytes: 10})
	if err := p2.Write([]byte{0x01, 0x00}); err != nil {
		t.Fatal(err)
	}
	if _, status, err := p2.Next(); status != NeedMoreData || err != nil {
		t.Fatalf("want NeedMoreData, got status=%d err=%v", status, err)
	}
}

// TestRecordQuotaBeforeAllocation: once pending input exceeds the
// record byte budget, Write rejects the chunk and keeps none of it.
func TestRecordQuotaBeforeAllocation(t *testing.T) {
	schema := Schema{{Kind: Blob, N: 1}}
	p, err := NewParser(schema, BigToLittle, Limits{MaxRecordBytes: 4, MaxPayloadBytes: 4})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Write([]byte{1, 2}); err != nil {
		t.Fatal(err)
	}
	before := p.Buffered()
	if err := p.Write(make([]byte, 5)); err != ErrRecordQuota {
		t.Fatalf("want ErrRecordQuota, got %v", err)
	}
	if p.Buffered() != before {
		t.Fatalf("offending chunk partially retained: %d vs %d", p.Buffered(), before)
	}
}

// TestLittleEndianSource: construct an LE record, convert to BE, and
// verify numeric and header encodings independently.
func TestLittleEndianSource(t *testing.T) {
	schema := Schema{{Kind: U16}, {Kind: VarBlob}}
	src := []byte{0x34, 0x12, 0x03, 0x00, 0xaa, 0xbb, 0xcc}
	dst := make([]byte, len(src))
	if _, err := Convert(dst, src, schema, LittleToBig); err != nil {
		t.Fatal(err)
	}
	want := []byte{0x12, 0x34, 0x00, 0x03, 0xaa, 0xbb, 0xcc}
	if !bytes.Equal(dst, want) {
		t.Fatalf("LE->BE: got %x want %x", dst, want)
	}
}

// TestBadSchema: invalid field kinds and negative blob sizes are errors.
func TestBadSchema(t *testing.T) {
	if err := (Schema{{Kind: Blob, N: -1}}).Validate(); err != ErrBadSchema {
		t.Fatalf("want ErrBadSchema, got %v", err)
	}
	if err := (Schema{{Kind: Kind(99)}}).Validate(); err != ErrBadSchema {
		t.Fatalf("want ErrBadSchema, got %v", err)
	}
	if _, err := NewParser(Schema{{Kind: Kind(99)}}, BigToLittle, Limits{}); err != ErrBadSchema {
		t.Fatalf("NewParser: want ErrBadSchema, got %v", err)
	}
}
