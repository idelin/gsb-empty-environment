package recstream

import (
	"bytes"
	"errors"
	"io"
	"math"
	"testing"
)

func fieldsEqual(a, b []Field) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		x, y := a[i], b[i]
		if x.ID != y.ID || x.Wire != y.Wire || x.Value != y.Value {
			return false
		}
		if !bytes.Equal(x.Raw, y.Raw) {
			return false
		}
	}
	return true
}

func sampleRecords() []*Record {
	return []*Record{
		NewRecord(), // empty record
		NewRecord(
			NewVarint(1, 150),
			NewFixed32(2, 0xdeadbeef),
			NewFixed64(3, math.MaxUint64),
			NewBytes(4, []byte("hello, world")),
			NewVarint(5, 0),
		),
		NewRecord(NewBytes(1, []byte{})), // empty bytes field
		NewRecord(NewVarint(100000, 0), NewBytes(200000, bytes.Repeat([]byte{0xab}, 300))),
	}
}

func buildStream(t *testing.T, recs []*Record) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := NewWriter(&buf)
	for _, r := range recs {
		if _, _, err := w.Write(r); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	return buf.Bytes()
}

func TestRoundTripSequential(t *testing.T) {
	recs := sampleRecords()
	data := buildStream(t, recs)
	got, err := DecodeAll(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != len(recs) {
		t.Fatalf("got %d records, want %d", len(got), len(recs))
	}
	for i := range recs {
		if !fieldsEqual(recs[i].Fields, got[i].Fields) {
			t.Fatalf("record %d mismatch:\n want %#v\n got  %#v", i, recs[i].Fields, got[i].Fields)
		}
	}
}

func TestEmptyFile(t *testing.T) {
	// Empty input: Decoder yields io.EOF immediately; no record exists.
	d := NewDecoder(bytes.NewReader(nil))
	if _, err := d.Next(); err != io.EOF {
		t.Fatalf("empty file: want io.EOF, got %v", err)
	}
	// BuildIndex on empty source yields an empty index with zero digest.
	idx, err := BuildIndex(bytes.NewReader(nil))
	if err != nil {
		t.Fatalf("build index on empty: %v", err)
	}
	if idx.Count() != 0 || idx.SourceSize != 0 {
		t.Fatalf("empty index wrong: %+v", idx)
	}
	rr := NewRandomReader(bytes.NewReader(nil), idx)
	if _, err := rr.ReadRecord(0); !errors.Is(err, ErrBoundary) {
		t.Fatalf("read empty index: want ErrBoundary, got %v", err)
	}
}

func TestEmptyRecordIsPreserved(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	off, size, err := w.Write(NewRecord())
	if err != nil {
		t.Fatal(err)
	}
	if off != 0 || size < 2 {
		t.Fatalf("empty frame off=%d size=%d", off, size)
	}
	d := NewDecoder(&buf)
	r, err := d.Next()
	if err != nil {
		t.Fatalf("decode empty record: %v", err)
	}
	if len(r.Fields) != 0 {
		t.Fatalf("empty record got fields: %#v", r.Fields)
	}
	if _, err := d.Next(); err != io.EOF {
		t.Fatalf("want EOF after empty record, got %v", err)
	}
}

func TestChunkedArbitrarySplits(t *testing.T) {
	recs := sampleRecords()
	data := buildStream(t, recs)

	for _, step := range []int{1, 2, 3, 7, 64, 4096} {
		cd := NewChunkDecoder()
		var got []*Record
		var firstErr error
		for i := 0; i < len(data); i += step {
			end := i + step
			if end > len(data) {
				end = len(data)
			}
			rs, err := cd.Feed(data[i:end])
			if err != nil && !errors.Is(err, ErrNeedMore) && firstErr == nil {
				firstErr = err
			}
			got = append(got, rs...)
		}
		if err := cd.Finalize(); err != nil {
			t.Fatalf("step=%d finalize: %v", step, err)
		}
		if firstErr != nil {
			t.Fatalf("step=%d unexpected err: %v", step, firstErr)
		}
		if len(got) != len(recs) {
			t.Fatalf("step=%d got %d records want %d", step, len(got), len(recs))
		}
		for i := range recs {
			if !fieldsEqual(recs[i].Fields, got[i].Fields) {
				t.Fatalf("step=%d record %d mismatch", step, i)
			}
		}
	}
}

func TestChunkEmptyFeedsAndNeedMore(t *testing.T) {
	cd := NewChunkDecoder()
	if _, err := cd.Feed(nil); !errors.Is(err, ErrNeedMore) {
		t.Fatalf("empty feed: want ErrNeedMore, got %v", err)
	}
	if err := cd.Finalize(); err != nil {
		t.Fatalf("nothing fed must finalize clean, got %v", err)
	}
}

func TestMissingVsBadData(t *testing.T) {
	rec := NewRecord(NewVarint(1, 7))
	full, err := EncodeFrame(rec)
	if err != nil {
		t.Fatal(err)
	}

	// Every proper prefix that ends inside the frame is "need more",
	// never corrupt.
	for cut := 0; cut < len(full)-1; cut++ {
		cd := NewChunkDecoder()
		_, err := cd.Feed(full[:cut])
		if !errors.Is(err, ErrNeedMore) {
			t.Fatalf("cut=%d: want ErrNeedMore, got %v", cut, err)
		}
	}
	// Finalize with bytes still pending means data is missing.
	cd := NewChunkDecoder()
	cd.Feed(full[:len(full)-1])
	if err := cd.Finalize(); !errors.Is(err, ErrTruncated) {
		t.Fatalf("truncated finalize: want ErrTruncated, got %v", err)
	}

	// Flipping a payload byte is bad data, not missing data.
	bad := append([]byte(nil), full...)
	bad[len(bad)-2] ^= 0xff // corrupt the body (CRC covers it)
	badCd := NewChunkDecoder()
	if _, err := badCd.Feed(bad); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("corrupt frame: want ErrCorrupt, got %v", err)
	}
	// Decoder is latched: further good bytes cannot resync.
	if _, err := badCd.Feed(full); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("latched decoder: want ErrCorrupt, got %v", err)
	}
}

func TestSequentialTruncation(t *testing.T) {
	full, _ := EncodeFrame(NewRecord(NewVarint(1, 1)))
	// Stream cut in the middle of an announced frame -> ErrTruncated.
	d := NewDecoder(bytes.NewReader(full[:len(full)-1]))
	if _, err := d.Next(); !errors.Is(err, ErrTruncated) {
		t.Fatalf("want ErrTruncated, got %v", err)
	}
}
