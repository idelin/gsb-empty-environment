package netsec

import (
	"net"
	"sync"
	"time"
)

// memPipeEnd 是内存双向管道的一端。写入的数据进入对端的读缓冲，
// 读截止时间通过定时器唤醒等待者，模拟内核套接字的超时语义。
// 标准库 net.Pipe 无内部缓冲，已阻塞的 Read 无法被 SetReadDeadline
// 唤醒，因此无法用于验证非阻塞窥探（Peek）行为。
type memPipeEnd struct {
	mu        sync.Mutex
	cond      *sync.Cond
	peer      *memPipeEnd
	readBuf   []byte
	closed    bool
	readDL    time.Time
	timerMu   sync.Mutex
	readTimer *time.Timer
}

func newMemoryPipe() (net.Conn, net.Conn) {
	a := &memPipeEnd{}
	b := &memPipeEnd{}
	a.cond = sync.NewCond(&a.mu)
	b.cond = sync.NewCond(&b.mu)
	a.peer, b.peer = b, a
	return a, b
}

func (e *memPipeEnd) Read(p []byte) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for len(e.readBuf) == 0 && !e.closed {
		if !e.readDL.IsZero() && !e.readDL.After(time.Now()) {
			return 0, pipeReadTimeout()
		}
		e.cond.Wait()
	}
	if len(e.readBuf) == 0 {
		return 0, net.ErrClosed
	}
	n := copy(p, e.readBuf)
	e.readBuf = e.readBuf[n:]
	return n, nil
}

func (e *memPipeEnd) Write(p []byte) (int, error) {
	dst := e.peer
	dst.mu.Lock()
	defer dst.mu.Unlock()
	if dst.closed || e.closed {
		return 0, net.ErrClosed
	}
	dst.readBuf = append(dst.readBuf, p...)
	dst.cond.Broadcast()
	return len(p), nil
}

func (e *memPipeEnd) Close() error {
	e.mu.Lock()
	e.closed = true
	e.mu.Unlock()
	e.cond.Broadcast()

	e.peer.mu.Lock()
	e.peer.closed = true
	e.peer.mu.Unlock()
	e.peer.cond.Broadcast()
	return nil
}

func (e *memPipeEnd) SetReadDeadline(t time.Time) error {
	e.mu.Lock()
	e.readDL = t
	e.mu.Unlock()

	e.timerMu.Lock()
	if e.readTimer != nil {
		e.readTimer.Stop()
		e.readTimer = nil
	}
	e.timerMu.Unlock()

	if t.IsZero() {
		e.cond.Broadcast()
		return nil
	}
	d := time.Until(t)
	if d <= 0 {
		e.cond.Broadcast()
		return nil
	}
	e.timerMu.Lock()
	e.readTimer = time.AfterFunc(d, e.cond.Broadcast)
	e.timerMu.Unlock()
	return nil
}

func (e *memPipeEnd) SetWriteDeadline(time.Time) error { return nil }
func (e *memPipeEnd) SetDeadline(t time.Time) error    { return e.SetReadDeadline(t) }
func (e *memPipeEnd) LocalAddr() net.Addr              { return memoryAddr{} }
func (e *memPipeEnd) RemoteAddr() net.Addr             { return memoryAddr{} }

type memoryAddr struct{}

func (memoryAddr) Network() string { return "memory" }
func (memoryAddr) String() string  { return "memory-pipe" }

// pipeReadTimeout 返回一个能被 errors.As(err, &net.Error{}) 识别、
// 且 Timeout() 为 true 的错误，与真实套接字的截止时间超时同构。
func pipeReadTimeout() error {
	return &net.OpError{
		Op:     "read",
		Net:    "memory",
		Source: memoryAddr{},
		Addr:   memoryAddr{},
		Err:    pipeTimeoutErr("i/o timeout"),
	}
}

type pipeTimeoutErr string

func (e pipeTimeoutErr) Error() string   { return string(e) }
func (e pipeTimeoutErr) Timeout() bool   { return true }
func (e pipeTimeoutErr) Temporary() bool { return true }
