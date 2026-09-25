// Demo of the chunkstore package: chunked content-addressed storage with
// deduplication, reference-counted metadata transactions, fault injection
// and crash recovery.
package main

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"chunkstore"
)

const blockSize = 64 // tiny blocks so the demo shows real chunking

func blockFiles(dir string) []string {
	var out []string
	_ = filepath.WalkDir(filepath.Join(dir, "blocks"), func(p string, e os.DirEntry, err error) error {
		if err == nil && !e.IsDir() {
			rel, _ := filepath.Rel(dir, p)
			out = append(out, rel)
		}
		return nil
	})
	return out
}

func showBlocks(dir string) {
	files := blockFiles(dir)
	fmt.Printf("  block files on disk (%d):\n", len(files))
	for _, f := range files {
		fmt.Printf("    %s\n", f)
	}
}

func main() {
	dir := filepath.Join(os.TempDir(), "chunkstore-demo")
	os.RemoveAll(dir)

	s, err := chunkstore.Open(dir, blockSize)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("opened store at %s (block size %d)\n\n", dir, s.BlockSize())

	// Two values sharing a 3-block prefix -> content-defined dedup.
	shared := bytes.Repeat([]byte("shared-block-payload!"), 10)[:blockSize*3]
	a := append(append([]byte{}, shared...), []byte("unique-to-A")...)
	b := append(append([]byte{}, shared...), []byte("unique-to-B")...)

	must(s.Put("a", bytes.NewReader(a)))
	must(s.Put("b", bytes.NewReader(b)))
	fmt.Println("put a (4 blocks) and b (4 blocks) sharing a 3-block prefix:")
	showBlocks(dir)

	// Delete a: shared blocks survive, only a's unique block is reclaimed.
	must(s.Delete("a"))
	fmt.Println("\ndelete a -> shared blocks kept, only a's unique block reclaimed:")
	showBlocks(dir)

	// Fault injection: fail on the 3rd block write of a 5-block put.
	fj := chunkstore.NewFaultInjector()
	fj.ArmAfter("block.write", 2)
	s.SetFaultInjector(fj)
	big := make([]byte, blockSize*5)
	rand.Read(big)
	err = s.Put("c", bytes.NewReader(big))
	fmt.Printf("\nput c with fault injected at 3rd block write -> %v\n", err)
	fmt.Println("store rolled back, no orphan blocks left:")
	showBlocks(dir)

	// Simulated process crash mid-put, then reopen.
	s.SetFaultInjector(nil)
	writes := 0
	s.SetPointHook(func(point string) {
		if point == "block.write" {
			writes++
			if writes == 2 {
				panic("simulated crash")
			}
		}
	})
	func() { defer func() { _ = recover() }(); _ = s.Put("d", bytes.NewReader(big)) }()
	fmt.Println("\ncrash simulated after 2nd block write of put d; reopening store...")
	s2, err := chunkstore.Open(dir, blockSize)
	if err != nil {
		log.Fatal(err)
	}
	if _, err := s2.Get("d"); err != nil {
		fmt.Printf("  get d after crash: %v (commit never happened)\n", err)
	}
	fmt.Println("  orphan blocks leaked by the crash (space only, never correctness):")
	showBlocks(dir)
	must(s2.CollectGarbage())
	fmt.Println("after CollectGarbage:")
	showBlocks(dir)

	// Empty value and exact-block-size boundary.
	must(s2.Put("empty", bytes.NewReader(nil)))
	exact := make([]byte, blockSize)
	rand.Read(exact)
	must(s2.Put("exact", bytes.NewReader(exact)))
	gotEmpty, _ := s2.Get("empty")
	gotExact, _ := s2.Get("exact")
	fmt.Printf("\nempty value reads back %d bytes; exact-block-size value ok=%v\n",
		len(gotEmpty), bytes.Equal(gotExact, exact))

	gotB, _ := s2.Get("b")
	fmt.Printf("value b still intact after all of the above: %v\n", bytes.Equal(gotB, b))
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
