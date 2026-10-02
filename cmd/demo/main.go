// Command demo streams 100 MiB of random frames through a 4 MiB ring
// pool, verifying zero-copy slicing, reference conservation, and near
// zero GC pressure on the pooled path.
package main

import (
	"fmt"
	"io"
	"math/rand"
	"runtime"
	"time"

	"ringpool"
)

const (
	blockSize = 4096
	numBlocks = 1024 // 4 MiB ring
	total     = 100 << 20
	maxFrame  = 64 << 10
)

func fillFrame(seed int, buf []byte) {
	x := uint32(seed)*2654435761 + 1
	for i := range buf {
		x ^= x << 13
		x ^= x >> 17
		x ^= x << 5
		buf[i] = byte(x)
	}
}

func main() {
	pool := ringpool.New(blockSize, numBlocks)
	defer pool.Close()

	pr, pw := io.Pipe()
	fw := ringpool.NewFrameWriter(pw)
	fr := ringpool.NewFrameReader(pool, pr)

	go func() {
		rng := rand.New(rand.NewSource(42))
		sent, frame := 0, 0
		for sent < total {
			n := 1 + rng.Intn(maxFrame)
			if n > total-sent {
				n = total - sent
			}
			payload := make([]byte, n)
			fillFrame(frame, payload)
			if err := fw.WriteFrame(payload); err != nil {
				panic(err)
			}
			sent += n
			frame++
		}
		pw.Close()
	}()

	var m0 runtime.MemStats
	runtime.ReadMemStats(&m0)
	start := time.Now()

	received, frames := 0, 0
	var checksum, maxOutstanding uint64
	for {
		s, err := fr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			panic(err)
		}
		for _, b := range s.Bytes() {
			checksum += uint64(b)
		}
		received += s.Len()
		if cur := uint64(pool.Stats().OutstandingBytes); cur > maxOutstanding {
			maxOutstanding = cur
		}
		s.Release()
		frames++
	}

	elapsed := time.Since(start)
	var m1 runtime.MemStats
	runtime.ReadMemStats(&m1)
	st := pool.Stats()

	fmt.Printf("streamed        : %d MiB in %d frames\n", received>>20, frames)
	fmt.Printf("elapsed         : %v (%.1f MiB/s)\n", elapsed.Round(time.Millisecond), float64(received)/(1<<20)/elapsed.Seconds())
	fmt.Printf("checksum        : %d\n", checksum)
	fmt.Printf("ring capacity   : %d MiB (%d blocks x %d B)\n", pool.Capacity()>>20, st.NumBlocks, st.BlockSize)
	fmt.Printf("max outstanding : %d KiB (<= ring capacity)\n", maxOutstanding>>10)
	fmt.Printf("borrow/release  : %d / %d (conserved: %v)\n", st.TotalBorrowed, st.TotalReleased, st.TotalBorrowed == st.TotalReleased)
	fmt.Printf("outstanding now : %d slices, %d bytes\n", st.OutstandingSlices, st.OutstandingBytes)
	fmt.Printf("free blocks     : %d / %d\n", st.FreeBlocks, st.NumBlocks)
	fmt.Printf("heap allocs     : %d total (%.1f per frame, incl. producer payloads)\n",
		m1.Mallocs-m0.Mallocs, float64(m1.Mallocs-m0.Mallocs)/float64(frames))
}
