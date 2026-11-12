package secretlease

import (
	"errors"
	"sync"
	"sync/atomic"
)

// ErrPoolExhausted is returned when the pool has no free chunk for an
// allocation.
var ErrPoolExhausted = errors.New("secretlease: secure memory pool exhausted")

// ErrBufferDestroyed is returned when a destroyed buffer is touched.
var ErrBufferDestroyed = errors.New("secretlease: buffer has been destroyed")

// MemPool is a fixed-size pool of fixed-size chunks that backs every secret
// and every derived buffer. Chunks are zeroed before being returned to the
// free list, which makes the pool usable in tests as an oracle for "no secret
// bytes remain anywhere".
type MemPool struct {
	chunkSize int

	mu    sync.Mutex
	arena [][]byte
	free  [][]byte

	allocN atomic.Int64
}

// NewMemPool creates a pool whose arena holds capacity chunks of chunkSize
// bytes each.
func NewMemPool(chunkSize, capacity int) *MemPool {
	if chunkSize <= 0 || capacity <= 0 {
		panic("secretlease: chunkSize and capacity must be positive")
	}
	p := &MemPool{chunkSize: chunkSize, free: make([][]byte, 0, capacity)}
	for i := 0; i < capacity; i++ {
		p.arena = append(p.arena, make([]byte, chunkSize))
		p.free = append(p.free, p.arena[i])
	}
	return p
}

// Alloc hands out a pool chunk wrapped in a [Buffer]. The requested size must
// not exceed the pool chunk size.
func (p *MemPool) Alloc(size int) (*Buffer, error) {
	if size < 0 {
		panic("secretlease: negative allocation size")
	}
	if size > p.chunkSize {
		return nil, ErrPoolExhausted
	}
	p.mu.Lock()
	if len(p.free) == 0 {
		p.mu.Unlock()
		return nil, ErrPoolExhausted
	}
	chunk := p.free[len(p.free)-1]
	p.free = p.free[:len(p.free)-1]
	p.mu.Unlock()

	p.allocN.Add(1)
	return &Buffer{pool: p, chunk: chunk, size: size, alive: true}, nil
}

func (p *MemPool) reclaim(chunk []byte) {
	clear(chunk)
	p.mu.Lock()
	p.free = append(p.free, chunk)
	p.mu.Unlock()
}

// Outstanding returns the number of chunks currently handed out.
func (p *MemPool) Outstanding() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.arena) - len(p.free)
}

// Allocations returns the total number of successful allocations.
func (p *MemPool) Allocations() int64 {
	return p.allocN.Load()
}

// Contains reports whether pattern currently appears anywhere in the pool
// arena, including both live chunks and zeroed free chunks.
func (p *MemPool) Contains(pattern []byte) bool {
	if len(pattern) == 0 {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, chunk := range p.arena {
		if containsBytes(chunk, pattern) {
			return true
		}
	}
	return false
}

// FreeChunksZeroed reports whether every free chunk is fully zeroed.
func (p *MemPool) FreeChunksZeroed() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, chunk := range p.free {
		for _, b := range chunk {
			if b != 0 {
				return false
			}
		}
	}
	return true
}

func containsBytes(haystack, needle []byte) bool {
	if len(needle) == 0 || len(needle) > len(haystack) {
		return false
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		match := true
		for j := range needle {
			if haystack[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// Buffer is a byte buffer backed by a [MemPool] chunk. Destroy zeroes the
// backing memory and returns the chunk to the pool.
type Buffer struct {
	pool  *MemPool
	mu    sync.Mutex
	chunk []byte
	size  int
	alive bool
}

// Bytes returns the usable portion of the backing chunk. It returns nil after
// Destroy.
func (b *Buffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.alive {
		return nil
	}
	return b.chunk[:b.size]
}

// Alive reports whether the buffer still owns its backing chunk.
func (b *Buffer) Alive() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.alive
}

// Destroy zeroes the backing chunk and returns it to the pool. It is safe to
// call more than once.
func (b *Buffer) Destroy() {
	b.mu.Lock()
	if !b.alive {
		b.mu.Unlock()
		return
	}
	b.alive = false
	chunk := b.chunk
	b.chunk = nil
	pool := b.pool
	b.mu.Unlock()

	pool.reclaim(chunk)
}
