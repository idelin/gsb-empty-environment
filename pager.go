package main

import (
	"errors"
	"io"
	"sync"
)

// blockSize is the granularity at which the source stream is paged. Only
// blocks still referenced by an active cursor have to be retained.
const blockSize = 64 * 1024

const maxLineLength = 64 * 1024

var (
	errLineTooLong = errors.New("line exceeds maximum length")
	errCRLF        = errors.New("expected CRLF line ending")
)

// pager presents a random-access, offset-addressed view over a sequential
// byte stream. Data is fetched in fixed-size blocks and blocks are dropped
// only after every active cursor has advanced into a later block, so retained
// memory is bounded by the spread between cursors rather than by the size of
// the stream or any message body.
type pager struct {
	src io.Reader

	mu       sync.Mutex
	cond     *sync.Cond
	reading  bool
	frontier int64
	closed   bool
	srcErr   error
	blocks   map[int64][]byte
	cursors  map[*Cursor]struct{}
}

func newPager(src io.Reader) *pager {
	p := &pager{
		src:     src,
		blocks:  make(map[int64][]byte),
		cursors: make(map[*Cursor]struct{}),
	}
	p.cond = sync.NewCond(&p.mu)
	return p
}

// newCursor creates a cursor positioned at the given absolute offset.
func (p *pager) newCursor(off int64) *Cursor {
	p.mu.Lock()
	defer p.mu.Unlock()
	c := &Cursor{p: p, off: off}
	p.cursors[c] = struct{}{}
	return c
}

func (p *pager) release(c *Cursor) {
	p.mu.Lock()
	delete(p.cursors, c)
	p.cond.Broadcast()
	p.mu.Unlock()
}

// collectGarbage reclaims unreachable blocks. The engine calls it only once
// both framing goroutines have released their cursors, so no reader can still
// reference an older block.
func (p *pager) collectGarbage() {
	p.mu.Lock()
	p.gc()
	p.mu.Unlock()
}

// minCursor reports the smallest offset still held by an active cursor, or
// -1 when no cursor exists. Caller must hold p.mu.
func (p *pager) minCursor() int64 {
	first := true
	var min int64
	for c := range p.cursors {
		if first || c.off < min {
			min = c.off
			first = false
		}
	}
	if first {
		return -1
	}
	return min
}

// gc drops blocks that no active cursor can reach anymore. Every block
// containing any cursor position is retained, which keeps all current and
// in-flight reads safe. Caller must hold p.mu.
func (p *pager) gc() {
	if len(p.cursors) == 0 {
		return
	}
	for start := range p.blocks {
		reachable := false
		for c := range p.cursors {
			cursorBlock := c.off / blockSize * blockSize
			if start == cursorBlock {
				reachable = true
				break
			}
		}
		if !reachable {
			delete(p.blocks, start)
		}
	}
}

// readAt reads len(buf) bytes at absolute offset off, blocking until the
// underlying stream catches up. Only one goroutine performs the actual source
// read at a time; the others wait on the condition variable.
func (p *pager) readAt(off int64, buf []byte) (int, error) {
	if len(buf) == 0 {
		return 0, nil
	}
	// Cap each read at its containing block so copyFromBlocks touches exactly
	// one retained block; cross-block reads loop in the caller.
	blockStart := off / blockSize * blockSize
	limit := blockStart + blockSize - off
	if int64(len(buf)) > limit {
		buf = buf[:limit]
	}

	p.mu.Lock()
	for {
		// Re-check availability on every wake-up: data may have been paged
		// while this goroutine waited on another reader.
		if off+int64(len(buf)) <= p.frontier {
			n := p.copyFromBlocks(off, buf)
			p.mu.Unlock()
			return n, nil
		}
		if p.closed {
			p.mu.Unlock()
			return 0, p.srcErr
		}
		if p.reading {
			p.cond.Wait()
			continue
		}

		p.reading = true
		tmp := make([]byte, blockSize)
		p.mu.Unlock()

		n, err := io.ReadFull(p.src, tmp)

		p.mu.Lock()
		p.reading = false
		if n > 0 {
			block := make([]byte, n)
			copy(block, tmp[:n])
			p.blocks[p.frontier] = block
			p.frontier += int64(n)
		}
		if err != nil {
			p.closed = true
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				p.srcErr = io.EOF
			} else {
				p.srcErr = err
			}
		}
		p.cond.Broadcast()
	}
}

// copyFromBlocks copies available bytes starting at off. Reads never straddle
// a block boundary, so exactly one map lookup is needed.
func (p *pager) copyFromBlocks(off int64, buf []byte) int {
	blockStart := off / blockSize * blockSize
	block := p.blocks[blockStart]
	idx := off - blockStart
	return copy(buf, block[idx:])
}

// Cursor is an independent absolute-offset reader over the paged stream.
type Cursor struct {
	p   *pager
	off int64
}

// Offset reports the current absolute byte position.
func (c *Cursor) Offset() int64 {
	c.p.mu.Lock()
	defer c.p.mu.Unlock()
	return c.off
}

// seek moves the cursor to off, which must not be inside a discarded block.
func (c *Cursor) seek(off int64) {
	c.p.mu.Lock()
	c.off = off
	c.p.mu.Unlock()
}

// advance moves a cursor under the pager lock and then reclaims blocks that
// have become unreachable.
func (p *pager) advance(c *Cursor, n int) {
	p.mu.Lock()
	c.off += int64(n)
	p.mu.Unlock()
}

// readByte returns the next byte at the cursor.
func (c *Cursor) readByte() (byte, error) {
	var b [1]byte
	if _, err := c.p.readAt(c.Offset(), b[:]); err != nil {
		return 0, err
	}
	c.p.advance(c, 1)
	return b[0], nil
}

var discardPool = sync.Pool{
	New: func() any {
		b := make([]byte, 32*1024)
		return &b
	},
}

// discard advances the cursor by n bytes without materializing them.
func (c *Cursor) discard(n int64) error {
	bp := discardPool.Get().(*[]byte)
	defer discardPool.Put(bp)
	buf := *bp
	for n > 0 {
		want := int64(len(buf))
		if n < want {
			want = n
		}
		got, err := c.p.readAt(c.Offset(), buf[:want])
		c.p.advance(c, got)
		n -= int64(got)
		if err != nil {
			return err
		}
	}
	return nil
}

// readLine returns the next line without its terminating CRLF (or bare LF).
// It consumes one byte at a time and never buffers more than the line itself.
func (c *Cursor) readLine(limit int) ([]byte, error) {
	line := make([]byte, 0, 128)
	for {
		b, err := c.readByte()
		if err != nil {
			return nil, err
		}
		if b == '\n' {
			if len(line) > 0 && line[len(line)-1] == '\r' {
				line = line[:len(line)-1]
			}
			return line, nil
		}
		if len(line) >= limit {
			return nil, errLineTooLong
		}
		line = append(line, b)
	}
}

// expectCRLF consumes a required CRLF sequence used between chunk data and
// the next chunk-size line.
func (c *Cursor) expectCRLF() error {
	cr, err := c.readByte()
	if err != nil {
		return err
	}
	if cr != '\r' {
		return errCRLF
	}
	lf, err := c.readByte()
	if err != nil {
		return err
	}
	if lf != '\n' {
		return errCRLF
	}
	return nil
}
