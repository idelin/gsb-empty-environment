// Package ringpool implements a fixed-size ring memory pool with
// zero-copy, read-only slices for high-performance network I/O buffering.
//
// The pool pre-allocates a fixed number of equal-size blocks arranged as a
// ring. Producers copy inbound bytes into the ring with Write; consumers
// cut read-only Slice views that reference the ring memory directly, with
// no copying. Each block is reference counted: when all slices borrowed
// from a block are released and the block has been fully consumed, the
// block is returned to the free list for reuse. After warm-up the hot
// path performs no heap allocations, so GC pressure is effectively zero.
package ringpool

import (
	"errors"
	"sync"
)

// ErrClosed is returned by Write and Slice after the pool is closed and
// no buffered data remains.
var ErrClosed = errors.New("ringpool: pool is closed")

// block is one fixed-size slot of the ring.
type block struct {
	buf     []byte // backing memory, allocated once at pool creation
	refs    int32  // outstanding Slice references
	fill    int    // number of valid bytes in buf
	retired bool   // fully consumed by the reader; freed when refs reaches 0
}

// intQueue is a fixed-capacity circular FIFO of block indices. It never
// allocates after construction.
type intQueue struct {
	buf  []int
	head int
	size int
}

func newIntQueue(capacity int) *intQueue {
	return &intQueue{buf: make([]int, capacity)}
}

func (q *intQueue) push(v int) {
	q.buf[(q.head+q.size)%len(q.buf)] = v
	q.size++
}

func (q *intQueue) front() int { return q.buf[q.head] }

func (q *intQueue) pop() {
	q.head = (q.head + 1) % len(q.buf)
	q.size--
}

// Pool is a ring of fixed-size memory blocks. It is safe for concurrent
// use by multiple producers and consumers.
type Pool struct {
	mu       sync.Mutex
	notEmpty sync.Cond // signalled when data becomes available
	notFull  sync.Cond // signalled when a block is freed

	blocks   []block   // the ring; len never changes
	freeList []int     // stack of free block indices, capacity == len(blocks)
	pending  *intQueue // FIFO of sealed blocks holding unread data

	wBlock   int  // index of the block currently being filled, -1 if none
	rOff     int  // read offset within the front pending block
	buffered int  // total bytes available for slicing
	closed   bool // set by Close
}

// New returns a Pool of numBlocks blocks of blockSize bytes each. The
// pool's resident memory is exactly numBlocks*blockSize bytes and never
// grows.
func New(numBlocks, blockSize int) *Pool {
	if numBlocks < 1 || blockSize < 1 {
		panic("ringpool: numBlocks and blockSize must be positive")
	}
	p := &Pool{wBlock: -1, pending: newIntQueue(numBlocks)}
	p.blocks = make([]block, numBlocks)
	p.freeList = make([]int, 0, numBlocks)
	for i := numBlocks - 1; i >= 0; i-- {
		p.blocks[i].buf = make([]byte, blockSize)
		p.freeList = append(p.freeList, i)
	}
	p.notEmpty.L = &p.mu
	p.notFull.L = &p.mu
	return p
}

// Write copies b into the ring, blocking while the ring is full. It
// returns the number of bytes written; a non-nil error is returned only
// if the pool was closed.
func (p *Pool) Write(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for n < len(b) {
		if p.closed {
			return n, ErrClosed
		}
		if p.wBlock < 0 {
			for len(p.freeList) == 0 {
				if p.closed {
					return n, ErrClosed
				}
				p.notFull.Wait()
			}
			p.wBlock = p.freeList[len(p.freeList)-1]
			p.freeList = p.freeList[:len(p.freeList)-1]
			blk := &p.blocks[p.wBlock]
			blk.fill = 0
			blk.retired = false
		}
		blk := &p.blocks[p.wBlock]
		m := copy(blk.buf[blk.fill:], b[n:])
		blk.fill += m
		n += m
		p.buffered += m
		if blk.fill == len(blk.buf) {
			p.pending.push(p.wBlock)
			p.wBlock = -1
		}
		p.notEmpty.Signal()
	}
	return n, nil
}

// Slice returns a read-only Slice of at most max bytes of buffered data,
// referencing ring memory directly without copying. It blocks until at
// least one byte is available or the pool is closed and drained.
//
// A returned Slice may cover at most the unread remainder of a single
// block; callers needing more data should call Slice repeatedly. The
// caller must call Release exactly once when done with the Slice.
func (p *Pool) Slice(max int) (*Slice, error) {
	if max < 1 {
		panic("ringpool: Slice max must be positive")
	}
	p.mu.Lock()
	for p.buffered == 0 {
		if p.closed {
			p.mu.Unlock()
			return nil, ErrClosed
		}
		p.notEmpty.Wait()
	}
	var bi int
	if p.pending.size > 0 {
		bi = p.pending.front()
	} else {
		// Cut the partially filled write block, sealing it so the
		// producer moves on to a fresh block.
		bi = p.wBlock
		p.pending.push(bi)
		p.wBlock = -1
	}
	blk := &p.blocks[bi]
	n := blk.fill - p.rOff
	if n > max {
		n = max
	}
	blk.refs++
	data := blk.buf[p.rOff : p.rOff+n : p.rOff+n]
	p.rOff += n
	p.buffered -= n
	if p.rOff == blk.fill {
		p.pending.pop()
		p.rOff = 0
		blk.retired = true
	}
	s := slicePool.Get().(*Slice)
	s.pool = p
	s.blk = bi
	s.data = data
	p.mu.Unlock()
	return s, nil
}

// release decrements the reference count of a block, returning it to the
// free list once it is both fully consumed and no longer referenced.
func (p *Pool) release(bi int) {
	p.mu.Lock()
	blk := &p.blocks[bi]
	blk.refs--
	if blk.refs == 0 && blk.retired {
		blk.retired = false
		blk.fill = 0
		p.freeList = append(p.freeList, bi)
		p.notFull.Signal()
	}
	p.mu.Unlock()
}

// Close shuts the pool down. Blocked and future Writes fail with
// ErrClosed; Slice continues to return buffered data until drained, then
// returns ErrClosed.
func (p *Pool) Close() {
	p.mu.Lock()
	p.closed = true
	p.notEmpty.Broadcast()
	p.notFull.Broadcast()
	p.mu.Unlock()
}

// Buffered returns the number of bytes currently available for slicing.
func (p *Pool) Buffered() int {
	p.mu.Lock()
	n := p.buffered
	p.mu.Unlock()
	return n
}

// FreeBlocks returns the number of blocks currently on the free list.
func (p *Pool) FreeBlocks() int {
	p.mu.Lock()
	n := len(p.freeList)
	p.mu.Unlock()
	return n
}

// Leased returns the number of blocks with outstanding Slice references.
func (p *Pool) Leased() int {
	p.mu.Lock()
	n := 0
	for i := range p.blocks {
		if p.blocks[i].refs > 0 {
			n++
		}
	}
	p.mu.Unlock()
	return n
}

// NumBlocks returns the number of blocks in the ring.
func (p *Pool) NumBlocks() int { return len(p.blocks) }

// BlockSize returns the size of each block in bytes.
func (p *Pool) BlockSize() int { return len(p.blocks[0].buf) }

// Capacity returns the total resident memory of the pool in bytes.
func (p *Pool) Capacity() int { return len(p.blocks) * len(p.blocks[0].buf) }

// slicePool recycles Slice headers so the steady-state hot path does not
// allocate. A Slice must not be used after Release.
var slicePool = sync.Pool{New: func() interface{} { return new(Slice) }}

// Slice is a read-only, zero-copy view onto a region of one ring block.
// The underlying memory is reused after Release, so the Slice and any
// sub-slices of Bytes must not be retained past Release.
type Slice struct {
	pool *Pool
	blk  int
	data []byte
}

// Bytes returns the read-only view of the borrowed ring memory.
func (s *Slice) Bytes() []byte { return s.data }

// Len returns the number of bytes in the view.
func (s *Slice) Len() int { return len(s.data) }

// Release returns the Slice's block reference to the pool. When the last
// reference to a fully consumed block is released, the block is recycled.
// Release is idempotent-safe to call once; using the Slice afterwards is
// a programming error.
func (s *Slice) Release() {
	if s.pool == nil {
		return
	}
	s.pool.release(s.blk)
	s.pool = nil
	s.blk = 0
	s.data = nil
	slicePool.Put(s)
}
