// Command demo runs a 10-operator vectorized query pipeline over a pooled
// chunk allocator and prints a memory monitor trace proving that, once the
// pipeline is established, the heap allocation count stays at zero, no GC
// cycle runs, and the memory curve is flat.
package main

import (
	"fmt"
	"os"
	"runtime"

	"vecpool"
)

const (
	poolChunks  = 64
	totalChunks = 200_000
	sampleEvery = 25_000
)

// buildPipeline assembles the 10 complex operators of the query.
func buildPipeline(pool *vecpool.Pool) *vecpool.Pipeline {
	return vecpool.NewPipeline(pool,
		vecpool.AddScalar(500),
		vecpool.MulScalar(1.5),
		vecpool.Square(),
		vecpool.Sqrt(),
		vecpool.FilterGreaterThan{Threshold: 10},
		vecpool.Log1pAbs(),
		vecpool.Sin(),
		vecpool.Abs(),
		vecpool.SubtractMean(),
		vecpool.Clamp(0, 100),
	)
}

func main() {
	// Pin the execution to a single P so the runtime's per-P sudog cache
	// stabilizes during warm-up; otherwise goroutine migration between
	// Ps causes occasional 96-byte runtime-internal sudog allocations
	// that would show up as noise on the memory monitor.
	runtime.GOMAXPROCS(1)

	pool := vecpool.NewPool(poolChunks)
	pipe := buildPipeline(pool)

	// Wire the pipeline as a streaming stage: source -> pipeline -> sink.
	// Channel buffers stay small so most chunks live in the pool, not
	// parked in channels: pool size (64) always exceeds the maximum
	// number of in-flight chunks, so the source never starves.
	in := make(chan *vecpool.Chunk, 4)
	out := make(chan *vecpool.Chunk, 4)

	// Sink: returns every finished chunk to the pool for reuse.
	done := make(chan struct{})
	go func() {
		for c := range out {
			pool.Put(c)
		}
		close(done)
	}()

	// Execution stage: runs the 10 operators on each chunk.
	go func() {
		for c := range in {
			out <- pipe.Execute(c)
		}
		close(out)
	}()

	mon := vecpool.NewMemMonitor(totalChunks/sampleEvery + 2)

	// Warm-up: establishes the pipeline (goroutines, channels, runtime
	// caches). Everything after this point must perform zero heap
	// allocations. A few thousand chunks are streamed so every lazy
	// one-time runtime allocation happens before the baseline.
	for i := 0; i < 4096; i++ {
		c := pool.Get()
		fill(c, i)
		in <- c
	}
	runtime.Gosched()
	mon.Sample() // baseline: pipeline established

	// Source: feeds the query, reusing memory slots returned by the sink.
	for i := 0; i < totalChunks; i++ {
		c := pool.Get() // never allocates: reuses a returned slot
		fill(c, i)
		in <- c
		if (i+1)%sampleEvery == 0 {
			mon.Sample()
		}
	}
	close(in)
	<-done
	mon.Sample()

	allocBytes, gcCycles, pauseNs := mon.Delta()
	fmt.Printf("pipeline: %d operators, pool: %d chunks x %d float64\n",
		pipe.Len(), pool.Cap(), vecpool.ChunkSize)
	fmt.Printf("streamed %d chunks (%d vectors x %d elements)\n\n",
		totalChunks, totalChunks, vecpool.ChunkSize)
	fmt.Printf("%-10s %-14s %-14s %-8s %-12s\n", "chunk#", "HeapAlloc(B)", "TotalAlloc(B)", "NumGC", "PauseTotal(ns)")
	for i, s := range mon.Samples() {
		fmt.Printf("%-10d %-14d %-14d %-8d %-12d\n",
			i*sampleEvery, s.HeapAlloc, s.TotalAlloc, s.NumGC, s.PauseTotalNs)
	}
	fmt.Printf("\nafter pipeline established: heap alloc = %d bytes, GC cycles = %d, STW pause = %d ns\n",
		allocBytes, gcCycles, pauseNs)
	if allocBytes != 0 || gcCycles != 0 {
		fmt.Println("FAIL: query execution allocated memory or triggered GC")
		os.Exit(1)
	}
	fmt.Println("OK: zero heap allocation, zero GC, flat memory curve")
}

// fill writes deterministic synthetic data into a chunk without allocating.
func fill(c *vecpool.Chunk, seq int) {
	base := seq * vecpool.ChunkSize
	for j := range c.Data {
		c.Data[j] = float64((base+j)%2000) - 1000.0
	}
	c.Len = vecpool.ChunkSize
}
