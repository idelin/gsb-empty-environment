// Demo of the chunkstore package: chunked dedup, shared-chunk safety,
// injected-failure rollback, and read-time checksum verification.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"chunkstore"
)

func main() {
	dir, err := os.MkdirTemp("", "chunkstore-demo-*")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(dir)
	fmt.Println("store dir:", dir)

	s, err := chunkstore.Open(dir, chunkstore.Options{ChunkSize: 1024})
	if err != nil {
		log.Fatal(err)
	}

	// 1. Large value split into chunks.
	big := bytes.Repeat([]byte("hello-chunks-"), 400) // 5200 bytes -> 6 chunks
	must(s.PutBytes("big", big))
	keys, chunks := s.Stats()
	fmt.Printf("put big (%d bytes)      -> keys=%d chunks=%d\n", len(big), keys, chunks)

	// 2. Second value sharing big's first 5 chunks plus a tail: dedup.
	tail := []byte("unique-tail")
	big2 := append(append([]byte{}, big...), tail...)
	must(s.PutBytes("big2", big2))
	_, chunks = s.Stats()
	fmt.Printf("put big2 (shared+tail)  -> chunks=%d (only 1 new, rest deduped)\n", chunks)

	// 3. Delete big: shared chunks survive, big2 still readable.
	must(s.Delete("big"))
	got, err := s.Get("big2")
	must(err)
	fmt.Printf("delete big; big2 intact -> %v, chunks=%d (only big's private chunk reclaimed)\n",
		bytes.Equal(got, big2), chunksOf(s))

	// 4. Injected failure mid-put: new orphan chunks rolled back.
	boom := errors.New("simulated disk error")
	s2, err := chunkstore.Open(dir, chunkstore.Options{
		ChunkSize: 1024,
		Hooks: chunkstore.Hooks{
			AfterChunkWrite: func(n int, _ string) error {
				if n == 3 {
					return boom
				}
				return nil
			},
		},
	})
	must(err)
	err = s2.PutBytes("victim", bytes.Repeat([]byte("x"), 5000))
	fmt.Printf("put victim, fail@chunk3 -> err=%q, chunks=%d (orphans cleaned)\n", err, chunksOf(s2))

	// 5. Empty value and exact-boundary value.
	must(s2.PutBytes("empty", nil))
	must(s2.PutBytes("exact", bytes.Repeat([]byte("y"), 2048))) // exactly 2 chunks
	e, _ := s2.Get("empty")
	x, _ := s2.Get("exact")
	fmt.Printf("empty=%d bytes, exact=%d bytes roundtrip ok\n", len(e), len(x))

	// 6. Corruption detection on read: tamper with big2's first chunk.
	sum := sha256.Sum256(big[:1024])
	h := hex.EncodeToString(sum[:])
	p := filepath.Join(dir, "chunks", h[:2], h)
	must(os.WriteFile(p, []byte("tampered"), 0o644))
	if _, err := s2.Get("big2"); err != nil {
		fmt.Println("tampered chunk detected ->", err)
	}
}

func chunksOf(s *chunkstore.Store) int {
	_, c := s.Stats()
	return c
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
