package deflate

import (
	"bytes"
	"compress/flate"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"math/rand"
	"testing"
)

// makeLogs generates deterministic system-log-like data with heavy
// repetition: a small pool of levels, hosts, services and messages.
func makeLogs(n int) []byte {
	rng := rand.New(rand.NewSource(42))
	levels := []string{"INFO", "WARN", "ERROR", "DEBUG"}
	hosts := []string{"web-01", "web-02", "db-01", "cache-01", "worker-03"}
	svc := []string{"auth", "billing", "search", "upload", "notify"}
	msg := []string{
		"request completed status=200",
		"connection reset by peer",
		"slow query detected duration=812ms",
		"cache hit ratio=0.97",
		"retrying upstream attempt=2",
		"disk usage above threshold usage=91%",
	}
	var b bytes.Buffer
	ts := int64(1759900000)
	for b.Len() < n {
		ts += int64(rng.Intn(7) + 1)
		fmt.Fprintf(&b, "2026-10-02T08:%02d:%02dZ %-5s host=%s service=%s pid=%d msg=\"%s\" trace_id=%08x\n",
			(ts/60)%60, ts%60,
			levels[rng.Intn(len(levels))],
			hosts[rng.Intn(len(hosts))],
			svc[rng.Intn(len(svc))],
			1000+rng.Intn(9000),
			msg[rng.Intn(len(msg))],
			rng.Uint32())
	}
	return b.Bytes()[:n]
}

func sha256hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// firstBlockType returns BTYPE of the first block in a deflate stream.
func firstBlockType(t *testing.T, data []byte) int {
	t.Helper()
	if len(data) == 0 {
		t.Fatal("empty stream")
	}
	return int((data[0] >> 1) & 0x3)
}

func roundTrip(t *testing.T, data []byte) []byte {
	t.Helper()
	compressed := Compress(data)
	got, err := Decompress(compressed)
	if err != nil {
		t.Fatalf("Decompress: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("round trip mismatch: got %d bytes, want %d", len(got), len(data))
	}
	return compressed
}

func TestRoundTripRepetitiveLogs(t *testing.T) {
	logs := makeLogs(1 << 20) // 1 MiB of repetitive logs
	compressed := roundTrip(t, logs)
	t.Logf("logs: %d -> %d bytes (%.1f%%), sha256=%s",
		len(logs), len(compressed),
		100*float64(len(compressed))/float64(len(logs)),
		sha256hex(logs)[:16])
	if got := sha256hex(logs); got != sha256hex(mustDecompress(t, compressed)) {
		t.Fatal("sha256 mismatch")
	}
	// Cross-validate with the standard library decoder.
	zr := flate.NewReader(bytes.NewReader(compressed))
	stdOut, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("stdlib flate decode: %v", err)
	}
	if !bytes.Equal(stdOut, logs) {
		t.Fatal("stdlib flate decode mismatch")
	}
}

func mustDecompress(t *testing.T, data []byte) []byte {
	t.Helper()
	out, err := Decompress(data)
	if err != nil {
		t.Fatalf("Decompress: %v", err)
	}
	return out
}

func TestStoredBlock(t *testing.T) {
	// Incompressible random data must fall back to stored blocks.
	rng := rand.New(rand.NewSource(7))
	data := make([]byte, 100000)
	rng.Read(data)
	compressed := roundTrip(t, data)
	if bt := firstBlockType(t, compressed); bt != 0 {
		t.Fatalf("BTYPE = %d, want 0 (stored)", bt)
	}
}

func TestStaticBlock(t *testing.T) {
	// Small inputs are cheapest with the fixed Huffman tables.
	data := []byte("hello world hello hello hello")
	compressed := roundTrip(t, data)
	if bt := firstBlockType(t, compressed); bt != 1 {
		t.Fatalf("BTYPE = %d, want 1 (static)", bt)
	}
}

func TestDynamicBlock(t *testing.T) {
	data := makeLogs(200000)
	compressed := roundTrip(t, data)
	if bt := firstBlockType(t, compressed); bt != 2 {
		t.Fatalf("BTYPE = %d, want 2 (dynamic)", bt)
	}
}

// TestForcedStaticDecode feeds the decoder a hand-built static block,
// independent of the compressor's block-type heuristics.
func TestForcedStaticDecode(t *testing.T) {
	data := []byte("forced static block: abcabcabc abcabcabc")
	tokens := lz77Encode(data)
	var w bitWriter
	w.writeBits(1, 1)
	w.writeBits(1, 2)
	writeTokens(&w, tokens, staticLitLengths, staticLitCodes, staticDistLengths, staticDistCodes)
	got, err := Decompress(w.bytes())
	if err != nil {
		t.Fatalf("Decompress: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatal("static block decode mismatch")
	}
}

// TestForcedStoredDecode feeds the decoder a hand-built stored block.
func TestForcedStoredDecode(t *testing.T) {
	data := []byte("forced stored block payload")
	var w bitWriter
	w.writeBits(1, 1)
	w.writeBits(0, 2)
	w.alignByte()
	n := len(data)
	w.writeBytes([]byte{byte(n), byte(n >> 8), byte(^n), byte(^n >> 8)})
	w.writeBytes(data)
	got, err := Decompress(w.bytes())
	if err != nil {
		t.Fatalf("Decompress: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatal("stored block decode mismatch")
	}
}

func TestEmptyInput(t *testing.T) {
	compressed := Compress(nil)
	got, err := Decompress(compressed)
	if err != nil {
		t.Fatalf("Decompress: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d bytes, want 0", len(got))
	}
}

func TestSizes(t *testing.T) {
	logs := makeLogs(300000)
	for _, n := range []int{1, 2, 3, 255, 256, 257, 258, 259, 1000, 65534, 65535, 65536, 200000, 300000} {
		data := logs[:n]
		compressed := Compress(data)
		got, err := Decompress(compressed)
		if err != nil {
			t.Fatalf("n=%d: Decompress: %v", n, err)
		}
		if !bytes.Equal(got, data) {
			t.Fatalf("n=%d: round trip mismatch", n)
		}
		if sha256hex(got) != sha256hex(data) {
			t.Fatalf("n=%d: sha256 mismatch", n)
		}
	}
}

// TestDecodeStdlibFlate verifies our decoder against streams produced
// by the standard library at several compression levels (these exercise
// dynamic, static and stored blocks from a reference encoder).
func TestDecodeStdlibFlate(t *testing.T) {
	logs := makeLogs(150000)
	rng := rand.New(rand.NewSource(99))
	random := make([]byte, 50000)
	rng.Read(random)
	inputs := map[string][]byte{
		"logs":   logs,
		"random": random,
		"tiny":   []byte("hi"),
	}
	levels := map[string]int{"fast": flate.BestSpeed, "default": flate.DefaultCompression, "best": flate.BestCompression}
	for iname, data := range inputs {
		for lname, level := range levels {
			var buf bytes.Buffer
			zw, err := flate.NewWriter(&buf, level)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := zw.Write(data); err != nil {
				t.Fatal(err)
			}
			if err := zw.Close(); err != nil {
				t.Fatal(err)
			}
			got, err := Decompress(buf.Bytes())
			if err != nil {
				t.Fatalf("%s/%s: Decompress: %v", iname, lname, err)
			}
			if !bytes.Equal(got, data) {
				t.Fatalf("%s/%s: mismatch", iname, lname)
			}
		}
	}
}

func TestCorruptInput(t *testing.T) {
	if _, err := Decompress([]byte{0xff, 0xff, 0xff}); err == nil {
		t.Fatal("expected error for garbage input")
	}
	// Truncated stream.
	compressed := Compress(makeLogs(50000))
	if _, err := Decompress(compressed[:len(compressed)/2]); err == nil {
		t.Fatal("expected error for truncated input")
	}
}

func BenchmarkCompressLogs(b *testing.B) {
	logs := makeLogs(1 << 20)
	b.SetBytes(int64(len(logs)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Compress(logs)
	}
}

func BenchmarkDecompressLogs(b *testing.B) {
	logs := makeLogs(1 << 20)
	compressed := Compress(logs)
	b.SetBytes(int64(len(logs)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Decompress(compressed); err != nil {
			b.Fatal(err)
		}
	}
}
