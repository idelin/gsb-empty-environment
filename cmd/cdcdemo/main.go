// Command cdcdemo demonstrates content-defined chunking: it chunks a
// 10 MiB file, inserts 100 bytes in the middle, re-chunks, and shows
// that only 1-2 chunks change while >=99% of chunk hashes are reused.
package main

import (
	"fmt"
	"math/rand"

	"cdc"
)

func genData(seed int64, n int) []byte {
	r := rand.New(rand.NewSource(seed))
	data := make([]byte, n)
	r.Read(data)
	return data
}

func main() {
	const fileSize = 10 << 20 // 10 MiB
	const insertAt = fileSize / 2

	oldData := genData(42, fileSize)
	insert := genData(7, 100)
	newData := make([]byte, 0, fileSize+100)
	newData = append(newData, oldData[:insertAt]...)
	newData = append(newData, insert...)
	newData = append(newData, oldData[insertAt:]...)

	oldChunks := cdc.NewChunker().Split(oldData)
	newChunks := cdc.NewChunker().Split(newData)
	oldTree := cdc.NewMerkleTreeFromChunks(oldChunks)
	newTree := cdc.NewMerkleTreeFromChunks(newChunks)

	fmt.Printf("file: %d bytes -> insert 100 bytes at offset %d -> %d bytes\n",
		len(oldData), insertAt, len(newData))
	fmt.Printf("chunks: %d -> %d (min %d / avg %d / max %d bytes)\n",
		len(oldChunks), len(newChunks), cdc.MinChunkSize, cdc.AvgChunkSize, cdc.MaxChunkSize)
	oldRoot := oldTree.Root()
	newRoot := newTree.Root()
	fmt.Printf("merkle root: %x...\n", oldRoot[:8])
	fmt.Printf("merkle root: %x... (after insert)\n", newRoot[:8])

	oldChanged, newChanged := cdc.AlignDiff(oldChunks, newChunks)
	fmt.Printf("changed chunks: %d old -> %d new\n", len(oldChanged), len(newChanged))
	for _, ch := range newChanged {
		fmt.Printf("  new chunk @%d len=%d hash=%x...\n", ch.Offset, ch.Length, ch.Hash[:8])
	}

	reuse := cdc.ReuseRatio(oldChunks, newChunks)
	fmt.Printf("chunk hash reuse: %.2f%% (%d of %d new chunks unchanged)\n",
		reuse*100, int(reuse*float64(len(newChunks))+0.5), len(newChunks))

	// Incremental sync: ship only the changed chunks plus the Merkle
	// authentication path needed to verify them against the new root.
	changedIdx := len(oldChunks) - len(oldChanged) // first changed leaf index
	proof := newTree.Proof(changedIdx)
	ok := cdc.VerifyProof(newChunks[changedIdx].Hash, changedIdx, proof, newTree.Root())
	fmt.Printf("incremental sync to new tree: %d chunk(s) + merkle path of %d hashes (depth %d), proof verifies: %v\n",
		len(newChanged), len(proof), newTree.Depth(), ok)
}
