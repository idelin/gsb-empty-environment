package recstream

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"strings"
	"testing"
)

// A corrupted length header must not let the parser resync onto a later
// valid record. After the first bad length, both the sequential and chunk
// decoders terminate with an error instead of delivering the second record.
func TestBadLengthNoResync(t *testing.T) {
	r1 := NewRecord(NewBytes(1, []byte("first-record-payload")))
	r2 := NewRecord(NewVarint(2, 42))
	var buf bytes.Buffer
	w := NewWriter(&buf)
	o1, s1, _ := w.Write(r1)
	_, _, _ = w.Write(r2)

	tampered := append([]byte(nil), buf.Bytes()...)
	// Inflate the first frame's declared length to cover the next record.
	// The length varint of the small first frame is one byte; rewriting it
	// in place at o1 keeps the buffer aligned.
	tampered[o1] = byte(s1 + 5)
	cd := NewChunkDecoder()
	got, err := cd.Feed(tampered)
	if err == nil {
		t.Fatalf("expected error, got %d records", len(got))
	}
	if errors.Is(err, ErrNeedMore) {
		t.Fatalf("bad length reported as need-more; subsequent good record would be swallowed")
	}
	if err := cd.Finalize(); err == nil {
		t.Fatalf("finalize after bad length must fail")
	}

	// Sequential decoder likewise must not yield the second record.
	d := NewDecoder(bytes.NewReader(tampered))
	var n int
	for {
		_, err := d.Next()
		if err != nil {
			break
		}
		n++
	}
	if n != 0 {
		t.Fatalf("decoder resynced and delivered %d record(s) after bad length", n)
	}
}

// A frame shortened so only a single 0x00 byte remains must not be accepted
// as an empty record (the length-seeded CRC rejects it).
func TestShortenedFrameNotEmpty(t *testing.T) {
	short := []byte{1, 0x00} // claims frameLen=1, payload byte is CRC=0
	if _, _, _, err := splitFrame(short, DefaultMaxRecordSize); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("shortened frame: want ErrCorrupt, got %v", err)
	}
	// Sanity: a genuinely empty frame is accepted and parses to no fields.
	empty, err := EncodeFrame(NewRecord())
	if err != nil {
		t.Fatal(err)
	}
	body, _, n, err := splitFrame(empty, DefaultMaxRecordSize)
	if err != nil {
		t.Fatalf("real empty frame: %v", err)
	}
	if n != len(empty) || len(body) != 0 {
		t.Fatalf("empty frame split: n=%d len=%d bodyLen=%d", n, len(empty), len(body))
	}
}

func TestLengthQuotaEnforcedBeforeAllocation(t *testing.T) {
	const quota = 32
	// Only the length header is present: quota failure must occur before
	// any payload buffer growth.
	var hdr [binary.MaxVarintLen64]byte
	binary.PutUvarint(hdr[:], uint64(quota+1))
	cd := NewChunkDecoder(WithChunkMaxRecordSize(quota))
	if _, err := cd.Feed(hdr[:]); !errors.Is(err, ErrRecordTooLarge) {
		t.Fatalf("chunk quota: want ErrRecordTooLarge, got %v", err)
	}
	d := NewDecoder(strings.NewReader(string(hdr[:])+strings.Repeat("\x00", quota+1)),
		WithDecoderMaxRecordSize(quota))
	if _, err := d.Next(); !errors.Is(err, ErrRecordTooLarge) {
		t.Fatalf("stream quota: want ErrRecordTooLarge, got %v", err)
	}
}

func TestOversizedBytesFieldRejected(t *testing.T) {
	// A body whose bytes field length runs past the frame is corrupt.
	var bad bytes.Buffer
	put := func(v uint64) {
		var b [binary.MaxVarintLen64]byte
		n := binary.PutUvarint(b[:], v)
		bad.Write(b[:n])
	}
	put(uint64((3 << 3) | WireBytes)) // tag field 3, wire bytes
	put(100000)                       // claims 100000 bytes
	bad.WriteString("short")
	frame := encodeForTest(t, bad.Bytes())
	_, err := DecodeAll(bytes.NewReader(frame))
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("oversized bytes field: want ErrCorrupt, got %v", err)
	}
}

// helper: wrap an arbitrary raw body into a well-checksummed frame.
func encodeForTest(t *testing.T, body []byte) []byte {
	t.Helper()
	var len8 [8]byte
	binary.LittleEndian.PutUint64(len8[:], uint64(len(body)))
	hsh := crc32.New(crc32.IEEETable)
	hsh.Write(len8[:])
	hsh.Write(body)
	var out bytes.Buffer
	put := func(v uint64) {
		var b [binary.MaxVarintLen64]byte
		n := binary.PutUvarint(b[:], v)
		out.Write(b[:n])
	}
	put(uint64(len(body)))
	out.Write(body)
	put(uint64(hsh.Sum32()))
	return out.Bytes()
}

func TestUnknownFieldsSkippedAcrossVersions(t *testing.T) {
	// Old reader schema only knows field 1. New writer adds fields with
	// high IDs and unknown-to-reader content.
	old := NewRecord(NewVarint(1, 7))
	combined := NewRecord(
		NewVarint(1, 7),
		NewFixed64(42, 123456789), // added in v2
		NewBytes(43, []byte("v2-only")),
		NewVarint(44, 999),
	)
	data, err := EncodeFrame(combined)
	if err != nil {
		t.Fatal(err)
	}
	cd := NewChunkDecoder()
	rs, err := cd.Feed(data)
	if err != nil || len(rs) != 1 {
		t.Fatalf("decode combined: n=%d err=%v", len(rs), err)
	}
	got := rs[0]
	v, ok := got.Get(1)
	if !ok {
		t.Fatal("known field 1 lost")
	}
	if val, _ := v.Varint(); val != 7 {
		t.Fatalf("field 1 = %d, want 7", val)
	}
	if len(got.Fields) != 4 {
		t.Fatalf("unknown fields not preserved: %d", len(got.Fields))
	}
	// Re-encode the decoded record: unknown fields round-trip verbatim.
	out2, err := EncodeFrame(got)
	if err != nil || !bytes.Equal(out2, data) {
		t.Fatalf("unknown fields did not round-trip: %v", err)
	}
	// Type drift on a reused ID is rejected as corrupt.
	drift := NewRecord(NewBytes(1, []byte("was varint")))
	ddata, _ := EncodeFrame(drift)
	rs2, derr := NewChunkDecoder().Feed(ddata)
	_ = rs2
	if derr != nil {
		t.Fatalf("decoding a different wire type is structurally fine: %v", derr)
	}
	// The typed accessor signals the mismatch rather than misreading.
	if _, ok := rs2[0].Get(1); !ok {
		t.Fatal("field present")
	}
	if _, ok := rs2[0].Fields[0].Varint(); ok {
		t.Fatal("varint accessor must fail for bytes wire type")
	}
	_ = old
}

func TestInvalidTagsAndVarints(t *testing.T) {
	cases := [][]byte{
		{0x00},                        // field id 0
		{(7 << 3) | 4, 0, 0, 0, 0},    // unknown wire type 4
		{(1 << 3) | WireFixed64},      // truncated fixed64
		{(1 << 3) | WireBytes, 0xff},  // truncated length varint
		{(1 << 3)},                    // no value at all
		{(1 << 3) | WireVarint, 0xff}, // unterminated varint
	}
	for i, c := range cases {
		frame := encodeForTest(t, c)
		_, err := DecodeAll(bytes.NewReader(frame))
		if !errors.Is(err, ErrCorrupt) {
			t.Fatalf("case %d: want ErrCorrupt, got %v", i, err)
		}
	}
}

func TestIndexBuildAndRandomAccess(t *testing.T) {
	recs := sampleRecords()
	data := buildStream(t, recs)
	src := bytes.NewReader(data)

	idx, err := BuildIndex(src)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if idx.Count() != len(recs) || idx.SourceSize != int64(len(data)) {
		t.Fatalf("index stats wrong: %+v", idx)
	}

	// Serialize and re-parse the index.
	var ib bytes.Buffer
	if _, err := idx.WriteTo(&ib); err != nil {
		t.Fatal(err)
	}
	idx2, err := ParseIndex(bytes.NewReader(ib.Bytes()))
	if err != nil {
		t.Fatalf("parse index: %v", err)
	}
	if idx2.Count() != idx.Count() || idx2.SourceSize != idx.SourceSize ||
		idx2.SourceDigest != idx.SourceDigest {
		t.Fatalf("index round-trip mismatch")
	}

	// Verify source summary.
	if err := idx2.VerifySource(bytes.NewReader(data)); err != nil {
		t.Fatalf("verify identical source: %v", err)
	}
	modified := append([]byte(nil), data...)
	modified[len(modified)-1] ^= 1
	if err := idx2.VerifySource(bytes.NewReader(modified)); !errors.Is(err, ErrSourceMismatch) {
		t.Fatalf("tampered source: want ErrSourceMismatch, got %v", err)
	}
	if err := idx2.VerifySource(io.MultiReader(bytes.NewReader(data), strings.NewReader("x"))); !errors.Is(err, ErrSourceMismatch) {
		t.Fatalf("appended source: want ErrSourceMismatch, got %v", err)
	}

	// Random access hits true boundaries and returns equal records.
	rr := NewRandomReader(bytes.NewReader(data), idx2)
	for i := range recs {
		got, err := rr.ReadRecord(i)
		if err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
		if !fieldsEqual(recs[i].Fields, got.Fields) {
			t.Fatalf("random record %d mismatch", i)
		}
	}
	if _, err := rr.ReadRecord(len(recs)); !errors.Is(err, ErrBoundary) {
		t.Fatalf("out of range: want ErrBoundary, got %v", err)
	}
}

func TestIndexHitMustLandOnBoundary(t *testing.T) {
	recs := sampleRecords()
	data := buildStream(t, recs)
	idx, _ := BuildIndex(bytes.NewReader(data))

	// Point entry 1 one byte past the true boundary: must fail.
	tam := *idx
	tam.Entries = append([]Entry(nil), idx.Entries...)
	tam.Entries[1].Offset++
	rr := NewRandomReader(bytes.NewReader(data), &tam)
	if _, err := rr.ReadRecord(1); !errors.Is(err, ErrBoundary) {
		t.Fatalf("offset+1: want ErrBoundary, got %v", err)
	}

	// Corrupt a body byte in the source; digest check and CRC check fail.
	bad := append([]byte(nil), data...)
	entry := idx.Entries[1]
	bad[entry.Offset+1] ^= 0xff
	if err := idx.VerifySource(bytes.NewReader(bad)); !errors.Is(err, ErrSourceMismatch) {
		t.Fatalf("digest must detect corruption, got %v", err)
	}
	rr2 := NewRandomReader(bytes.NewReader(bad), idx)
	if _, err := rr2.ReadRecord(1); !errors.Is(err, ErrBoundary) {
		t.Fatalf("corrupt body: want ErrBoundary, got %v", err)
	}

	// Parse rejects an index whose offsets do not chain into exact size.
	var broken bytes.Buffer
	broken.Write(indexMagic)
	put := func(w *bytes.Buffer, v uint64) {
		var b [binary.MaxVarintLen64]byte
		n := binary.PutUvarint(b[:], v)
		w.Write(b[:n])
	}
	put(&broken, uint64(len(data)))
	broken.Write(make([]byte, 32))
	put(&broken, 1)
	put(&broken, 5) // offset 5
	put(&broken, 1) // frameSize 1
	put(&broken, 0) // crc
	if _, err := ParseIndex(&broken); !errors.Is(err, ErrIndexCorrupt) {
		t.Fatalf("non-chaining index: want ErrIndexCorrupt, got %v", err)
	}
}
