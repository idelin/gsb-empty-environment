package deflate

import (
	"bytes"
	"compress/flate"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/ioutil"
	"math/rand"
	"testing"
	"time"
)

// generateLogs builds a deterministic synthetic system log of at least n
// bytes, full of repeated patterns (the acceptance workload).
func generateLogs(n int) []byte {
	levels := []string{"INFO", "WARN", "ERROR", "DEBUG"}
	hosts := []string{"web-01", "web-02", "db-01", "cache-01"}
	msgs := []string{
		"request completed status=200 duration_ms=12 path=/api/v1/users",
		"connection accepted from 10.0.0.14:53322 backlog=128",
		"query executed rows=128 plan=SeqScan table=events",
		"cache hit ratio=0.97 keys=1024 evictions=0",
		"retrying upstream attempt=2 backoff_ms=200 service=billing",
		"disk usage ok mount=/var/log used=61%",
	}
	var b bytes.Buffer
	rng := rand.New(rand.NewSource(42))
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for b.Len() < n {
		ts := base.Add(time.Duration(b.Len()) * time.Millisecond).Format("2006-01-02T15:04:05.000Z")
		fmt.Fprintf(&b, "%s %s %s %s\n", ts, levels[rng.Intn(len(levels))],
			hosts[rng.Intn(len(hosts))], msgs[rng.Intn(len(msgs))])
	}
	return b.Bytes()
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func roundTrip(t *testing.T, data []byte, opt Options) []byte {
	t.Helper()
	compressed := CompressWithOptions(data, opt)
	restored, err := Decompress(compressed)
	if err != nil {
		t.Fatalf("Decompress failed: %v", err)
	}
	if !bytes.Equal(restored, data) {
		t.Fatalf("round trip mismatch: got %d bytes, want %d", len(restored), len(data))
	}
	if sha256Hex(restored) != sha256Hex(data) {
		t.Fatal("SHA256 mismatch after round trip")
	}
	return compressed
}

func containsType(types []int, want int) bool {
	for _, ty := range types {
		if ty == want {
			return true
		}
	}
	return false
}

func TestRoundTripSystemLogs(t *testing.T) {
	data := generateLogs(2 << 20)
	compressed := roundTrip(t, data, Options{})

	types, err := BlockTypes(compressed)
	if err != nil {
		t.Fatalf("BlockTypes: %v", err)
	}
	t.Logf("logs: %d -> %d bytes (%.1fx), %d blocks, types=%v contains dynamic=%v",
		len(data), len(compressed), float64(len(data))/float64(len(compressed)),
		len(types), types[:1], containsType(types, 2))
	if !containsType(types, 2) {
		t.Error("expected dynamic huffman blocks for repetitive logs")
	}

	// Cross-check: the standard library must decode our stream identically.
	r := flate.NewReader(bytes.NewReader(compressed))
	stdOut, err := ioutil.ReadAll(r)
	if err != nil {
		t.Fatalf("stdlib flate decode failed: %v", err)
	}
	if !bytes.Equal(stdOut, data) {
		t.Fatal("stdlib flate decoded output differs")
	}
}

func TestStoredBlocksForRandomData(t *testing.T) {
	data := make([]byte, 300<<10)
	rand.New(rand.NewSource(7)).Read(data)
	compressed := roundTrip(t, data, Options{})

	types, err := BlockTypes(compressed)
	if err != nil {
		t.Fatalf("BlockTypes: %v", err)
	}
	if !containsType(types, 0) {
		t.Errorf("expected stored blocks for incompressible data, types=%v", types)
	}
	if len(compressed) > len(data)+len(data)/100+64 {
		t.Errorf("stored fallback too lossy: %d -> %d", len(data), len(compressed))
	}
}

func TestForcedBlockTypes(t *testing.T) {
	data := generateLogs(200 << 10)
	cases := []struct {
		name string
		opt  Options
		want int
	}{
		{"stored", Options{BlockType: BlockStored}, 0},
		{"static", Options{BlockType: BlockStatic}, 1},
		{"dynamic", Options{BlockType: BlockDynamic}, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			compressed := roundTrip(t, data, c.opt)
			types, err := BlockTypes(compressed)
			if err != nil {
				t.Fatalf("BlockTypes: %v", err)
			}
			for _, ty := range types {
				if ty != c.want {
					t.Fatalf("forced %s: got block type %d in %v", c.name, ty, types)
				}
			}
			// The standard library must agree on what we wrote.
			r := flate.NewReader(bytes.NewReader(compressed))
			stdOut, err := ioutil.ReadAll(r)
			if err != nil {
				t.Fatalf("stdlib flate decode failed: %v", err)
			}
			if !bytes.Equal(stdOut, data) {
				t.Fatal("stdlib flate decoded output differs")
			}
			t.Logf("%s: %d -> %d bytes over %d block(s)", c.name, len(data), len(compressed), len(types))
		})
	}
}

func TestEdgeCases(t *testing.T) {
	cases := []struct {
		name string
		data []byte
	}{
		{"empty", nil},
		{"one byte", []byte("x")},
		{"two bytes", []byte("ab")},
		{"three same", []byte("aaa")},
		{"all zeros", make([]byte, 1<<20)},
		{"all same byte", bytes.Repeat([]byte("Q"), 1<<20)},
		{"short repeat", bytes.Repeat([]byte("abc"), 100000)},
		{"text", []byte("the quick brown fox jumps over the lazy dog. ")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for _, bt := range []BlockType{BlockAuto, BlockStored, BlockStatic, BlockDynamic} {
				roundTrip(t, c.data, Options{BlockType: bt})
			}
		})
	}
}

func TestMultipleBlocks(t *testing.T) {
	data := generateLogs(3 << 20)
	compressed := roundTrip(t, data, Options{})
	types, err := BlockTypes(compressed)
	if err != nil {
		t.Fatalf("BlockTypes: %v", err)
	}
	if len(types) < 2 {
		t.Fatalf("expected multiple blocks for %d bytes, got %v", len(data), types)
	}
	t.Logf("%d blocks for %d input bytes", len(types), len(data))
}

// TestDecompressStdlibOutput feeds streams produced by the standard library
// (all compression levels, hence all block flavors) into our Decompress.
func TestDecompressStdlibOutput(t *testing.T) {
	data := generateLogs(1 << 20)
	levels := []int{flate.NoCompression, flate.BestSpeed, flate.DefaultCompression, flate.BestCompression, flate.HuffmanOnly}
	for _, level := range levels {
		t.Run(fmt.Sprintf("level=%d", level), func(t *testing.T) {
			var buf bytes.Buffer
			w, err := flate.NewWriter(&buf, level)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := w.Write(data); err != nil {
				t.Fatal(err)
			}
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}
			restored, err := Decompress(buf.Bytes())
			if err != nil {
				t.Fatalf("Decompress(stdlib level %d): %v", level, err)
			}
			if !bytes.Equal(restored, data) {
				t.Fatalf("stdlib level %d: output mismatch", level)
			}
			types, _ := BlockTypes(buf.Bytes())
			t.Logf("level %d: stdlib %d bytes, block types seen: %v", level, buf.Len(), types[:1])
		})
	}
}

func TestCorruptStreams(t *testing.T) {
	if _, err := Decompress([]byte{0x07, 0x00, 0x00}); err == nil {
		t.Error("expected error for reserved block type 3")
	}
	if _, err := Decompress([]byte{}); err == nil {
		t.Error("expected error for empty stream")
	}
	// Stored block with bad NLEN complement.
	if _, err := Decompress([]byte{0x01, 0x05, 0x00, 0x00, 0x00}); err == nil {
		t.Error("expected error for bad stored NLEN")
	}
	// Truncated dynamic stream.
	full := Compress(generateLogs(100 << 10))
	if _, err := Decompress(full[:len(full)/2]); err == nil {
		t.Error("expected error for truncated stream")
	}
}

// BenchmarkCompressLogs gives a rough sense of throughput on the log workload.
func BenchmarkCompressLogs(b *testing.B) {
	data := generateLogs(1 << 20)
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Compress(data)
	}
}

func BenchmarkDecompressLogs(b *testing.B) {
	compressed := Compress(generateLogs(1 << 20))
	b.SetBytes(int64(len(compressed)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Decompress(compressed); err != nil {
			b.Fatal(err)
		}
	}
}
