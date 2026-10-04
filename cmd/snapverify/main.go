// Command snapverify demonstrates the incremental snapshot verification
// flow: a prover with the full map emits (delta, proof) pairs, and a
// receiver holding only the current root and version verifies each hop.
package main

import (
	"fmt"
	"math/rand"
	"os"

	"snapdelta"
)

func main() {
	seed := int64(1)
	if len(os.Args) > 1 {
		if _, err := fmt.Sscan(os.Args[1], &seed); err != nil {
			fmt.Fprintf(os.Stderr, "usage: snapverify [seed]\n")
			os.Exit(2)
		}
	}
	r := rand.New(rand.NewSource(seed))

	// Prover's full snapshot; the receiver only keeps root + version.
	full := make(map[uint16][]byte)
	for len(full) < 50 {
		k := uint16(r.Intn(snapdelta.KeySpaceSize))
		v := make([]byte, 8)
		r.Read(v)
		full[k] = v
	}
	root := snapdelta.FullRoot(full)
	version := uint64(1)
	fmt.Printf("genesis: version=%d keys=%d root=%x\n", version, len(full), root[:8])

	for round := 0; round < 5; round++ {
		var ops []snapdelta.Op
		for i := 0; i < 6; i++ {
			k := uint16(r.Intn(snapdelta.KeySpaceSize))
			if r.Intn(3) == 0 {
				ops = append(ops, snapdelta.Op{Type: snapdelta.OpDelete, Key: k})
			} else {
				v := make([]byte, 8)
				r.Read(v)
				ops = append(ops, snapdelta.Op{Type: snapdelta.OpPut, Key: k, Value: v})
			}
		}
		delta, proof, newFull, err := snapdelta.BuildDelta(full, version, ops, 0, snapdelta.KeySpaceSize-1)
		if err != nil {
			fmt.Fprintf(os.Stderr, "round %d: build: %v\n", round, err)
			os.Exit(1)
		}
		newRoot, newVersion, err := snapdelta.Verify(root, version, delta, proof)
		if err != nil {
			fmt.Fprintf(os.Stderr, "round %d: verify: %v\n", round, err)
			os.Exit(1)
		}
		if newRoot != snapdelta.FullRoot(newFull) {
			fmt.Fprintf(os.Stderr, "round %d: verified root != full rebuild\n", round)
			os.Exit(1)
		}
		fmt.Printf("round %d: v%d -> v%d ops=%d inherited-blocks=%d root=%x OK\n",
			round, version, newVersion, len(ops), len(proof.Nodes), newRoot[:8])
		full, root, version = newFull, newRoot, newVersion
	}
	fmt.Println("all deltas verified against full rebuilds")
}
