package recordio

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func mustStream(t *testing.T, records ...[]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	if _, err := WriteStream(&buf, records...); err != nil {
		t.Fatalf("WriteStream: %v", err)
	}
	return buf.Bytes()
}

func decodeAll(t *testing.T, data []byte, chunkSize int, maxSize int) []*Record {
	t.Helper()
	p := NewParser(maxSize)
	var got []*Record
	for len(data) > 0 {
		n := chunkSize
		if n > len(data) {
			n = len(data)
		}
		p.Feed(data[:n])
		data = data[n:]
		if err := p.Drain(func(rec *Record, _ int64) error {
			got = append(got, rec)
			return nil
		}); err != nil && err != ErrNeedMoreData {
			t.Fatalf("decode: %v", err)
		}
	}
	if !p.Done() {
		t.Fatalf("parser not Done after all bytes: err=%v", p.Err())
	}
	return got
}

func TestRoundTrip(t *testing.T) {
	recs := [][]byte{
		EncodeRecord(1, []Field{{1, []byte("alice")}, {2, []byte("30")}}),
		EncodeRecord(2, []Field{{1, []byte("bob")}, {2, []byte("31")}, {3, []byte("bob@x")}}),
	}
	data := mustStream(t, recs...)

	got := decodeAll(t, data, 64, 0)
	if len(got) != 2 {
		t.Fatalf("got %d records, want 2", len(got))
	}
	if got[0].SchemaVersion != 1 || got[1].SchemaVersion != 2 {
		t.Fatalf("schema versions = %d,%d", got[0].SchemaVersion, got[1].SchemaVersion)
	}
	if v, ok := got[1].FieldByID(3); !ok || string(v) != "bob@x" {
		t.Fatalf("field 3 = %q,%v", v, ok)
	}
}

func TestChunkedFeedsAreEquivalent(t *testing.T) {
	recs := [][]byte{
		EncodeRecord(1, []Field{{1, []byte("hello")}}),
		EncodeRecord(1, nil),
		EncodeRecord(3, []Field{{7, bytes.Repeat([]byte("z"), 5000)}}),
	}
	data := mustStream(t, recs...)

	reference := decodeAll(t, data, len(data), 0)
	for _, chunk := range []int{1, 2, 3, 5, 16, 64, 4096} {
		got := decodeAll(t, data, chunk, 0)
		if len(got) != len(reference) {
			t.Fatalf("chunk %d: got %d records, want %d", chunk, len(got), len(reference))
		}
		for i := range got {
			if got[i].SchemaVersion != reference[i].SchemaVersion ||
				len(got[i].Fields) != len(reference[i].Fields) {
				t.Fatalf("chunk %d: record %d mismatch", chunk, i)
			}
			for j, f := range got[i].Fields {
				if f.ID != reference[i].Fields[j].ID ||
					!bytes.Equal(f.Data, reference[i].Fields[j].Data) {
					t.Fatalf("chunk %d: field mismatch", chunk)
				}
			}
		}
	}
}

func TestNeedMoreDataIsNotBadData(t *testing.T) {
	full := mustStream(t, EncodeRecord(1, []Field{{1, []byte("abc")}}))
	p := NewParser(0)

	// Feed one byte at a time. Before the frame completes, only
	// ErrNeedMoreData may appear; never ErrBadData.
	for i := 0; i < len(full)-1; i++ {
		p.Feed(full[i : i+1])
		_, _, err := p.Next()
		if !errors.Is(err, ErrNeedMoreData) {
			t.Fatalf("byte %d: err=%v, want ErrNeedMoreData", i, err)
		}
	}
	p.Feed(full[len(full)-1:])
	rec, _, err := p.Next()
	if err != nil {
		t.Fatalf("final Next: %v", err)
	}
	if string(rec.Fields[0].Data) != "abc" {
		t.Fatalf("payload = %q", rec.Fields[0].Data)
	}
}

func TestBadMagicIsBadData(t *testing.T) {
	p := NewParser(0)
	p.Feed([]byte("XXXX"))
	_, _, err := p.Next()
	if !errors.Is(err, ErrBadData) {
		t.Fatalf("err=%v, want ErrBadData", err)
	}
}

func TestLengthQuotaCheckedBeforeAllocation(t *testing.T) {
	const quota = 64
	p := NewParser(quota)
	p.Feed([]byte(Magic))

	// Declare a body far beyond the quota but only supply the 4-byte length.
	// This must be ErrBadData, not ErrNeedMoreData, and nothing may be
	// allocated for the missing giant body.
	var frame [lenSize]byte
	putU32 := frame[:]
	putU32[0] = 0x00
	putU32[1] = 0x00
	putU32[2] = 0x10
	putU32[3] = 0x00 // 1 MiB > 64
	p.Feed(putU32)
	if _, _, err := p.Next(); !errors.Is(err, ErrBadData) {
		t.Fatalf("oversized length: err=%v, want ErrBadData", err)
	}
	if p.Err() == nil {
		t.Fatal("parser should retain terminal error")
	}
}

func TestBadLengthLocksParserAndCannotResync(t *testing.T) {
	p := NewParser(0)
	p.Feed([]byte(Magic))

	// First frame: bodyLen=2, smaller than the 4-byte meta block -> bad.
	var bad [lenSize]byte
	binaryPutU32(bad[:], 2)
	p.Feed(bad[:])
	if _, _, err := p.Next(); !errors.Is(err, ErrBadData) {
		t.Fatalf("bad length: err=%v", err)
	}

	// Feeding a perfectly valid record afterwards must not be accepted:
	// a corrupt length can never make later bytes parse misaligned.
	p.Feed(EncodeRecord(9, []Field{{1, []byte("victim")}}))
	_, _, err := p.Next()
	if !errors.Is(err, ErrBadData) {
		t.Fatalf("after corruption: err=%v, want sticky ErrBadData", err)
	}
}

func TestOverlongFrameTrailingBytesRejected(t *testing.T) {
	p := NewParser(0)
	p.Feed([]byte(Magic))

	// Hand-built frame: bodyLen claims 8 bytes (meta + 4 trailing), but the
	// field count is 0. Strict boundary parsing rejects the trailing bytes.
	frame := make([]byte, lenSize+recMetaSize+lenSize)
	binaryPutU32(frame[0:lenSize], recMetaSize+lenSize)
	binaryPutU16(frame[lenSize:], 1)    // schema v1
	binaryPutU16(frame[lenSize+2:], 0)  // zero fields -> 4 trailing bytes
	p.Feed(frame)
	if _, _, err := p.Next(); !errors.Is(err, ErrBadData) {
		t.Fatalf("overlong frame: err=%v, want ErrBadData", err)
	}
}

func TestEmptyRecordIsValid(t *testing.T) {
	got := decodeAll(t, mustStream(t, EncodeRecord(1, nil)), 1, 0)
	if len(got) != 1 {
		t.Fatalf("got %d records", len(got))
	}
	if got[0].SchemaVersion != 1 || len(got[0].Fields) != 0 {
		t.Fatalf("empty record not empty: %+v", got[0])
	}
}

func TestEmptyFileHeaderOnlyHasZeroRecords(t *testing.T) {
	data := []byte(Magic)
	idx, err := BuildIndex(bytes.NewReader(data), 0)
	if err != nil {
		t.Fatalf("header-only file: %v", err)
	}
	if idx.Len() != 0 {
		t.Fatalf("want 0 indexed records, got %d", idx.Len())
	}
}

func TestZeroByteFileIsBadData(t *testing.T) {
	_, err := BuildIndex(bytes.NewReader(nil), 0)
	if !errors.Is(err, ErrBadData) {
		t.Fatalf("zero-byte file: err=%v, want ErrBadData", err)
	}
}

func TestTruncatedStreamIsBadData(t *testing.T) {
	full := mustStream(t, EncodeRecord(1, []Field{{1, []byte("abc")}}))
	for _, cut := range []int{1, 3, len(full) - 1} {
		if _, err := BuildIndex(bytes.NewReader(full[:cut]), 0); !errors.Is(err, ErrBadData) {
			t.Fatalf("cut=%d: err=%v, want ErrBadData", cut, err)
		}
	}
}

func TestIndexOffsetsPointAtRealBoundaries(t *testing.T) {
	recs := [][]byte{
		EncodeRecord(1, []Field{{1, []byte("a")}}),
		EncodeRecord(1, nil),
		EncodeRecord(2, []Field{{1, []byte("bb")}, {9, []byte("skip-me")}}),
	}
	data := mustStream(t, recs...)

	idx, err := BuildIndex(bytes.NewReader(data), 0)
	if err != nil {
		t.Fatalf("BuildIndex: %v", err)
	}
	if idx.Len() != 3 {
		t.Fatalf("index len = %d", idx.Len())
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "data.bin")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	for n := 0; n < idx.Len(); n++ {
		rec, err := ReadRecordAt(f, idx, n, 0)
		if err != nil {
			t.Fatalf("ReadRecordAt(%d): %v", n, err)
		}
		stream := decodeAll(t, mustStream(t, EncodeRecord(rec.SchemaVersion, rec.Fields)), 7, 0)[0]
		if stream.SchemaVersion != rec.SchemaVersion || len(stream.Fields) != len(rec.Fields) {
			t.Fatalf("record %d not independently re-readable", n)
		}
	}

	// Cross-version lookup: reader asks only for field 1; field 9 is skipped.
	rec, err := ReadRecordAt(f, idx, 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := rec.FieldByID(1); !ok || string(v) != "bb" {
		t.Fatalf("field 1 = %q,%v", v, ok)
	}
	if _, ok := rec.FieldByID(42); ok {
		t.Fatal("unknown field should be absent, not an error")
	}
}

func TestIndexHitOnCorruptedLengthFails(t *testing.T) {
	data := mustStream(t,
		EncodeRecord(1, []Field{{1, []byte("first")}}),
		EncodeRecord(1, []Field{{1, []byte("second")}}),
	)
	idx, err := BuildIndex(bytes.NewReader(data), 0)
	if err != nil {
		t.Fatal(err)
	}
	// Corrupt the second record's declared length in the source bytes.
	off := int(idx.Offsets[1])
	data[off] = 0xFF
	data[off+1] = 0xFF
	data[off+2] = 0xFF
	data[off+3] = 0x7F // ~2 GiB
	dir := t.TempDir()
	path := filepath.Join(dir, "corrupt.bin")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := ReadRecordAt(f, idx, 1, 0); !errors.Is(err, ErrBadData) {
		t.Fatalf("corrupt indexed hit: err=%v, want ErrBadData", err)
	}
}

func TestIndexSaveLoadAndDigestGuard(t *testing.T) {
	recs := [][]byte{EncodeRecord(1, []Field{{1, []byte("x")}})}
	data := mustStream(t, recs...)

	idx, err := BuildIndex(bytes.NewReader(data), 0)
	if err != nil {
		t.Fatal(err)
	}
	var saved bytes.Buffer
	if err := idx.Save(&saved); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadIndex(&saved)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Len() != idx.Len() || loaded.Offsets[0] != idx.Offsets[0] {
		t.Fatalf("round-trip mismatch: %+v vs %+v", loaded.Offsets, idx.Offsets)
	}

	digest, err := DigestOf(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if err := loaded.VerifySource(digest); err != nil {
		t.Fatalf("matching digest rejected: %v", err)
	}

	// One source byte changed: digest guard must reject the stale index.
	tampered := bytes.Clone(data)
	tampered[len(tampered)-1] ^= 0xFF
	badDigest, err := DigestOf(bytes.NewReader(tampered))
	if err != nil {
		t.Fatal(err)
	}
	if err := loaded.VerifySource(badDigest); !errors.Is(err, ErrBadData) {
		t.Fatalf("tampered source: err=%v, want ErrBadData", err)
	}
}

func TestCorruptIndexRejected(t *testing.T) {
	if _, err := LoadIndex(bytes.NewReader([]byte("XXXX"))); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("truncated header: err=%v", err)
	}
	var bad bytes.Buffer
	bad.WriteString(indexMagic)
	bad.Write(make([]byte, 32))
	var cnt [4]byte
	binaryPutU32(cnt[:], maxIndexEntries+1)
	bad.Write(cnt[:])
	if _, err := LoadIndex(&bad); !errors.Is(err, ErrBadData) {
		t.Fatalf("huge count: err=%v, want ErrBadData", err)
	}
}
