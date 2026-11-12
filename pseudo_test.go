package netsec

// 本地伪端点测试设施：沙箱不允许监听 TCP，因此用内存管道模拟网络。
// 伪网络维护 "主机名 -> ip:port" 的解析表与 "ip:port -> 端点" 的路由表，
// 拨号后连接按解析结果报告 RemoteAddr，从而可以真实演练
// “实际拨号地址”核对与“业务域名被解析到元数据地址”的场景。

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"testing"
)

type pseudoAddr string

func (a pseudoAddr) Network() string { return "pseudo" }
func (a pseudoAddr) String() string  { return string(a) }

// pseudoConn 包装管道连接，按伪网络拓扑报告对端地址。
type pseudoConn struct {
	net.Conn
	local, remote pseudoAddr
}

func (c pseudoConn) LocalAddr() net.Addr  { return c.local }
func (c pseudoConn) RemoteAddr() net.Addr { return c.remote }

// pseudoEndpoint 是本地伪端点：内存管道上的 HTTP 服务。
type pseudoEndpoint struct {
	addr   string
	conns  chan net.Conn
	server *http.Server
	once   sync.Once
}

func newPseudoEndpoint(t *testing.T, addr string, handler http.Handler) *pseudoEndpoint {
	t.Helper()
	ep := &pseudoEndpoint{addr: addr, conns: make(chan net.Conn, 16)}
	ep.server = &http.Server{Handler: handler}
	go func() { ep.server.Serve(ep) }()
	t.Cleanup(func() { ep.server.Close() })
	return ep
}

func (ep *pseudoEndpoint) Accept() (net.Conn, error) {
	conn, ok := <-ep.conns
	if !ok {
		return nil, errors.New("pseudo: endpoint closed")
	}
	return conn, nil
}

func (ep *pseudoEndpoint) Close() error {
	ep.once.Do(func() { close(ep.conns) })
	return nil
}

func (ep *pseudoEndpoint) Addr() net.Addr { return pseudoAddr(ep.addr) }

func (ep *pseudoEndpoint) dial() (net.Conn, error) {
	client, server := net.Pipe()
	ep.conns <- pseudoConn{Conn: server, local: pseudoAddr(ep.addr), remote: "192.0.2.1:50000"}
	return pseudoConn{Conn: client, local: "192.0.2.1:50000", remote: pseudoAddr(ep.addr)}, nil
}

// pseudoNet 是伪网络：负责主机名解析与按地址路由到伪端点。
type pseudoNet struct {
	endpoints map[string]*pseudoEndpoint // "ip:port" -> 端点
	hosts     map[string]string          // "host:port" -> "ip:port"
}

func newPseudoNet() *pseudoNet {
	return &pseudoNet{
		endpoints: make(map[string]*pseudoEndpoint),
		hosts:     make(map[string]string),
	}
}

func (pn *pseudoNet) addEndpoint(t *testing.T, ipPort string, aliases []string, handler http.Handler) {
	t.Helper()
	pn.endpoints[ipPort] = newPseudoEndpoint(t, ipPort, handler)
	for _, alias := range aliases {
		pn.hosts[alias] = ipPort
	}
}

// dial 模拟“解析 + 拨号”：先按主机表解析，再按落地地址路由，
// 连接对端地址报告为解析后的 ip:port。
func (pn *pseudoNet) dial(_ context.Context, _, address string) (net.Conn, error) {
	target := address
	if mapped, ok := pn.hosts[address]; ok {
		target = mapped
	}
	ep, ok := pn.endpoints[target]
	if !ok {
		return nil, fmt.Errorf("pseudoNet: no route to %s", address)
	}
	return ep.dial()
}
