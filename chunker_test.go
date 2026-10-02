package cdc

import (
	"math/rand"
	"testing"
)

// deterministicData returns n bytes of pseudo-random data reproducible
// across runs.
func deterministicData(n int, seed int64) []byte {
	r := rand.New(rand.NewSource(seed))
	data := make([]byte, n)
	r.Read(data)
	return data
}

func insertAt(data []byte, pos int, ins []byte) []byte {
	out := make([]byte, 0, len(data)+len(ins))
	out = append(out, data[:pos]...)
	out = append(out, ins...)
	out = append(out, data[pos:]...)
	return out
}

func TestSplitCoversInputExactly(t *testing.T) {
	data := deterministicData(1<<20, 1)
	chunks := DefaultChunker().Split(data)
	if len(chunks) == 0 {
		t.Fatal("no chunks produced")
	}
	offset := 0
	for _, ch := range chunks {
		if ch.Offset != offset {
			t.Fatalf("gap or overlap at offset %d, chunk starts at %d", offset, ch.Offset)
		}
		offset += ch.Length
	}
	if offset != len(data) {
		t.Fatalf("chunks cover %d bytes, input has %d", offset, len(data))
	}
}

func TestChunkSizeBounds(t *testing.T) {
	data := deterministicData(4<<20, 2)
	c := DefaultChunker()
	for _, ch := range c.Split(data) {
		if ch.Length > DefaultMaxSize {
			t.Fatalf("chunk of %d bytes exceeds max %d", ch.Length, DefaultMaxSize)
		}
	}
	// All chunks except possibly the final one must respect minSize.
	chunks := c.Split(data)
	for _, ch := range chunks[:len(chunks)-1] {
		if ch.Length < DefaultMinSize {
			t.Fatalf("chunk of %d bytes below min %d", ch.Length, DefaultMinSize)
		}
	}
}

func TestSplitDeterministic(t *testing.T) {
	data := deterministicData(1<<20, 3)
	a := DefaultChunker().Split(data)
	b := DefaultChunker().Split(data)
	if len(a) != len(b) {
		t.Fatalf("chunk counts differ: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("chunk %d differs across runs", i)
		}
	}
}

// TestInsertMiddleLocalizesChanges is the acceptance test: insert 100
// bytes in the middle of a 10 MB file and verify that at least 99% of
// the new stream is covered by chunks whose hashes already exist in
// the old tree, and that only a handful of chunks actually changed.
func TestInsertMiddleLocalizesChanges(t *testing.T) {
	const fileSize = 10 << 20 // 10 MiB
	const insertSize = 100

	oldData := deterministicData(fileSize, 42)
	insert := deterministicData(insertSize, 43)
	newData := insertAt(oldData, fileSize/2, insert)

	chunker := DefaultChunker()
	oldChunks := chunker.Split(oldData)
	newChunks := chunker.Split(newData)

	oldTree := BuildTree(Hashes(oldChunks))
	newTree := BuildTree(Hashes(newChunks))

	if oldTree.Root() == newTree.Root() {
		t.Fatal("merkle root unchanged after insertion")
	}

	changed, reusedBytes := Diff(oldChunks, newChunks)
	reuseRatio := float64(reusedBytes) / float64(len(newData))
	t.Logf("old chunks: %d, new chunks: %d", len(oldChunks), len(newChunks))
	t.Logf("changed chunks: %d (%d bytes to transfer)", len(changed), totalLen(changed))
	t.Logf("reused: %d of %d bytes (%.4f%%)", reusedBytes, len(newData), reuseRatio*100)

	if reuseRatio < 0.99 {
		t.Fatalf("hash reuse %.4f%% below required 99%%", reuseRatio*100)
	}
	if len(changed) > 4 {
		t.Fatalf("%d chunks changed, expected only 1-2 local chunks", len(changed))
	}

	// Every changed chunk must sit near the insertion point: the edit
	// may shift the boundaries of at most the surrounding chunks.
	mid := fileSize / 2
	for _, ch := range changed {
		if ch.Offset > mid+DefaultMaxSize || ch.Offset+ch.Length < mid-DefaultMaxSize {
			t.Fatalf("changed chunk at [%d,%d) is not local to insertion at %d",
				ch.Offset, ch.Offset+ch.Length, mid)
		}
	}

	// The merkle proof of a changed chunk must verify against the new
	// root, demonstrating the incremental sync path: ship the changed
	// chunk plus its authentication path.
	for _, ch := range changed {
		idx := indexOfHash(newChunks, ch.Hash)
		if idx < 0 {
			t.Fatalf("changed chunk not found in new chunk list")
		}
		if !VerifyProof(ch.Hash, newTree.Proof(idx), newTree.Root()) {
			t.Fatalf("merkle proof for changed chunk at index %d does not verify", idx)
		}
	}
}

func totalLen(chunks []Chunk) int {
	n := 0
	for _, ch := range chunks {
		n += ch.Length
	}
	return n
}

func indexOfHash(chunks []Chunk, h [32]byte) int {
	for i, ch := range chunks {
		if ch.Hash == h {
			return i
		}
	}
	return -1
}
