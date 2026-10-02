package lease

import (
	"fmt"
	"net"
	"sync"
	"time"
)

// Transport 抽象服务端与客户端之间的连接层。
// 生产环境使用 NetTransport（TCP/Unix 套接字）；
// 测试与单机演示使用 MemTransport（基于 net.Pipe 的进程内网络），
// 两者对协议逻辑完全等价。
type Transport interface {
	// Listen 创建一个监听端点，其 Addr() 即可被 Dial 寻址。
	Listen() (net.Listener, error)
	// Dial 连接到指定地址；对端不可达时返回错误（模拟客户端失联）。
	Dial(addr string) (net.Conn, error)
}

// NetTransport 使用真实网络套接字（network 为 "tcp" 或 "unix"）。
type NetTransport struct {
	Network     string
	ListenAddr  string
	DialTimeout time.Duration
}

func (t NetTransport) Listen() (net.Listener, error) {
	return net.Listen(t.Network, t.ListenAddr)
}

func (t NetTransport) Dial(addr string) (net.Conn, error) {
	timeout := t.DialTimeout
	if timeout == 0 {
		timeout = 2 * time.Second
	}
	return net.DialTimeout(t.Network, addr, timeout)
}

// MemTransport 是进程内传输：地址注册表 + net.Pipe 连接对。
// 关闭的监听端点会从注册表移除，Dial 立即失败，等价于对端失联。
type MemTransport struct {
	mu        sync.Mutex
	listeners map[string]*memListener
	nextID    int
}

func NewMemTransport() *MemTransport {
	return &MemTransport{listeners: make(map[string]*memListener)}
}

func (t *MemTransport) Listen() (net.Listener, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.nextID++
	addr := fmt.Sprintf("mem-%d", t.nextID)
	l := &memListener{
		transport: t,
		addr:      addr,
		conns:     make(chan net.Conn, 16),
		done:      make(chan struct{}),
	}
	t.listeners[addr] = l
	return l, nil
}

func (t *MemTransport) Dial(addr string) (net.Conn, error) {
	t.mu.Lock()
	l, ok := t.listeners[addr]
	t.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("dial %s: unreachable", addr)
	}
	client, server := net.Pipe()
	select {
	case l.conns <- server:
		return client, nil
	case <-l.done:
		client.Close()
		server.Close()
		return nil, fmt.Errorf("dial %s: closed", addr)
	}
}

type memListener struct {
	transport *MemTransport
	addr      string
	conns     chan net.Conn
	done      chan struct{}
	closeOnce sync.Once
}

func (l *memListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.done:
		return nil, fmt.Errorf("listener %s closed", l.addr)
	}
}

func (l *memListener) Close() error {
	l.closeOnce.Do(func() {
		close(l.done)
		l.transport.mu.Lock()
		delete(l.transport.listeners, l.addr)
		l.transport.mu.Unlock()
	})
	return nil
}

func (l *memListener) Addr() net.Addr { return memAddr(l.addr) }

type memAddr string

func (a memAddr) Network() string { return "mem" }
func (a memAddr) String() string  { return string(a) }
