package leasecache

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
)

// PipeNetwork 是一个进程内的内存网络，提供与 TCP 相同的 Listen/Dial 语义。
// 真实部署使用 TCP（Server.Start / Client.Start）；PipeNetwork 用于本机
// 测试与演示——协议（HTTP/JSON）完全一致，仅替换传输层。
type PipeNetwork struct {
	mu        sync.Mutex
	listeners map[string]*pipeListener
}

// NewPipeNetwork 创建一个空的内存网络。
func NewPipeNetwork() *PipeNetwork {
	return &PipeNetwork{listeners: make(map[string]*pipeListener)}
}

// Listen 在指定名称上监听（类似 TCP 的地址）。
func (n *PipeNetwork) Listen(addr string) (net.Listener, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if _, ok := n.listeners[addr]; ok {
		return nil, errors.New("address already in use: " + addr)
	}
	l := &pipeListener{
		addr:  pipeAddr(addr),
		conns: make(chan net.Conn),
		done:  make(chan struct{}),
	}
	n.listeners[addr] = l
	return l, nil
}

// Dial 连接到指定名称的 listener；对端不存在或已关闭时返回“连接拒绝”。
func (n *PipeNetwork) Dial(addr string) (net.Conn, error) {
	n.mu.Lock()
	l, ok := n.listeners[addr]
	n.mu.Unlock()
	if !ok {
		return nil, errors.New("connection refused: " + addr)
	}
	server, client := net.Pipe()
	select {
	case l.conns <- server:
		return client, nil
	case <-l.done:
		server.Close()
		client.Close()
		return nil, errors.New("connection refused: " + addr)
	}
}

// HTTPTransport 返回一个通过本网络拨号的 http.Transport，
// 使标准 net/http 客户端/服务端可以跑在 PipeNetwork 上。
func (n *PipeNetwork) HTTPTransport() *http.Transport {
	return &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, _, err := net.SplitHostPort(addr)
			if err != nil {
				host = addr
			}
			return n.Dial(host)
		},
	}
}

type pipeAddr string

func (a pipeAddr) Network() string { return "pipe" }
func (a pipeAddr) String() string  { return string(a) }

type pipeListener struct {
	addr  net.Addr
	conns chan net.Conn
	done  chan struct{}
	once  sync.Once
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.done:
		return nil, errors.New("listener closed")
	}
}

func (l *pipeListener) Close() error {
	l.once.Do(func() { close(l.done) })
	return nil
}

func (l *pipeListener) Addr() net.Addr { return l.addr }
