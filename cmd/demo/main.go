// Command demo runs the acceptance scenario end to end: chunk a
// 10 MiB file, insert 100 bytes in the middle, re-chunk, and show
// that only the local chunks and their Merkle paths change.
package main

import (
	"fmt"
	"math/rand"

	"cdc"
)

func main() {
	const fileSize = 10 << 20
	const insertSize = 100

	rng := rand.New(rand.NewSource(42))
	oldData := make([]byte, fileSize)
	rng.Read(oldData)
	insert := make([]byte, insertSize)
	rng.Read(insert)

	mid := fileSize / 2
	newData := make([]byte, 0, fileSize+insertSize)
	newData = append(newData, oldData[:mid]...)
	newData = append(newData, insert...)
	newData = append(newData, oldData[mid:]...)

	chunker := cdc.DefaultChunker()
	oldChunks := chunker.Split(oldData)
	newChunks := chunker.Split(newData)
	oldTree := cdc.BuildTree(cdc.Hashes(oldChunks))
	newTree := cdc.BuildTree(cdc.Hashes(newChunks))

	fmt.Printf("file: %d bytes -> %d bytes (inserted %d bytes at offset %d)\n",
		len(oldData), len(newData), insertSize, mid)
	fmt.Printf("chunks: %d -> %d\n", len(oldChunks), len(newChunks))
	fmt.Printf("old merkle root: %x\n", oldTree.Root())
	fmt.Printf("new merkle root: %x\n", newTree.Root())

	changed, reused := cdc.Diff(oldChunks, newChunks)
	fmt.Printf("reused: %d / %d bytes (%.4f%%)\n",
		reused, len(newData), 100*float64(reused)/float64(len(newData)))
	fmt.Printf("incremental transfer: %d chunk(s)\n", len(changed))
	for _, ch := range changed {
		idx := -1
		for i, nc := range newChunks {
			if nc.Hash == ch.Hash {
				idx = i
				break
			}
		}
		proof := newTree.Proof(idx)
		ok := cdc.VerifyProof(ch.Hash, proof, newTree.Root())
		fmt.Printf("  chunk #%d offset=%d len=%d hash=%x... merkle-path=%d hashes verified=%v\n",
			idx, ch.Offset, ch.Length, ch.Hash[:8], len(proof), ok)
	}
}
