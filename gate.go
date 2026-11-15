package netsec

import (
	"context"
	"net"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	"example.org/netsec/q0490/internal/pathclass"
)

// errUnimplemented 与 gRPC 对未知服务的默认响应一致，
// 避免通过错误信息区分"服务不存在"与"服务被门控"。
var errUnimplemented = status.Error(codes.Unimplemented, "unknown service")

// Gate 是服务暴露门控：按 :path 的服务名判定，
// grpc.reflection.*、grpc.channelz.*、grpc.admin.*、grpc.testing.*
// 与 grpc.health.v1.Health 仅允许来自内部监听的连接访问；
// 对外监听上的访问以及非法路径一律返回 UNIMPLEMENTED。
//
// 通过 NewExposureGate 给出内部监听标识（net.Listener），
// 门控按连接的服务端本地地址识别其来自哪个监听。
type Gate struct {
	internal []net.Addr
}

// NewExposureGate 创建门控。internal 为内部监听的 net.Listener，
// 只有来自这些监听的连接可以访问受保护服务。
func NewExposureGate(internal ...net.Listener) *Gate {
	g := &Gate{}
	for _, lis := range internal {
		if lis != nil && lis.Addr() != nil {
			g.internal = append(g.internal, lis.Addr())
		}
	}
	return g
}

// UnaryInterceptor 返回一元方法的门控拦截器。
func (g *Gate) UnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if err := g.check(ctx, info.FullMethod); err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}
}

// StreamInterceptor 返回流式方法的门控拦截器。
// 拒绝发生在调用 handler 之前，即读取第一条消息之前。
func (g *Gate) StreamInterceptor() grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if err := g.check(ss.Context(), info.FullMethod); err != nil {
			return err
		}
		return handler(srv, ss)
	}
}

func (g *Gate) check(ctx context.Context, fullMethod string) error {
	switch pathclass.Classify(fullMethod) {
	case pathclass.Allow:
		return nil
	case pathclass.Protected:
		if g.isInternal(ctx) {
			return nil
		}
	}
	return errUnimplemented
}

// isInternal 判断当前连接是否来自内部监听。
// 无法确认来源时按外部处理（fail closed）。
func (g *Gate) isInternal(ctx context.Context) bool {
	p, ok := peer.FromContext(ctx)
	if !ok || p.LocalAddr == nil {
		return false
	}
	for _, addr := range g.internal {
		if addrMatches(addr, p.LocalAddr) {
			return true
		}
	}
	return false
}

// addrMatches 判断连接的服务端本地地址是否属于监听地址。
// TCP 监听绑定到未指定地址（0.0.0.0 / ::）时按端口匹配，
// 否则要求 IP 与端口完全一致；其余网络类型按地址字符串比较。
func addrMatches(listen, local net.Addr) bool {
	la, lok := listen.(*net.TCPAddr)
	pa, pok := local.(*net.TCPAddr)
	if lok && pok {
		if la.Port != pa.Port {
			return false
		}
		return la.IP.IsUnspecified() || la.IP.Equal(pa.IP)
	}
	return listen.Network() == local.Network() && listen.String() == local.String()
}
