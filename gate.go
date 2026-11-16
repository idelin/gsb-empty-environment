package netsec

import (
	"context"
	"net"
	"reflect"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

// Gate 按连接来源的监听标识，门控 gRPC 管理类服务的暴露：
// grpc.reflection.*、grpc.channelz.*、grpc.admin.*、grpc.testing.*
// 以及 grpc.health.v1.Health 只允许来自内部监听的连接访问。
//
// 典型用法：
//
//	gate := netsec.NewGate("internal")
//	srv := grpc.NewServer(gate.ServerOptions()...)
//	go srv.Serve(gate.WrapListener(internalLis, "internal")) // 内部监听
//	go srv.Serve(gate.WrapListener(publicLis, "public"))     // 对外监听
//
// 外部连接访问受门控服务时收到 UNIMPLEMENTED（与未知服务一致，
// 不暴露服务存在性）；未经过 WrapListener 标记的连接按外部连接
// 处理（fail closed）。流式方法在读取第一条消息之前即被拒绝。
type Gate struct {
	internal map[any]struct{}

	mu       sync.Mutex
	byRemote map[string]any // 连接远端地址 -> 监听标识（peer 缺少 LocalAddr 时的回退）
}

// NewGate 返回一个门控器。internalIDs 是内部监听标识，必须是非 nil
// 的可比较值（如字符串）。没有任何内部标识时，所有连接都按外部处理。
func NewGate(internalIDs ...any) *Gate {
	g := &Gate{
		internal: make(map[any]struct{}, len(internalIDs)),
		byRemote: make(map[string]any),
	}
	for _, id := range internalIDs {
		checkListenerID(id)
		g.internal[id] = struct{}{}
	}
	return g
}

func checkListenerID(id any) {
	if id == nil || !reflect.TypeOf(id).Comparable() {
		panic("netsec: listener ID must be a non-nil comparable value")
	}
}

// WrapListener 包装一个监听器，把其上接受的连接标记为属于监听标识 id。
// 只有 id 属于 NewGate 给出的内部标识时，这些连接才能访问受门控的服务。
func (g *Gate) WrapListener(lis net.Listener, id any) net.Listener {
	checkListenerID(id)
	return &taggedListener{Listener: lis, gate: g, id: id}
}

// UnaryServerInterceptor 返回门控的一元拦截器。
func (g *Gate) UnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if err := g.check(ctx, info.FullMethod); err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}
}

// StreamServerInterceptor 返回门控的流式拦截器。拦截器在调用处理器
// 之前运行，因此拒绝发生在读取第一条消息之前。
func (g *Gate) StreamServerInterceptor() grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if err := g.check(ss.Context(), info.FullMethod); err != nil {
			return err
		}
		return handler(srv, ss)
	}
}

// ServerOptions 返回装配门控拦截器的服务端选项。
// 若已有其他拦截器，请改用 grpc.ChainUnaryInterceptor /
// grpc.ChainStreamInterceptor 组合 UnaryServerInterceptor 与
// StreamServerInterceptor 的返回值。
func (g *Gate) ServerOptions() []grpc.ServerOption {
	return []grpc.ServerOption{
		grpc.UnaryInterceptor(g.UnaryServerInterceptor()),
		grpc.StreamInterceptor(g.StreamServerInterceptor()),
	}
}

// check 判定是否放行一个 RPC。路径非法或越权访问受门控服务时，
// 一律返回 UNIMPLEMENTED（而非 PERMISSION_DENIED），与 gRPC 对
// 未知服务/方法的响应保持一致，避免暴露管理服务的存在。
func (g *Gate) check(ctx context.Context, fullMethod string) error {
	service, ok := classifyPath(fullMethod)
	if !ok {
		return status.Errorf(codes.Unimplemented, "malformed method name: %q", fullMethod)
	}
	if isGatedService(service) && !g.isInternal(ctx) {
		return status.Errorf(codes.Unimplemented, "unknown service %v", service)
	}
	return nil
}

// isInternal 报告 ctx 对应的连接是否来自内部监听。
func (g *Gate) isInternal(ctx context.Context) bool {
	p, ok := peer.FromContext(ctx)
	if !ok {
		return false
	}
	// 首选：本地地址中编码的监听标识（gRPC 会把连接的 LocalAddr
	// 放入 peer.Peer.LocalAddr）。
	if addr, ok := p.LocalAddr.(taggedAddr); ok {
		_, internal := g.internal[addr.id]
		return internal
	}
	// 回退：按远端地址查找 Accept 时记录的监听标识。
	if p.Addr != nil {
		g.mu.Lock()
		id, ok := g.byRemote[p.Addr.String()]
		g.mu.Unlock()
		if ok {
			_, internal := g.internal[id]
			return internal
		}
	}
	return false
}

// taggedListener 把监听标识打到每个接受的连接上。
type taggedListener struct {
	net.Listener
	gate *Gate
	id   any
}

func (l *taggedListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	conn := &taggedConn{Conn: c, gate: l.gate, id: l.id, remote: c.RemoteAddr().String()}
	l.gate.mu.Lock()
	l.gate.byRemote[conn.remote] = l.id
	l.gate.mu.Unlock()
	return conn, nil
}

// taggedConn 携带所属监听标识的连接。
type taggedConn struct {
	net.Conn
	gate   *Gate
	id     any
	remote string
}

// LocalAddr 把监听标识编码进本地地址，随 peer.Peer.LocalAddr 传给拦截器。
func (c *taggedConn) LocalAddr() net.Addr {
	return taggedAddr{Addr: c.Conn.LocalAddr(), id: c.id}
}

func (c *taggedConn) Close() error {
	c.gate.mu.Lock()
	delete(c.gate.byRemote, c.remote)
	c.gate.mu.Unlock()
	return c.Conn.Close()
}

// taggedAddr 在 net.Addr 上附带监听标识。
type taggedAddr struct {
	net.Addr
	id any
}
