package sshchannel

import (
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// halfPipe is an in-memory, net.Pipe-like connection whose CloseWrite is a
// genuine write-side shutdown: the peer observes EOF on Read while both
// directions stay otherwise usable. It lets tests exercise half-close in an
// environment where binding loopback TCP sockets is unavailable.
type halfPipe struct {
	peer *halfPipe

	readMu     sync.Mutex
	readCond   *sync.Cond
	readBuf    []byte
	writeMu    sync.Mutex
	closeOnce  sync.Once
	halfOnce   sync.Once
	closed     chan struct{}
	half       chan struct{}
	closedFlag atomic.Bool

	localAddr  net.Addr
	remoteAddr net.Addr
}

type pipeAddr struct{ name string }

func (a pipeAddr) Network() string { return "halfpipe" }
func (a pipeAddr) String() string  { return a.name }

func newHalfPipePair() (*halfPipe, *halfPipe) {
	a := &halfPipe{
		closed:     make(chan struct{}),
		half:       make(chan struct{}),
		localAddr:  pipeAddr{"pipe-a"},
		remoteAddr: pipeAddr{"pipe-b"},
	}
	b := &halfPipe{
		closed:     make(chan struct{}),
		half:       make(chan struct{}),
		localAddr:  pipeAddr{"pipe-b"},
		remoteAddr: pipeAddr{"pipe-a"},
	}
	a.peer, b.peer = b, a
	a.readCond = sync.NewCond(&a.readMu)
	b.readCond = sync.NewCond(&b.readMu)
	return a, b
}

func (p *halfPipe) Read(b []byte) (int, error) {
	p.readMu.Lock()
	for len(p.readBuf) == 0 {
		// Channel signals are edge-free: a closed channel stays ready, so a
		// half-close or full-close can never be missed by a Cond.Wait that
		// started before the signal arrived.
		select {
		case <-p.half:
			p.readMu.Unlock()
			return 0, io.EOF
		case <-p.closed:
			p.readMu.Unlock()
			return 0, net.ErrClosed
		case <-p.peer.closed:
			p.readMu.Unlock()
			return 0, net.ErrClosed
		default:
		}
		p.readCond.Wait()
	}
	n := copy(b, p.readBuf)
	p.readBuf = p.readBuf[n:]
	p.readMu.Unlock()
	return n, nil
}

func (p *halfPipe) Write(b []byte) (int, error) {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()

	select {
	case <-p.closed:
		return 0, net.ErrClosed
	case <-p.peer.closed:
		return 0, net.ErrClosed
	default:
	}

	cp := make([]byte, len(b))
	copy(cp, b)
	p.peer.readMu.Lock()
	select {
	case <-p.peer.closed:
		p.peer.readMu.Unlock()
		return 0, net.ErrClosed
	default:
	}
	p.peer.readBuf = append(p.peer.readBuf, cp...)
	p.peer.readCond.Signal()
	p.peer.readMu.Unlock()
	return len(b), nil
}

func (p *halfPipe) CloseWrite() error {
	p.halfOnce.Do(func() { close(p.half) })
	p.readCond.Broadcast()
	return nil
}

func (p *halfPipe) Close() error {
	p.closeOnce.Do(func() {
		p.closedFlag.Store(true)
		close(p.closed)
		p.halfOnce.Do(func() { close(p.half) })
	})
	p.readCond.Broadcast()
	if p.peer != nil {
		p.peer.closedFlag.Store(true)
		p.peer.readCond.Broadcast()
	}
	return nil
}

func (p *halfPipe) isClosed() bool { return p.closedFlag.Load() }

func (p *halfPipe) LocalAddr() net.Addr  { return p.localAddr }
func (p *halfPipe) RemoteAddr() net.Addr { return p.remoteAddr }

func (p *halfPipe) SetDeadline(t time.Time) error      { return nil }
func (p *halfPipe) SetReadDeadline(t time.Time) error  { return nil }
func (p *halfPipe) SetWriteDeadline(t time.Time) error { return nil }
