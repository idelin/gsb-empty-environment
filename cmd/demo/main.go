// Demo of the blkmap store: write, cross-block patch, snapshot, punch.
// Usage: go run ./cmd/demo [dir]   (default ./demo-data)
package main

import (
	"fmt"
	"log"
	"os"

	"blkmap"
)

func show(s *blkmap.Store, label string, n int) {
	buf := make([]byte, n)
	if _, err := s.ReadAt(buf, 0); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%-28s %q\n", label, string(buf))
}

func main() {
	dir := "./demo-data"
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}
	os.RemoveAll(dir)

	s, err := blkmap.Open(dir, 16) // 16-byte blocks for a compact demo
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("store dir: %s (block size %d)\n\n", dir, s.BlockSize())

	// 1. Write 2 blocks + 8 bytes: covers blocks 0,1 and half of block 2.
	if _, err := s.WriteAt([]byte("AAAAAAAAAAAAAAAABBBBBBBBBBBBBBBBCCCCCCCC"), 0); err != nil {
		log.Fatal(err)
	}
	show(s, "write 40 bytes:", 40)

	// 2. Cross-block write: 8 bytes straddling the block0/block1 boundary.
	if _, err := s.WriteAt([]byte("xxxxxxxx"), 12); err != nil {
		log.Fatal(err)
	}
	show(s, "cross-block write @12:", 40)

	// 3. Snapshot, then punch the middle; the snapshot must not change.
	if err := s.Snapshot("before-punch"); err != nil {
		log.Fatal(err)
	}
	if err := s.PunchHole(20, 8); err != nil {
		log.Fatal(err)
	}
	show(s, "after punch [20,28):", 40)

	snap := make([]byte, 40)
	if _, err := s.ReadSnapshotAt("before-punch", snap, 0); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%-28s %q\n", "snapshot before-punch:", string(snap))

	// 4. Reopen from the directory: state and snapshot survive.
	s, err = blkmap.Open(dir, 0)
	if err != nil {
		log.Fatal(err)
	}
	show(s, "after reopen:", 40)

	fmt.Printf("\nextents after reopen: %+v\n", s.Extents())
	entries, _ := os.ReadDir(dir + "/blocks")
	fmt.Printf("physical blocks on disk: %d (holes occupy none)\n", len(entries))
}
