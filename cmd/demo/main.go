// Command demo streams 100 MiB of deterministic random frames through a
// 4 MiB ring pool, verifying integrity and reporting memory statistics.
package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math/rand"
	"runtime"
	"time"

	"ringpool"
)

const (
	numBlocks  = 256
	blockSize  = 16 * 1024
	totalBytes = 100 << 20
	maxFrame   = 64 * 1024
	seed       = 42
)

func main() {
	pool := ringpool.New(numBlocks, blockSize)
	fmt.Printf("ring pool: %d blocks x %d KiB = %d MiB resident\n",
		numBlocks, blockSize>>10, pool.Capacity()>>20)

	whdr := make([]byte, 4)
	payload := make([]byte, maxFrame)

	start := time.Now()
	go func() {
		wr := rand.New(rand.NewSource(seed))
		written := 0
		for written < totalBytes {
			n := 64 + wr.Intn(maxFrame-64)
			binary.LittleEndian.PutUint32(whdr, uint32(n))
			wr.Read(payload[:n])
			pool.Write(whdr)
			pool.Write(payload[:n])
			written += n
		}
		pool.Close()
	}()

	rd := rand.New(rand.NewSource(seed))
	expect := make([]byte, maxFrame)
	frame := make([]byte, maxFrame)
	rhdr := make([]byte, 4)
	var slices int64
	consumed := 0

	fill := func(dst []byte) {
		for off := 0; off < len(dst); {
			s, err := pool.Slice(len(dst) - off)
			if err != nil {
				panic(err)
			}
			slices++
			off += copy(dst[off:], s.Bytes())
			s.Release()
		}
	}

	for consumed < totalBytes {
		fill(rhdr)
		n := int(binary.LittleEndian.Uint32(rhdr))
		rd.Intn(maxFrame - 64) // keep PRNG in lock-step with producer
		rd.Read(expect[:n])
		fill(frame[:n])
		if !bytes.Equal(frame[:n], expect[:n]) {
			panic("stream corrupted")
		}
		consumed += n
	}
	elapsed := time.Since(start)

	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	fmt.Printf("streamed:   %d MiB in %v (%.0f MiB/s)\n",
		consumed>>20, elapsed.Round(time.Millisecond),
		float64(consumed)/(1<<20)/elapsed.Seconds())
	fmt.Printf("slices:     %d borrowed / released (zero-copy)\n", slices)
	fmt.Printf("pool:       leased=%d free=%d/%d buffered=%d\n",
		pool.Leased(), pool.FreeBlocks(), pool.NumBlocks(), pool.Buffered())
	fmt.Printf("runtime:    heapAlloc=%d KiB numGC=%d\n",
		ms.HeapAlloc>>10, ms.NumGC)
	if pool.Leased() != 0 || pool.FreeBlocks() != numBlocks {
		panic("block leak detected")
	}
	fmt.Println("OK: borrow/release conserved, no leaks")
}
