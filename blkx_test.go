package blkx

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// encodeA builds a format-A stream from data using the given block sizes.
func encodeA(t *testing.T, data, meta []byte, blockSizes ...int) []byte {
	t.Helper()
	var buf bytes.Buffer
	aw, err := NewAWriter(&buf, meta, uint64(len(data)))
	if err != nil {
		t.Fatalf("NewAWriter: %v", err)
	}
	off := 0
	for _, n := range blockSizes {
		if off >= len(data) {
			break
		}
		if off+n > len(data) {
			n = len(data) - off
		}
		if err := aw.WriteBlock(data[off : off+n]); err != nil {
			t.Fatalf("WriteBlock: %v", err)
		}
		off += n
	}
	if off < len(data) {
		if err := aw.WriteBlock(data[off:]); err != nil {
			t.Fatalf("WriteBlock tail: %v", err)
		}
	}
	if err := aw.Finish(); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	return buf.Bytes()
}

func TestRoundTripRandomChunks(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	data := make([]byte, (1<<20)+777)
	rng.Read(data)
	meta := []byte(`{"title":"random blob","sha":"deadbeef","tags":["a","b"]}`)

	var sizes []int
	for remaining := len(data); remaining > 0; {
		n := 1 + rng.Intn(70000)
		if n > remaining {
			n = remaining
		}
		sizes = append(sizes, n)
		remaining -= n
	}
	aStream := encodeA(t, data, meta, sizes...)

	var bStream bytes.Buffer
	if err := Transcode(&bStream, bytes.NewReader(aStream)); err != nil {
		t.Fatalf("Transcode: %v", err)
	}

	var out bytes.Buffer
	gotMeta, n, err := Decode(&out, bytes.NewReader(bStream.Bytes()), uint64(len(data)))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !bytes.Equal(out.Bytes(), data) {
		t.Fatalf("round trip mismatch: got %d bytes, want %d", out.Len(), len(data))
	}
	if n != uint64(len(data)) {
		t.Fatalf("decoded length %d, want %d", n, len(data))
	}
	if !bytes.Equal(gotMeta, meta) {
		t.Fatalf("metadata not preserved: got %s", gotMeta)
	}
}

// corruptPayload flips one byte inside the first block payload of a
// format-A stream.
func corruptPayload(t *testing.T, stream []byte) []byte {
	t.Helper()
	ar, err := NewAReader(bytes.NewReader(stream))
	if err != nil {
		t.Fatalf("NewAReader: %v", err)
	}
	blk, err := ar.NextBlock()
	if err != nil || len(blk) == 0 {
		t.Fatalf("need a non-empty first block: %v", err)
	}
	headerLen := 4 + 1 + 8 + 4 + len(ar.Meta)
	bad := bytes.Clone(stream)
	bad[headerLen+9] ^= 0xFF // first payload byte, after 9-byte block header
	return bad
}

// assertNoCompleteMarker verifies the target stream is rejected before
// io.EOF, i.e. it carries no valid complete marker.
func assertNoCompleteMarker(t *testing.T, bStream []byte) {
	t.Helper()
	var sink bytes.Buffer
	if _, _, err := Decode(&sink, bytes.NewReader(bStream), 1<<62); err == nil {
		t.Fatalf("target stream decoded cleanly; complete marker must not be published")
	}
}

func TestCorruptSourcePublishesNoCompleteMarker(t *testing.T) {
	data := bytes.Repeat([]byte("hello world "), 5000)
	aStream := encodeA(t, data, []byte(`{"k":"v"}`), 4096, 4096)
	bad := corruptPayload(t, aStream)

	var bStream bytes.Buffer
	if err := Transcode(&bStream, bytes.NewReader(bad)); err == nil {
		t.Fatal("Transcode succeeded on corrupt source")
	}
	assertNoCompleteMarker(t, bStream.Bytes())
}

func TestDeclaredLengthMismatchRejected(t *testing.T) {
	payload := []byte("12345")
	var buf bytes.Buffer
	// Header declares 10 bytes but only 5 arrive.
	aw, err := NewAWriter(&buf, nil, 10)
	if err != nil {
		t.Fatalf("NewAWriter: %v", err)
	}
	if err := aw.WriteBlock(payload); err != nil {
		t.Fatalf("WriteBlock: %v", err)
	}
	// Bypass AWriter.Finish's own guard and hand-craft the trailer.
	var tail [5]byte
	tail[0] = tagComplete
	binary.BigEndian.PutUint32(tail[1:5], crc32.ChecksumIEEE(payload))
	buf.Write(tail[:])

	var bStream bytes.Buffer
	err = Transcode(&bStream, bytes.NewReader(buf.Bytes()))
	if err == nil || !strings.Contains(err.Error(), "declared length") {
		t.Fatalf("expected declared-length rejection, got %v", err)
	}
	assertNoCompleteMarker(t, bStream.Bytes())
}

func TestTruncatedSourceRejected(t *testing.T) {
	data := bytes.Repeat([]byte("x"), 10000)
	aStream := encodeA(t, data, nil, 4096)
	truncated := aStream[:len(aStream)-3] // cut inside the complete marker

	var bStream bytes.Buffer
	if err := Transcode(&bStream, bytes.NewReader(truncated)); err == nil {
		t.Fatal("Transcode succeeded on truncated source")
	}
	assertNoCompleteMarker(t, bStream.Bytes())
}

func TestBadVersionRejected(t *testing.T) {
	data := []byte("some data")
	aStream := encodeA(t, data, nil, 4)
	aStream[4] = 99 // version byte
	var bStream bytes.Buffer
	if err := Transcode(&bStream, bytes.NewReader(aStream)); err == nil {
		t.Fatal("Transcode succeeded on unsupported version")
	}
}

func TestTranscodeFileFailureCleansUpTail(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "out.bkb")

	data := bytes.Repeat([]byte("cleanup-me "), 5000)
	aStream := encodeA(t, data, nil, 4096, 4096)
	bad := corruptPayload(t, aStream)

	if err := TranscodeFile(dst, bytes.NewReader(bad)); err == nil {
		t.Fatal("TranscodeFile succeeded on corrupt source")
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Fatalf("committed target must not exist, stat err=%v", err)
	}
	if _, err := os.Stat(dst + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("uncommitted tail %s.tmp was not cleaned up", dst)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("directory not clean: %v", entries)
	}
}

func TestTranscodeFileSuccessCommits(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "out.bkb")
	data := bytes.Repeat([]byte("ok "), 3000)
	aStream := encodeA(t, data, []byte(`{"ok":true}`), 1024)

	if err := TranscodeFile(dst, bytes.NewReader(aStream)); err != nil {
		t.Fatalf("TranscodeFile: %v", err)
	}
	if _, err := os.Stat(dst + ".tmp"); !os.IsNotExist(err) {
		t.Fatal("temp file left behind after successful commit")
	}
	f, err := os.Open(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out bytes.Buffer
	if _, _, err := Decode(&out, f, 1<<20); err != nil {
		t.Fatalf("Decode committed file: %v", err)
	}
	if !bytes.Equal(out.Bytes(), data) {
		t.Fatal("committed file round trip mismatch")
	}
}

func TestDecodeQuotaBounded(t *testing.T) {
	data := bytes.Repeat([]byte("quota"), 2000) // 10000 bytes
	aStream := encodeA(t, data, nil, 3000, 3000)
	var bStream bytes.Buffer
	if err := Transcode(&bStream, bytes.NewReader(aStream)); err != nil {
		t.Fatalf("Transcode: %v", err)
	}

	// Quota below declared length: rejected at the header, zero output.
	var out bytes.Buffer
	if _, _, err := Decode(&out, bytes.NewReader(bStream.Bytes()), uint64(len(data)-1)); err == nil {
		t.Fatal("Decode succeeded despite quota < declared length")
	}
	if out.Len() != 0 {
		t.Fatalf("quota exceeded: %d bytes emitted", out.Len())
	}

	// Quota equal to the length: succeeds and stays within the quota.
	out.Reset()
	_, n, err := Decode(&out, bytes.NewReader(bStream.Bytes()), uint64(len(data)))
	if err != nil {
		t.Fatalf("Decode with exact quota: %v", err)
	}
	if n != uint64(len(data)) || out.Len() != len(data) {
		t.Fatalf("got %d bytes, want %d", n, len(data))
	}
}

func TestDeclaredLengthOverflowRejected(t *testing.T) {
	// Craft a stream whose blocks sum to more than the declared length.
	var buf bytes.Buffer
	buf.WriteString(MagicA)
	buf.WriteByte(VersionA)
	var num [8]byte
	binary.BigEndian.PutUint64(num[:], 4) // declared 4
	buf.Write(num[:])
	binary.BigEndian.PutUint32(num[:4], 0) // no meta
	buf.Write(num[:4])
	payload := []byte("12345678") // 8 > 4
	buf.WriteByte(tagData)
	binary.BigEndian.PutUint32(num[:4], uint32(len(payload)))
	buf.Write(num[:4])
	binary.BigEndian.PutUint32(num[:4], crc32.ChecksumIEEE(payload))
	buf.Write(num[:4])
	buf.Write(payload)
	buf.WriteByte(tagComplete)
	binary.BigEndian.PutUint32(num[:4], crc32.ChecksumIEEE(payload))
	buf.Write(num[:4])

	var bStream bytes.Buffer
	if err := Transcode(&bStream, bytes.NewReader(buf.Bytes())); err == nil {
		t.Fatal("Transcode succeeded despite payload exceeding declared length")
	}
	assertNoCompleteMarker(t, bStream.Bytes())
}

func TestEmptyStreamRoundTrip(t *testing.T) {
	aStream := encodeA(t, nil, []byte(`{}`))
	var bStream bytes.Buffer
	if err := Transcode(&bStream, bytes.NewReader(aStream)); err != nil {
		t.Fatalf("Transcode: %v", err)
	}
	var out bytes.Buffer
	if _, n, err := Decode(&out, bytes.NewReader(bStream.Bytes()), 0); err != nil || n != 0 {
		t.Fatalf("Decode empty: n=%d err=%v", n, err)
	}
}

// failAfterWriter fails after n bytes to simulate a target write failure.
type failAfterWriter struct {
	n int
}

func (f *failAfterWriter) Write(p []byte) (int, error) {
	if f.n <= 0 {
		return 0, io.ErrClosedPipe
	}
	if len(p) > f.n {
		n := f.n
		f.n = 0
		return n, io.ErrClosedPipe
	}
	f.n -= len(p)
	return len(p), nil
}

func TestTargetWriteFailurePropagates(t *testing.T) {
	data := bytes.Repeat([]byte("w"), 20000)
	aStream := encodeA(t, data, nil, 4096)
	fw := &failAfterWriter{n: 100} // header fits, first blocks fail
	if err := Transcode(fw, bytes.NewReader(aStream)); err == nil {
		t.Fatal("Transcode succeeded despite target write failure")
	}
}
