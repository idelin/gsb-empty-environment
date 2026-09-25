// Demo of the triestore package: put/get/scan/delete, snapshots and verify.
// Usage: go run ./cmd/demo [dir]   (dir defaults to ./demo-data)
package main

import (
	"fmt"
	"log"
	"os"

	"triestore"
)

func main() {
	dir := "./demo-data"
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}
	os.RemoveAll(dir)

	s, err := triestore.Open(dir, &triestore.Options{Sync: true})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("store opened at %s (sync=on)\n\n", dir)

	// 1. Insert keys, including an empty key and a binary key.
	r1, _ := s.Put([]byte("apple"), []byte("red"))
	s.Put([]byte("app"), []byte("green"))
	s.Put([]byte("banana"), []byte("yellow"))
	s.Put([]byte{}, []byte("empty-key-value"))
	s.Put([]byte{0x00, 0xff}, []byte{0xca, 0xfe})
	fmt.Printf("committed %d snapshots, current root=%.12s...\n\n", s.Seq(), s.RootID())

	// 2. Prefix scan.
	fmt.Println("scan prefix \"app\":")
	s.Scan([]byte("app"), func(k, v []byte) bool {
		fmt.Printf("  %q -> %q\n", k, v)
		return true
	})
	fmt.Println("scan all (empty key sorts first):")
	s.Scan(nil, func(k, v []byte) bool {
		fmt.Printf("  %q -> %q\n", k, v)
		return true
	})

	// 3. Delete "app": "apple" must survive; empty branches compress away.
	fmt.Println("\ndelete \"app\"")
	s.Delete([]byte("app"))
	if _, ok, _ := s.Get([]byte("app")); !ok {
		fmt.Println("  \"app\" is gone")
	}
	if v, ok, _ := s.Get([]byte("apple")); ok {
		fmt.Printf("  \"apple\" still readable: %q\n", v)
	}

	// 4. Historical root still serves the old value.
	v, ok, _ := s.GetAt(r1, []byte("app"))
	fmt.Printf("\nhistorical root@%d reads \"app\" -> %q (ok=%v)\n", r1.Seq, v, ok)

	// 5. Verify integrity of the current tree.
	if err := s.Verify(); err != nil {
		log.Fatalf("verify: %v", err)
	}
	fmt.Println("\nVerify: current root OK")

	// 6. Reopen to demonstrate crash recovery.
	s2, err := triestore.Open(dir, &triestore.Options{Sync: true})
	if err != nil {
		log.Fatal(err)
	}
	v, ok, _ = s2.Get([]byte{0x00, 0xff})
	fmt.Printf("reopened store: binary key 0x00ff -> %x (ok=%v), seq=%d\n", v, ok, s2.Seq())
}
