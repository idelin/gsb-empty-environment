package ticketadmit_test

import (
	"net"
	"sync"
	"time"
)

// bufferedPipe is an in-memory, full-duplex net.Conn whose Writes never block
// waiting for the peer to concurrently Read: bytes go into a queue and a
// channel signals availability. This removes the rendezvous semantics of
// net.Pipe that otherwise deadlock TLS post-handshake message ordering.
type bufferedPipe struct {
	recv       *pipeBuffer
	peer       *bufferedPipe
	closeOnce  sync.Once
	readMu     sync.Mutex
	writeMu    sync.Mutex
	deadlineMu sync.Mutex
	deadline   time.Time
}

type readRequest struct {
	buf []byte
	out chan readResult
}

type readResult struct {
	n   int
	err error
}

type pipeBuffer struct {
	mu      sync.Mutex
	chunks  [][]byte
	closed  bool
	waiters []readRequest
}

func newPipeBuffer() *pipeBuffer { return &pipeBuffer{} }

func (b *pipeBuffer) write(p []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return net.ErrClosed
	}

	// Satisfy any pending reader directly when possible.
	if len(b.waiters) > 0 && len(b.chunks) == 0 {
		req := b.waiters[0]
		b.waiters = b.waiters[1:]
		n := copy(req.buf, p)
		if n < len(p) {
			b.chunks = append(b.chunks, append([]byte(nil), p[n:]...))
		}
		req.out <- readResult{n: n}
		return nil
	}

	b.chunks = append(b.chunks, append([]byte(nil), p...))
	b.deliverLocked()
	return nil
}

// deliverLocked hands queued bytes to any waiting readers.
func (b *pipeBuffer) deliverLocked() {
	for len(b.waiters) > 0 && len(b.chunks) > 0 {
		req := b.waiters[0]
		n := copy(req.buf, b.chunks[0])
		if n == len(b.chunks[0]) {
			b.chunks = b.chunks[1:]
		} else {
			b.chunks[0] = b.chunks[0][n:]
		}
		b.waiters = b.waiters[1:]
		req.out <- readResult{n: n}
	}
}

func (b *pipeBuffer) enqueueRead(req readRequest) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed && len(b.chunks) == 0 {
		req.out <- readResult{err: net.ErrClosed}
		return
	}
	b.waiters = append(b.waiters, req)
	b.deliverLocked()
}

func (b *pipeBuffer) cancelRead(req readRequest) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i, w := range b.waiters {
		if w.out == req.out {
			b.waiters = append(b.waiters[:i], b.waiters[i+1:]...)
			return
		}
	}
}

func (b *pipeBuffer) close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = true
	for _, w := range b.waiters {
		w.out <- readResult{err: net.ErrClosed}
	}
	b.waiters = nil
}

// newBufferedPipePair returns two connected bufferedPipe endpoints.
func newBufferedPipePair() (net.Conn, net.Conn) {
	a := &bufferedPipe{recv: newPipeBuffer()}
	b := &bufferedPipe{recv: newPipeBuffer()}
	a.peer = b
	b.peer = a
	return a, b
}

func (p *bufferedPipe) Read(b []byte) (int, error) {
	p.readMu.Lock()
	defer p.readMu.Unlock()

	req := readRequest{buf: b, out: make(chan readResult, 1)}
	p.recv.enqueueRead(req)
	// Deadlines are set by the caller; honor them to match net.Conn behavior.
	p.deadlineMu.Lock()
	deadline := p.deadline
	p.deadlineMu.Unlock()
	if deadline.IsZero() {
		r := <-req.out
		return r.n, r.err
	}
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	select {
	case r := <-req.out:
		return r.n, r.err
	case <-timer.C:
		p.recv.cancelRead(req)
		return 0, errTimeout()
	}
}

func (p *bufferedPipe) Write(b []byte) (int, error) {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	if err := p.peer.recv.write(b); err != nil {
		return 0, err
	}
	return len(b), nil
}

func (p *bufferedPipe) Close() error {
	p.closeOnce.Do(p.recv.close)
	return nil
}

func (p *bufferedPipe) LocalAddr() net.Addr  { return pipeAddrValue{} }
func (p *bufferedPipe) RemoteAddr() net.Addr { return pipeAddrValue{} }

func (p *bufferedPipe) SetDeadline(t time.Time) error {
	p.deadlineMu.Lock()
	p.deadline = t
	p.deadlineMu.Unlock()
	return nil
}

func (p *bufferedPipe) SetReadDeadline(t time.Time) error  { return p.SetDeadline(t) }
func (p *bufferedPipe) SetWriteDeadline(t time.Time) error { return p.SetDeadline(t) }

type pipeAddrValue struct{}

func (pipeAddrValue) Network() string { return "buffered-pipe" }
func (pipeAddrValue) String() string  { return "buffered-pipe" }

// errTimeout returns a timeout-compatible error without importing os.
func errTimeout() error { return timeoutError{} }

type timeoutError struct{}

func (timeoutError) Error() string   { return "i/o timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }
