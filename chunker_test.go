package cdcupload

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"math/rand"
	"testing"
)

func testConfig() ChunkConfig {
	// Small but realistic bounds so a 256 KiB fixture produces many chunks.
	return ChunkConfig{MinSize: 1024, AvgSize: 4096, MaxSize: 16384}
}

func chunkAll(t *testing.T, data []byte, cfg ChunkConfig) []Chunk {
	t.Helper()
	c, err := NewChunker(bytes.NewReader(data), cfg)
	if err != nil {
		t.Fatalf("NewChunker: %v", err)
	}
	var got []Chunk
	for {
		ch, payload, err := c.NextChunk()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("NextChunk: %v", err)
		}
		if !bytes.Equal(payload, data[ch.Offset:ch.Offset+int64(ch.Length)]) {
			t.Fatalf("chunk %d payload mismatch", ch.Index)
		}
		if len(payload) != ch.Length {
			t.Fatalf("chunk %d length mismatch", ch.Index)
		}
		got = append(got, ch)
	}
	return got
}

func TestChunkerBoundsAndDeterminism(t *testing.T) {
	cfg := testConfig()
	rng := rand.New(rand.NewSource(42))
	data := make([]byte, 256*1024)
	rng.Read(data)

	chunks := chunkAll(t, data, cfg)

	var total int64
	for _, ch := range chunks {
		if ch.Length < cfg.MinSize && ch.Index != int64(len(chunks)-1) {
			t.Fatalf("chunk %d shorter than MinSize: %d", ch.Index, ch.Length)
		}
		if ch.Length > cfg.MaxSize {
			t.Fatalf("chunk %d exceeds MaxSize: %d > %d", ch.Index, ch.Length, cfg.MaxSize)
		}
		total += int64(ch.Length)
	}
	if total != int64(len(data)) {
		t.Fatalf("chunks cover %d bytes, want %d", total, len(data))
	}

	// Average should be in the same order of magnitude as AvgSize.
	avg := float64(len(data)) / float64(len(chunks))
	if avg < float64(cfg.AvgSize)/2 || avg > float64(cfg.AvgSize)*2 {
		t.Fatalf("average chunk size %.1f far from target %d", avg, cfg.AvgSize)
	}

	// Deterministic: same input must yield identical boundaries and digests.
	again := chunkAll(t, data, cfg)
	if len(again) != len(chunks) {
		t.Fatalf("non-deterministic chunk count: %d vs %d", len(again), len(chunks))
	}
	for i := range chunks {
		if chunks[i].Index != again[i].Index ||
			chunks[i].Offset != again[i].Offset ||
			chunks[i].Length != again[i].Length ||
			!bytes.Equal(chunks[i].Digest, again[i].Digest) {
			t.Fatalf("chunk %d differs across runs: %+v vs %+v", i, chunks[i], again[i])
		}
	}

	// FileSum must equal the digest of the whole input.
	c, _ := NewChunker(bytes.NewReader(data), cfg)
	for {
		if _, _, err := c.NextChunk(); err == io.EOF {
			break
		}
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(c.FileSum()) != hex.EncodeToString(sum[:]) {
		t.Fatal("whole-file digest mismatch")
	}
}

func digestSet(chunks []Chunk) map[string]int {
	m := make(map[string]int)
	for _, ch := range chunks {
		m[hex.EncodeToString(ch.Digest)]++
	}
	return m
}

// reuseRate is the symmetric overlap of chunk digests between two versions.
func reuseRate(a, b []Chunk) float64 {
	da, db := digestSet(a), digestSet(b)
	var shared int
	for digest, n := range da {
		if k := db[digest]; k < n {
			shared += k
		} else {
			shared += n
		}
	}
	return float64(shared) / float64(len(b))
}

func insertAtRandom(data []byte, rng *rand.Rand, n int) []byte {
	pos := rng.Intn(len(data) + 1)
	insert := make([]byte, n)
	rng.Read(insert)
	out := make([]byte, 0, len(data)+n)
	out = append(out, data[:pos]...)
	out = append(out, insert...)
	out = append(out, data[pos:]...)
	return out
}

func deleteAtRandom(data []byte, rng *rand.Rand, n int) []byte {
	pos := rng.Intn(len(data) - n)
	out := make([]byte, 0, len(data)-n)
	out = append(out, data[:pos]...)
	out = append(out, data[pos+n:]...)
	return out
}

func TestInsertionDeletionLocality(t *testing.T) {
	cfg := testConfig()
	rng := rand.New(rand.NewSource(7))
	base := make([]byte, 256*1024)
	rng.Read(base)
	baseChunks := chunkAll(t, base, cfg)

	const mutations = 10
	for i := 0; i < mutations; i++ {
		// Insert a small blob (smaller than MinSize) at a random position.
		inserted := insertAtRandom(base, rng, 256)
		insChunks := chunkAll(t, inserted, cfg)

		rate := reuseRate(baseChunks, insChunks)
		if rate < 0.9 {
			t.Fatalf("insertion %d reuse rate too low: %.3f", i, rate)
		}
		// A single local edit must only disturb a handful of nearby chunks.
		changed := len(insChunks) + len(baseChunks) - 2*sharedChunkCount(baseChunks, insChunks)
		if changed > 4 {
			t.Fatalf("insertion %d changed %d chunks, expected <= 4", i, changed)
		}

		// Chunks before the edit keep identical offsets, lengths and digests.
		editPos := firstOffsetDifference(baseChunks, insChunks)
		if editPos > 0 {
			for j := 0; j < editPos; j++ {
				if baseChunks[j].Offset != insChunks[j].Offset ||
					baseChunks[j].Length != insChunks[j].Length ||
					!bytes.Equal(baseChunks[j].Digest, insChunks[j].Digest) {
					t.Fatalf("prefix chunk %d changed despite edit later in file", j)
				}
			}
		}

		// Same expectations for a random deletion.
		deleted := deleteAtRandom(base, rng, 256)
		delChunks := chunkAll(t, deleted, cfg)
		if rate := reuseRate(baseChunks, delChunks); rate < 0.9 {
			t.Fatalf("deletion %d reuse rate too low: %.3f", i, rate)
		}
		if changed := len(delChunks) + len(baseChunks) - 2*sharedChunkCount(baseChunks, delChunks); changed > 4 {
			t.Fatalf("deletion %d changed %d chunks, expected <= 4", i, changed)
		}
	}
}

func sharedChunkCount(a, b []Chunk) int {
	da, db := digestSet(a), digestSet(b)
	var shared int
	for digest, n := range da {
		if k := db[digest]; k < n {
			shared += k
		} else {
			shared += n
		}
	}
	return shared
}

func firstOffsetDifference(a, b []Chunk) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i].Offset != b[i].Offset || !bytes.Equal(a[i].Digest, b[i].Digest) {
			return i
		}
	}
	return n
}

func TestConfigValidation(t *testing.T) {
	cases := []ChunkConfig{
		{MinSize: 8, AvgSize: 64, MaxSize: 128},
		{MinSize: 1024, AvgSize: 512, MaxSize: 2048},
		{MinSize: 1024, AvgSize: 4096, MaxSize: 2048},
	}
	for _, cfg := range cases {
		if err := cfg.Validate(); err == nil {
			t.Fatalf("expected error for %+v", cfg)
		}
	}
	if err := DefaultChunkConfig().Validate(); err != nil {
		t.Fatalf("default config invalid: %v", err)
	}
}
