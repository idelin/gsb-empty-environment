package cdc

import (
	"bytes"
	"crypto/sha256"
	"math/rand"
	"testing"
)

// genData returns n bytes of deterministic pseudo-random data.
func genData(n int) []byte {
	r := rand.New(rand.NewSource(42))
	data := make([]byte, n)
	r.Read(data)
	return data
}

func TestBuzhashRollingMatchesDefinition(t *testing.T) {
	bh := NewBuzhash(tableSeed)
	data := genData(10000)
	for i, b := range data {
		got := bh.Roll(b)
		if i+1 >= WindowSize {
			want := bh.hashWindow(data[i+1-WindowSize : i+1])
			if got != want {
				t.Fatalf("rolling hash mismatch at %d: got %x want %x", i, got, want)
			}
		}
	}
}

func TestSplitDeterministicAndComplete(t *testing.T) {
	data := genData(1 << 20) // 1 MiB
	c := NewChunker()
	chunks1 := c.Split(data)
	chunks2 := NewChunker().Split(data)
	if len(chunks1) == 0 {
		t.Fatal("no chunks produced")
	}
	if len(chunks1) != len(chunks2) {
		t.Fatalf("non-deterministic split: %d vs %d chunks", len(chunks1), len(chunks2))
	}
	// Chunks reassemble into the original data and respect size bounds.
	var reassembled []byte
	for i, ch := range chunks1 {
		if ch != chunks2[i] {
			t.Fatalf("chunk %d differs between runs", i)
		}
		if ch.Offset != len(reassembled) {
			t.Fatalf("chunk %d offset %d, want %d", i, ch.Offset, len(reassembled))
		}
		last := i == len(chunks1)-1
		if !last && ch.Length < MinChunkSize {
			t.Fatalf("chunk %d length %d below min", i, ch.Length)
		}
		if ch.Length > MaxChunkSize {
			t.Fatalf("chunk %d length %d exceeds max", i, ch.Length)
		}
		if got := sha256.Sum256(data[ch.Offset : ch.Offset+ch.Length]); got != ch.Hash {
			t.Fatalf("chunk %d hash mismatch", i)
		}
		reassembled = append(reassembled, data[ch.Offset:ch.Offset+ch.Length]...)
	}
	if !bytes.Equal(reassembled, data) {
		t.Fatal("chunks do not reassemble into original data")
	}
	t.Logf("1 MiB split into %d chunks (avg %d bytes)", len(chunks1), len(data)/len(chunks1))
}

func TestStreamingMatchesBatch(t *testing.T) {
	data := genData(1 << 20)
	want := NewChunker().Split(data)
	for _, writeSize := range []int{1, 7, 4096, 100000} {
		var got []Chunk
		s := NewSplitter(NewChunker(), func(ch Chunk, _ []byte) { got = append(got, ch) })
		for i := 0; i < len(data); i += writeSize {
			end := i + writeSize
			if end > len(data) {
				end = len(data)
			}
			s.Write(data[i:end])
		}
		s.Close()
		if len(got) != len(want) {
			t.Fatalf("writeSize %d: got %d chunks, want %d", writeSize, len(got), len(want))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("writeSize %d: chunk %d differs", writeSize, i)
			}
		}
	}
}

// TestInsertMiddleReuse is the acceptance test: insert 100 bytes in the
// middle of a 10 MiB file and verify that chunking re-splits locally,
// >=99% of chunk hashes are reused, and only 1-2 chunks change.
func TestInsertMiddleReuse(t *testing.T) {
	const fileSize = 10 << 20 // 10 MiB
	const insertAt = fileSize / 2
	oldData := genData(fileSize)

	insert := genData(100) // 100 new bytes
	newData := make([]byte, 0, fileSize+100)
	newData = append(newData, oldData[:insertAt]...)
	newData = append(newData, insert...)
	newData = append(newData, oldData[insertAt:]...)

	oldChunks := NewChunker().Split(oldData)
	newChunks := NewChunker().Split(newData)

	oldChanged, newChanged := AlignDiff(oldChunks, newChunks)
	t.Logf("old: %d chunks, new: %d chunks", len(oldChunks), len(newChunks))
	t.Logf("changed: %d old chunk(s) -> %d new chunk(s)", len(oldChanged), len(newChanged))

	if len(oldChanged) > 2 || len(newChanged) > 2 {
		t.Fatalf("insertion changed %d old and %d new chunks, want at most 2 each",
			len(oldChanged), len(newChanged))
	}
	if len(newChanged) == 0 {
		t.Fatal("insertion changed no chunks at all")
	}
	// The changed region must be local to the insertion point.
	for _, ch := range newChanged {
		if ch.Offset > insertAt+100 || ch.Offset+ch.Length < insertAt {
			t.Fatalf("changed chunk [%d,%d) is not local to insertion at %d",
				ch.Offset, ch.Offset+ch.Length, insertAt)
		}
	}

	reuse := ReuseRatio(oldChunks, newChunks)
	t.Logf("chunk hash reuse: %.4f%%", reuse*100)
	if reuse < 0.99 {
		t.Fatalf("reuse ratio %.4f < 0.99", reuse)
	}

	// Merkle trees: root changes, but incremental sync needs only the
	// changed chunks plus their Merkle paths (O(log n) hashes each).
	oldTree := NewMerkleTreeFromChunks(oldChunks)
	newTree := NewMerkleTreeFromChunks(newChunks)
	if oldTree.Root() == newTree.Root() {
		t.Fatal("merkle root did not change after insertion")
	}
	proofLen := len(newTree.Proof(len(oldChunks) - len(oldChanged)))
	t.Logf("incremental sync: %d chunk(s) + merkle path of %d hashes (tree depth %d)",
		len(newChanged), proofLen, newTree.Depth())
	if proofLen > 32 {
		t.Fatalf("merkle proof length %d unexpectedly large", proofLen)
	}
}

func TestMerkleProof(t *testing.T) {
	data := genData(1 << 20)
	chunks := NewChunker().Split(data)
	tree := NewMerkleTreeFromChunks(chunks)
	root := tree.Root()
	for i, ch := range chunks {
		if !VerifyProof(ch.Hash, i, tree.Proof(i), root) {
			t.Fatalf("proof for leaf %d does not verify", i)
		}
	}
	// A tampered leaf must not verify.
	bad := chunks[0].Hash
	bad[0] ^= 0xFF
	if VerifyProof(bad, 0, tree.Proof(0), root) {
		t.Fatal("proof verified for tampered leaf")
	}
}

func TestMerkleEmptyAndSingle(t *testing.T) {
	empty := NewMerkleTree(nil)
	if empty.Root() != sha256.Sum256(nil) {
		t.Fatal("empty tree root mismatch")
	}
	leaf := sha256.Sum256([]byte("only"))
	single := NewMerkleTree([][sha256.Size]byte{leaf})
	if single.Root() != leaf {
		t.Fatal("single-leaf tree root should be the leaf itself")
	}
	if !VerifyProof(leaf, 0, single.Proof(0), single.Root()) {
		t.Fatal("single-leaf proof does not verify")
	}
}
