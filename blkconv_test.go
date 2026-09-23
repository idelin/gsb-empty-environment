package blkconv

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// sampleData builds a compressibility mix: runs, random noise, text.
func sampleData(rng *rand.Rand, n int) []byte {
	var b []byte
	for len(b) < n {
		switch rng.Intn(3) {
		case 0: // repeated-byte run
			v := byte(rng.Intn(256))
			for i, l := 0, 1+rng.Intn(300); i < l; i++ {
				b = append(b, v)
			}
		case 1: // noise
			for i, l := 0, 1+rng.Intn(200); i < l; i++ {
				b = append(b, byte(rng.Intn(256)))
			}
		case 2: // text
			s := "the quick brown fox jumps over the lazy dog. "
			b = append(b, s[:1+rng.Intn(len(s))]...)
		}
	}
	return b[:n]
}

// chunkReader delivers random-sized reads.
type chunkReader struct {
	r   io.Reader
	rng *rand.Rand
}

func (c *chunkReader) Read(p []byte) (int, error) {
	if m := 1 + c.rng.Intn(4096); m < len(p) {
		p = p[:m]
	}
	return c.r.Read(p)
}

func writeRandomChunks(t *testing.T, enc *Encoder, data []byte, rng *rand.Rand) {
	t.Helper()
	for len(data) > 0 {
		n := 1 + rng.Intn(2*BlockSize)
		if n > len(data) {
			n = len(data)
		}
		if _, err := enc.Write(data[:n]); err != nil {
			t.Fatalf("write: %v", err)
		}
		data = data[n:]
	}
	if err := enc.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}

func packBytes(t *testing.T, f Format, data []byte, meta map[string]string, rng *rand.Rand) []byte {
	t.Helper()
	var buf bytes.Buffer
	enc, err := NewEncoder(&buf, f, meta, uint64(len(data)), sha256.Sum256(data))
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	writeRandomChunks(t, enc, data, rng)
	return buf.Bytes()
}

func TestRLECodecsRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 300; i++ {
		data := sampleData(rng, rng.Intn(5000))
		for _, f := range []Format{FormatA, FormatB} {
			enc := encodeBlock(f, nil, data)
			dec, err := decodeBlock(f, nil, enc, len(data))
			if err != nil {
				t.Fatalf("format %s iter %d: %v", f, i, err)
			}
			if !bytes.Equal(dec, data) {
				t.Fatalf("format %s iter %d: round trip mismatch", f, i)
			}
		}
	}
}

func TestStreamRoundTripBothFormats(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	data := sampleData(rng, 1<<20+777)
	meta := map[string]string{"name": "roundtrip", "run": "2"}
	for _, f := range []Format{FormatA, FormatB} {
		raw := packBytes(t, f, data, meta, rng)
		dec, err := NewDecoder(&chunkReader{r: bytes.NewReader(raw), rng: rng}, 0)
		if err != nil {
			t.Fatalf("format %s: %v", f, err)
		}
		got, err := io.ReadAll(dec)
		if err != nil {
			t.Fatalf("format %s: %v", f, err)
		}
		if !bytes.Equal(got, data) {
			t.Fatalf("format %s: decoded bytes differ", f)
		}
		h := dec.Header()
		if h.OrigLen != uint64(len(data)) || h.Digest != sha256.Sum256(data) {
			t.Fatalf("format %s: header not preserved", f)
		}
		if !reflect.DeepEqual(h.Meta, meta) {
			t.Fatalf("format %s: meta = %v", f, h.Meta)
		}
	}
}

func TestTranscodeRandomChunksDecodeEquals(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	data := sampleData(rng, 1<<20+12345)
	meta := map[string]string{"name": "random-chunks", "run": "42"}

	for _, dir := range []struct {
		from, to Format
	}{{FormatA, FormatB}, {FormatB, FormatA}} {
		src := packBytes(t, dir.from, data, meta, rng)
		var out bytes.Buffer
		in := &chunkReader{r: bytes.NewReader(src), rng: rng}
		if err := Transcode(in, dir.to, &out, 0); err != nil {
			t.Fatalf("%s->%s: %v", dir.from, dir.to, err)
		}
		dec, err := NewDecoder(bytes.NewReader(out.Bytes()), 0)
		if err != nil {
			t.Fatalf("%s->%s: %v", dir.from, dir.to, err)
		}
		got, err := io.ReadAll(dec)
		if err != nil {
			t.Fatalf("%s->%s: %v", dir.from, dir.to, err)
		}
		if !bytes.Equal(got, data) {
			t.Fatalf("%s->%s: decoded output differs from original", dir.from, dir.to)
		}
		h := dec.Header()
		if h.Format != dir.to {
			t.Fatalf("target format = %s, want %s", h.Format, dir.to)
		}
		if h.OrigLen != uint64(len(data)) || h.Digest != sha256.Sum256(data) {
			t.Fatalf("%s->%s: digest/length not preserved", dir.from, dir.to)
		}
		if !reflect.DeepEqual(h.Meta, meta) {
			t.Fatalf("%s->%s: meta = %v", dir.from, dir.to, h.Meta)
		}
	}
}

func TestCorruptPayloadRejected(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	data := sampleData(rng, 200000)
	raw := packBytes(t, FormatA, data, nil, rng)
	// First payload byte: fixed header (48) + meta "{}" (2) + tag (1) + A block header (12).
	raw[48+2+1+12] ^= 0xFF

	if _, err := NewDecoder(bytes.NewReader(raw), 0); err != nil {
		t.Fatalf("header should parse: %v", err)
	}
	dec, _ := NewDecoder(bytes.NewReader(raw), 0)
	if _, err := io.ReadAll(dec); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("decode: got %v, want ErrCorrupt", err)
	}
	var out bytes.Buffer
	if err := Transcode(bytes.NewReader(raw), FormatB, &out, 0); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("transcode: got %v, want ErrCorrupt", err)
	}
}

func TestTruncationRejectedNoCommit(t *testing.T) {
	rng := rand.New(rand.NewSource(4))
	data := sampleData(rng, 100000)
	raw := packBytes(t, FormatA, data, nil, rng)

	for _, cut := range []int{13, 5, len(raw) / 2} { // whole trailer, partial trailer, mid-stream
		trunc := raw[:len(raw)-cut]
		dec, err := NewDecoder(bytes.NewReader(trunc), 0)
		if err != nil {
			t.Fatalf("cut %d: header should parse: %v", cut, err)
		}
		if _, err := io.ReadAll(dec); !errors.Is(err, ErrNoCommit) {
			t.Fatalf("cut %d: got %v, want ErrNoCommit", cut, err)
		}
	}
}

// fixSumA recomputes the trailing CRC-32 after tampering with an A stream.
func fixSumA(raw []byte) {
	binary.BigEndian.PutUint32(raw[len(raw)-4:], crc32.ChecksumIEEE(raw[:len(raw)-4]))
}

func TestDeclaredLengthMismatchRejected(t *testing.T) {
	rng := rand.New(rand.NewSource(5))
	data := sampleData(rng, 150000)

	// Decoder side: header lies about origLen (stream checksum repaired).
	raw := packBytes(t, FormatA, data, nil, rng)
	binary.BigEndian.PutUint64(raw[8:16], uint64(len(data))+1)
	fixSumA(raw)
	dec, err := NewDecoder(bytes.NewReader(raw), 0)
	if err != nil {
		t.Fatalf("header should parse: %v", err)
	}
	if _, err := io.ReadAll(dec); !errors.Is(err, ErrLengthMismatch) {
		t.Fatalf("decode: got %v, want ErrLengthMismatch", err)
	}
	var out bytes.Buffer
	if err := Transcode(bytes.NewReader(raw), FormatB, &out, 0); !errors.Is(err, ErrLengthMismatch) {
		t.Fatalf("transcode: got %v, want ErrLengthMismatch", err)
	}

	// Encoder side: declared length does not match actual bytes written.
	var buf bytes.Buffer
	enc, err := NewEncoder(&buf, FormatA, nil, uint64(len(data))+1, sha256.Sum256(data))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := enc.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := enc.Close(); !errors.Is(err, ErrLengthMismatch) {
		t.Fatalf("encoder close: got %v, want ErrLengthMismatch", err)
	}
}

func TestDigestLieRejected(t *testing.T) {
	rng := rand.New(rand.NewSource(6))
	data := sampleData(rng, 120000)
	raw := packBytes(t, FormatA, data, nil, rng)
	raw[16] ^= 0xFF // corrupt declared digest, repair stream checksum
	fixSumA(raw)
	dec, err := NewDecoder(bytes.NewReader(raw), 0)
	if err != nil {
		t.Fatalf("header should parse: %v", err)
	}
	if _, err := io.ReadAll(dec); !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("got %v, want ErrDigestMismatch", err)
	}
}

func TestDecodeBudgetBounded(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	data := sampleData(rng, 300000)
	raw := packBytes(t, FormatB, data, nil, rng)
	dec, err := NewDecoder(bytes.NewReader(raw), 100000)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(dec); !errors.Is(err, ErrBudget) {
		t.Fatalf("got %v, want ErrBudget", err)
	}
	// And transcode honors the same budget.
	var out bytes.Buffer
	if err := Transcode(bytes.NewReader(raw), FormatA, &out, 100000); !errors.Is(err, ErrBudget) {
		t.Fatalf("transcode: got %v, want ErrBudget", err)
	}
}

// failWriter fails writes once limit bytes have been accepted.
type failWriter struct {
	buf   bytes.Buffer
	limit int
}

func (f *failWriter) Write(p []byte) (int, error) {
	if f.buf.Len()+len(p) > f.limit {
		return 0, errors.New("injected write failure (disk full)")
	}
	return f.buf.Write(p)
}

func TestWriteFailureCleansUncommittedTail(t *testing.T) {
	rng := rand.New(rand.NewSource(8))
	data := sampleData(rng, 200000)
	src := packBytes(t, FormatA, data, nil, rng)

	var committed, aborted bool
	fw := &failWriter{limit: 100} // header fits, first block write fails
	target := &CommitTarget{
		W:      fw,
		Commit: func() error { committed = true; return nil },
		Abort:  func() { aborted = true },
	}
	if err := TranscodeToTarget(bytes.NewReader(src), FormatB, target, 0); err == nil {
		t.Fatal("expected write failure")
	}
	if committed {
		t.Fatal("commit marker published despite write failure")
	}
	if !aborted {
		t.Fatal("uncommitted tail not aborted after write failure")
	}

	// Sanity: with room to write, the same target commits a valid stream.
	committed, aborted = false, false
	fw = &failWriter{limit: 1 << 30}
	target.W = fw
	if err := TranscodeToTarget(bytes.NewReader(src), FormatB, target, 0); err != nil {
		t.Fatal(err)
	}
	if !committed || aborted {
		t.Fatalf("committed=%v aborted=%v", committed, aborted)
	}
	dec, err := NewDecoder(bytes.NewReader(fw.buf.Bytes()), 0)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(dec)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("committed stream invalid: %v", err)
	}
}

func TestFileTargetCleanupOnBadInput(t *testing.T) {
	rng := rand.New(rand.NewSource(9))
	data := sampleData(rng, 200000)
	raw := packBytes(t, FormatA, data, nil, rng)
	raw[48+2+1+12] ^= 0xFF // corrupt first payload byte

	dir := t.TempDir()
	srcPath := filepath.Join(dir, "bad.blka")
	dstPath := filepath.Join(dir, "out.blkb")
	if err := os.WriteFile(srcPath, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := TranscodeFile(srcPath, dstPath, FormatB, 0); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("got %v, want ErrCorrupt", err)
	}
	if _, err := os.Stat(dstPath); !os.IsNotExist(err) {
		t.Fatal("final output must not exist after failed transcode")
	}
	if _, err := os.Stat(dstPath + ".part"); !os.IsNotExist(err) {
		t.Fatal("uncommitted tail (.part) was not cleaned up")
	}
}

func TestFileRoundTripAndCleanupOnSuccess(t *testing.T) {
	rng := rand.New(rand.NewSource(10))
	data := sampleData(rng, 1<<20+999)
	dir := t.TempDir()
	plain := filepath.Join(dir, "plain.bin")
	aPath := filepath.Join(dir, "data.blka")
	bPath := filepath.Join(dir, "data.blkb")
	back := filepath.Join(dir, "back.bin")
	if err := os.WriteFile(plain, data, 0o644); err != nil {
		t.Fatal(err)
	}
	meta := map[string]string{"origin": "test"}
	if _, err := PackFile(plain, aPath, FormatA, meta); err != nil {
		t.Fatal(err)
	}
	if _, err := TranscodeFile(aPath, bPath, FormatB, 0); err != nil {
		t.Fatal(err)
	}
	h, err := UnpackFile(bPath, back, 0)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(back)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) {
		t.Fatal("file round trip mismatch")
	}
	if !reflect.DeepEqual(h.Meta, meta) {
		t.Fatalf("meta = %v", h.Meta)
	}
	for _, p := range []string{aPath, bPath, back} {
		if _, err := os.Stat(p + ".part"); !os.IsNotExist(err) {
			t.Fatalf("stale temp file for %s", p)
		}
	}
}

func TestEmptyStream(t *testing.T) {
	for _, f := range []Format{FormatA, FormatB} {
		var buf bytes.Buffer
		enc, err := NewEncoder(&buf, f, nil, 0, sha256.Sum256(nil))
		if err != nil {
			t.Fatal(err)
		}
		if err := enc.Close(); err != nil {
			t.Fatal(err)
		}
		dec, err := NewDecoder(bytes.NewReader(buf.Bytes()), 0)
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(dec)
		if err != nil || len(got) != 0 {
			t.Fatalf("format %s: got %d bytes, %v", f, len(got), err)
		}
	}
}

// TestWorstCaseExpansionAccepted pins the per-format wire bound: a
// run(2)+literal(1) pattern is the worst case for RLE-B (4/3 expansion).
func TestWorstCaseExpansionAccepted(t *testing.T) {
	var data []byte
	for len(data) < 3*BlockSize+100 {
		data = append(data, 0x00, 0x00, 0x01) // 2-run + singleton literal
	}
	enc := encodeBlock(FormatB, nil, data[:BlockSize])
	if uint64(len(enc)) > maxWireLen(FormatB) {
		t.Fatalf("worst-case block %d exceeds bound %d", len(enc), maxWireLen(FormatB))
	}
	rng := rand.New(rand.NewSource(11))
	raw := packBytes(t, FormatB, data, nil, rng)
	dec, err := NewDecoder(bytes.NewReader(raw), 0)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(dec)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) {
		t.Fatal("worst-case stream round trip mismatch")
	}
}
