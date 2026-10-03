// Package vecpool implements a memory pool for vectorized intermediate
// results together with a GC-avoidance execution pipeline.
//
// Intermediate vectors exchanged between execution operators are backed by
// fixed-size chunks that are pre-allocated before the query starts. When an
// operator finishes consuming a chunk it returns it to the pool, and
// downstream operators reuse the same memory slots, so the whole query
// execution performs zero heap allocations in steady state.
package vecpool

import "fmt"

// ChunkSize is the fixed capacity, in float64 elements, of every chunk.
const ChunkSize = 1024

// Chunk is a fixed-size block of vector memory. The backing array is
// allocated exactly once (at pool construction) and reused for the whole
// lifetime of the process.
type Chunk struct {
	// Data is the backing store. Only Data[:Len] holds live values.
	Data []float64
	// Len is the number of live elements in Data.
	Len int
}

// Reset marks the chunk empty so it can be reused. The backing array is
// kept, so no memory is freed or allocated.
func (c *Chunk) Reset() { c.Len = 0 }

// Pool is a pre-allocated pool of Chunk objects. After construction it never
// allocates: Get blocks until a chunk is returned if the pool is exhausted,
// which guarantees zero heap allocation during query execution.
type Pool struct {
	free chan *Chunk
	size int
}

// NewPool pre-allocates n chunks of ChunkSize elements each. All memory the
// pool will ever use is allocated here, before the pipeline starts.
func NewPool(n int) *Pool {
	if n <= 0 {
		panic("vecpool: pool size must be positive")
	}
	p := &Pool{free: make(chan *Chunk, n), size: n}
	for i := 0; i < n; i++ {
		p.free <- &Chunk{Data: make([]float64, ChunkSize)}
	}
	return p
}

// Get acquires a chunk from the pool. It never allocates; if the pool is
// empty it blocks until another operator returns a chunk.
func (p *Pool) Get() *Chunk { return <-p.free }

// Put returns a chunk to the pool for reuse by downstream operators.
func (p *Pool) Put(c *Chunk) {
	c.Reset()
	p.free <- c
}

// Cap returns the number of chunks the pool manages.
func (p *Pool) Cap() int { return p.size }

// Available reports how many chunks are currently free (diagnostics only).
func (p *Pool) Available() int { return len(p.free) }

func (c *Chunk) String() string {
	return fmt.Sprintf("Chunk(len=%d cap=%d)", c.Len, cap(c.Data))
}
