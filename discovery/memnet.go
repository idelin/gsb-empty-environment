package discovery

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"
)

// memNet 是进程内内存网络：用 net.Pipe 承载真实的 HTTP 协议栈，
// 使测试与单机演示无需占用任何 TCP/Unix 端口。
type memNet struct {
	mu        sync.Mutex
	listeners map[string]*memListener
	next      int
}

type memAddr string

func (a memAddr) Network() string { return "mem" }
func (a memAddr) String() string  { return string(a) }

type memListener struct {
	addr   memAddr
	conns  chan net.Conn
	closed chan struct{}
	once   sync.Once
}

func (l *memListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.closed:
		return nil, errors.New("listener closed")
	}
}

func (l *memListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

func (l *memListener) Addr() net.Addr { return l.addr }

func (m *memNet) listen(_ string) (net.Listener, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.next++
	addr := fmt.Sprintf("mem-%d", m.next)
	l := &memListener{
		addr:   memAddr(addr),
		conns:  make(chan net.Conn),
		closed: make(chan struct{}),
	}
	m.listeners[addr] = l
	return l, nil
}

func (m *memNet) dial(ctx context.Context, addr string) (net.Conn, error) {
	if host, _, err := net.SplitHostPort(addr); err == nil {
		addr = host // HTTP 传输层会补上默认端口，按主机名查找监听器
	}
	m.mu.Lock()
	l, ok := m.listeners[addr]
	m.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("memnet: no listener on %s", addr)
	}
	client, server := net.Pipe()
	deadline := time.Now().Add(500 * time.Millisecond)
	select {
	case l.conns <- server:
		return client, nil
	case <-l.closed:
		return nil, fmt.Errorf("memnet: listener on %s closed", addr)
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(time.Until(deadline)):
		return nil, fmt.Errorf("memnet: dial %s timed out", addr)
	}
}

// UseInMemoryTransport 将本进程的传输层切换为内存网络，返回恢复函数。
// 生产部署无需调用（默认即为 TCP）；测试与单机演示用它免除端口依赖。
func UseInMemoryTransport() (restore func()) {
	mn := &memNet{listeners: make(map[string]*memListener)}
	oldListen, oldDial := listenFn, dialFn
	listenFn = mn.listen
	dialFn = mn.dial
	return func() { listenFn, dialFn = oldListen, oldDial }
}
