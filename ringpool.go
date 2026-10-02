// Package ringpool implements a fixed-size ring memory pool with
// zero-copy, reference-counted slices for high-performance network IO.
//
// The pool owns a single flat byte array organized as a ring of
// fixed-size blocks. Producers Reserve a contiguous run of blocks,
// fill the returned buffer in place, and Commit it as a read-only
// Slice. Slices carry a reference count; when the last reference is
// released the underlying blocks return to the pool for reuse. No
// payload bytes are ever copied and no per-frame heap allocation is
// required on the hot path.
package ringpool

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
)

var (
	// ErrClosed is returned by Reserve after the pool is closed.
	ErrClosed = errors.New("ringpool: pool is closed")
	// ErrTooLarge is returned when a reservation exceeds pool capacity.
	ErrTooLarge = errors.New("ringpool: reservation exceeds pool capacity")
)

// Pool is a ring of fixed-size blocks backing zero-copy slices.
// It is safe for concurrent use by one or more producers and consumers.
type Pool struct {
	mu        sync.Mutex
	cond      *sync.Cond
	buf       []byte
	blockSize int
	nblocks   int
	refs      []int32 // per-block occupancy (0 = free)
	writeBlk  int     // ring cursor: next block to allocate from
	freeBlks  int
	closed    bool

	resPool   sync.Pool // recycles Reservation objects
	slicePool sync.Pool // recycles Slice objects

	outstandingBytes  atomic.Int64
	outstandingSlices atomic.Int64
	totalBorrowed     atomic.Uint64
	totalReleased     atomic.Uint64
	totalStreamed     atomic.Uint64
}

// New creates a pool of numBlocks blocks of blockSize bytes each.
// The backing array (blockSize*numBlocks bytes) is the only bulk
// allocation the pool ever performs.
func New(blockSize, numBlocks int) *Pool {
	if blockSize <= 0 || numBlocks <= 0 {
		panic("ringpool: blockSize and numBlocks must be positive")
	}
	p := &Pool{
		buf:       make([]byte, blockSize*numBlocks),
		blockSize: blockSize,
		nblocks:   numBlocks,
		refs:      make([]int32, numBlocks),
		freeBlks:  numBlocks,
	}
	p.cond = sync.NewCond(&p.mu)
	p.resPool.New = func() any { return &Reservation{} }
	p.slicePool.New = func() any { return &Slice{} }
	return p
}

// BlockSize returns the pool's block granularity in bytes.
func (p *Pool) BlockSize() int { return p.blockSize }

// Capacity returns the total ring capacity in bytes.
func (p *Pool) Capacity() int { return len(p.buf) }

// canAllocLocked finds a contiguous run of k free blocks, allowing one
// wrap around the ring. It returns the starting block index.
func (p *Pool) canAllocLocked(k int) (int, bool) {
	// Contiguous free run from the write cursor to the end of the ring.
	run := 0
	for p.writeBlk+run < p.nblocks && p.refs[p.writeBlk+run] == 0 {
		run++
	}
	if run >= k {
		return p.writeBlk, true
	}
	// The tail is fully free; try wrapping to block 0. The skipped tail
	// blocks stay free and are reused on the next pass of the cursor.
	if p.writeBlk+run == p.nblocks {
		head := 0
		for head < p.writeBlk && p.refs[head] == 0 {
			head++
		}
		if head >= k {
			return 0, true
		}
	}
	return 0, false
}

// Reserve blocks until k contiguous blocks are available and returns a
// Reservation whose Buf (len n) the caller fills in place. The caller
// must either Commit or Cancel the reservation exactly once.
func (p *Pool) Reserve(n int) (*Reservation, error) {
	if n <= 0 {
		return nil, fmt.Errorf("ringpool: reservation size must be positive, got %d", n)
	}
	k := (n + p.blockSize - 1) / p.blockSize
	if k > p.nblocks {
		return nil, ErrTooLarge
	}
	p.mu.Lock()
	for {
		if p.closed {
			p.mu.Unlock()
			return nil, ErrClosed
		}
		if start, ok := p.canAllocLocked(k); ok {
			for i := start; i < start+k; i++ {
				p.refs[i] = 1
			}
			p.freeBlks -= k
			p.writeBlk = (start + k) % p.nblocks
			p.mu.Unlock()

			off := start * p.blockSize
			r := p.resPool.Get().(*Reservation)
			r.pool = p
			r.blk = start
			r.nblk = k
			r.Buf = p.buf[off : off+n : off+n]
			r.done = false
			return r, nil
		}
		p.cond.Wait()
	}
}

// TryReserve is the non-blocking variant of Reserve. It reports false
// when the ring cannot currently provide a contiguous run.
func (p *Pool) TryReserve(n int) (*Reservation, bool, error) {
	if n <= 0 {
		return nil, false, fmt.Errorf("ringpool: reservation size must be positive, got %d", n)
	}
	k := (n + p.blockSize - 1) / p.blockSize
	if k > p.nblocks {
		return nil, false, ErrTooLarge
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, false, ErrClosed
	}
	start, ok := p.canAllocLocked(k)
	if !ok {
		p.mu.Unlock()
		return nil, false, nil
	}
	for i := start; i < start+k; i++ {
		p.refs[i] = 1
	}
	p.freeBlks -= k
	p.writeBlk = (start + k) % p.nblocks
	p.mu.Unlock()

	off := start * p.blockSize
	r := p.resPool.Get().(*Reservation)
	r.pool = p
	r.blk = start
	r.nblk = k
	r.Buf = p.buf[off : off+n : off+n]
	r.done = false
	return r, true, nil
}

// releaseBlocksLocked returns blocks [blk, blk+nblk) to the pool.
func (p *Pool) releaseBlocks(blk, nblk int) {
	p.mu.Lock()
	for i := blk; i < blk+nblk; i++ {
		p.refs[i]--
		if p.refs[i] == 0 {
			p.freeBlks++
		}
		if p.refs[i] < 0 {
			panic("ringpool: block released more times than reserved")
		}
	}
	p.mu.Unlock()
	p.cond.Broadcast()
}

// Close marks the pool closed and wakes all blocked reservers.
func (p *Pool) Close() {
	p.mu.Lock()
	p.closed = true
	p.mu.Unlock()
	p.cond.Broadcast()
}

// Stats is a point-in-time snapshot of pool accounting.
type Stats struct {
	BlockSize         int
	NumBlocks         int
	FreeBlocks        int
	OutstandingSlices int64 // slices currently borrowed
	OutstandingBytes  int64 // payload bytes currently borrowed
	TotalBorrowed     uint64
	TotalReleased     uint64
	TotalStreamed     uint64 // payload bytes ever committed
}

// Stats returns a consistent snapshot of pool accounting.
func (p *Pool) Stats() Stats {
	p.mu.Lock()
	free := p.freeBlks
	p.mu.Unlock()
	return Stats{
		BlockSize:         p.blockSize,
		NumBlocks:         p.nblocks,
		FreeBlocks:        free,
		OutstandingSlices: p.outstandingSlices.Load(),
		OutstandingBytes:  p.outstandingBytes.Load(),
		TotalBorrowed:     p.totalBorrowed.Load(),
		TotalReleased:     p.totalReleased.Load(),
		TotalStreamed:     p.totalStreamed.Load(),
	}
}

// Reservation owns a contiguous run of ring blocks until Commit or
// Cancel is called exactly once.
type Reservation struct {
	pool *Pool
	blk  int
	nblk int
	// Buf is the writable view into the ring. Fill it, then Commit.
	Buf  []byte
	done bool
}

// Commit converts the reservation into a read-only Slice with a
// reference count of one. The reservation is consumed and recycled.
func (r *Reservation) Commit() *Slice {
	if r.done {
		panic("ringpool: reservation already consumed")
	}
	r.done = true
	p := r.pool

	s := p.slicePool.Get().(*Slice)
	s.pool = p
	s.blk = r.blk
	s.nblk = r.nblk
	s.data = r.Buf
	s.refs.Store(1)

	p.outstandingSlices.Add(1)
	p.outstandingBytes.Add(int64(len(s.data)))
	p.totalBorrowed.Add(1)
	p.totalStreamed.Add(uint64(len(s.data)))

	r.Buf = nil
	r.pool = nil
	p.resPool.Put(r)
	return s
}

// Cancel abandons the reservation and returns its blocks to the pool.
func (r *Reservation) Cancel() {
	if r.done {
		panic("ringpool: reservation already consumed")
	}
	r.done = true
	p := r.pool
	p.releaseBlocks(r.blk, r.nblk)
	r.Buf = nil
	r.pool = nil
	p.resPool.Put(r)
}

// Slice is a read-only, reference-counted view into the ring.
// It must be Released exactly once per reference held.
type Slice struct {
	pool *Pool
	blk  int
	nblk int
	data []byte
	refs atomic.Int32
}

// Bytes returns the slice payload. It remains valid until the last
// reference is released.
func (s *Slice) Bytes() []byte { return s.data }

// Len returns the payload length in bytes.
func (s *Slice) Len() int { return len(s.data) }

// Retain acquires an additional reference to the slice.
func (s *Slice) Retain() *Slice {
	if s.refs.Add(1) <= 1 {
		panic("ringpool: Retain on released slice")
	}
	return s
}

// Release drops one reference. When the count reaches zero the
// underlying blocks are returned to the pool and the Slice object is
// recycled. Releasing more references than held panics.
func (s *Slice) Release() {
	n := s.refs.Add(-1)
	if n < 0 {
		panic("ringpool: Release called too many times")
	}
	if n > 0 {
		return
	}
	p := s.pool
	p.outstandingSlices.Add(-1)
	p.outstandingBytes.Add(-int64(len(s.data)))
	p.totalReleased.Add(1)
	p.releaseBlocks(s.blk, s.nblk)
	s.data = nil
	s.pool = nil
	p.slicePool.Put(s)
}
