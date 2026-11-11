package connauthz

import (
	"net"
	"sync"
	"time"
)

// bufPipe 是带缓冲的全双工内存连接。net.Pipe 的读写是同步会合的，
// 两个 tls.Conn 在同一组 goroutine 上握手会死锁；缓冲管道允许各自独立推进。
type bufPipe struct {
	rd, wr *pipeSide
}

type pipeSide struct {
	mu       sync.Mutex
	cond     *sync.Cond
	buf      []byte
	closed   bool
	deadline time.Time
}

func newPipeSide() *pipeSide {
	s := &pipeSide{}
	s.cond = sync.NewCond(&s.mu)
	return s
}

// bufferedPipe 返回一对已连接的内存 net.Conn。
func bufferedPipe() (net.Conn, net.Conn) {
	a2b, b2a := newPipeSide(), newPipeSide()
	a := &bufPipe{rd: b2a, wr: a2b}
	b := &bufPipe{rd: a2b, wr: b2a}
	return a, b
}

func (p *bufPipe) Read(b []byte) (int, error) {
	p.rd.mu.Lock()
	defer p.rd.mu.Unlock()
	for len(p.rd.buf) == 0 {
		if p.rd.closed {
			return 0, net.ErrClosed
		}
		if !p.rd.deadline.IsZero() {
			now := time.Now()
			if !now.Before(p.rd.deadline) {
				return 0, &timeoutError{}
			}
			timer := time.AfterFunc(now.Sub(p.rd.deadline)+time.Millisecond, p.rd.cond.Broadcast)
			p.rd.cond.Wait()
			timer.Stop()
			continue
		}
		p.rd.cond.Wait()
	}
	n := copy(b, p.rd.buf)
	p.rd.buf = p.rd.buf[n:]
	return n, nil
}

func (p *bufPipe) Write(b []byte) (int, error) {
	p.wr.mu.Lock()
	defer p.wr.mu.Unlock()
	if p.wr.closed {
		return 0, net.ErrClosed
	}
	p.wr.buf = append(p.wr.buf, b...)
	p.wr.cond.Broadcast()
	return len(b), nil
}

func (p *bufPipe) Close() error {
	p.rd.mu.Lock()
	p.rd.closed = true
	p.rd.cond.Broadcast()
	p.rd.mu.Unlock()

	p.wr.mu.Lock()
	p.wr.closed = true
	p.wr.cond.Broadcast()
	p.wr.mu.Unlock()
	return nil
}

func (p *bufPipe) LocalAddr() net.Addr  { return dummyAddr{} }
func (p *bufPipe) RemoteAddr() net.Addr { return dummyAddr{} }

func (p *bufPipe) SetDeadline(t time.Time) error {
	p.SetReadDeadline(t)
	p.wr.mu.Lock()
	p.wr.deadline = t
	p.wr.mu.Unlock()
	return nil
}

func (p *bufPipe) SetReadDeadline(t time.Time) error {
	p.rd.mu.Lock()
	p.rd.deadline = t
	p.rd.cond.Broadcast()
	p.rd.mu.Unlock()
	return nil
}

func (p *bufPipe) SetWriteDeadline(time.Time) error { return nil }

type dummyAddr struct{}

func (dummyAddr) Network() string { return "bufpipe" }
func (dummyAddr) String() string  { return "bufpipe" }

type timeoutError struct{}

func (*timeoutError) Error() string   { return "i/o timeout" }
func (*timeoutError) Timeout() bool   { return true }
func (*timeoutError) Temporary() bool { return true }
