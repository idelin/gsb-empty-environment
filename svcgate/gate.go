// Package svcgate provides gRPC server interceptors that hide internal
// services (reflection, channelz, admin, test and health) from listeners
// that are not explicitly marked as internal.
//
// Blocked requests are answered with codes.Unimplemented rather than
// codes.PermissionDenied, so an external caller cannot tell whether the
// service exists at all. The same answer is returned for malformed request
// paths, and streaming RPCs are rejected before the first message is read.
package svcgate

import (
	"context"
	"net"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

// errHidden is intentionally identical in shape to the error grpc-go itself
// returns for an unknown service. Blocked callers see the same response as
// callers asking for a service that does not exist.
var errHidden = status.Error(codes.Unimplemented, "unknown service")

// InternalAuthInfo is the peer.AuthInfo carried by connections accepted from
// a listener wrapped with InternalListener.
type InternalAuthInfo interface {
	// Internal reports that the connection was accepted by an internal
	// listener.
	Internal() bool
	// Underlying returns the AuthInfo produced by the wrapped transport
	// credentials (for example credentials.TLSInfo).
	Underlying() credentials.AuthInfo
}

type internalAuthInfo struct {
	credentials.AuthInfo
}

func (internalAuthInfo) Internal() bool { return true }

func (i internalAuthInfo) Underlying() credentials.AuthInfo { return i.AuthInfo }

// internalConn marks a connection accepted from an internal listener. The
// mark survives until GateCredentials performs the transport handshake.
type internalConn struct {
	net.Conn
}

type internalListener struct {
	net.Listener
}

func (l *internalListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return internalConn{Conn: conn}, nil
}

// InternalListener marks every connection accepted from l as originating
// from an internal listener. Wrap the internal listeners of a grpc.Server
// and leave the external listeners unwrapped. The server must be configured
// with GateCredentials so the mark reaches each RPC context.
func InternalListener(l net.Listener) net.Listener {
	return &internalListener{Listener: l}
}

// gateCredentials wraps transport credentials and converts the internal
// connection mark into an InternalAuthInfo attached to the peer.
type gateCredentials struct {
	base credentials.TransportCredentials
}

// GateCredentials wraps the server transport credentials (for example
// insecure.NewCredentials or credentials.NewTLS) so that connections accepted
// from an InternalListener carry an internal identity on the peer. Pass the
// result to grpc.Creds.
func GateCredentials(base credentials.TransportCredentials) credentials.TransportCredentials {
	return &gateCredentials{base: base}
}

func (c *gateCredentials) ServerHandshake(rawConn net.Conn) (net.Conn, credentials.AuthInfo, error) {
	marked, ok := rawConn.(internalConn)
	if !ok {
		return c.base.ServerHandshake(rawConn)
	}
	conn, authInfo, err := c.base.ServerHandshake(marked.Conn)
	if err != nil {
		return nil, nil, err
	}
	return conn, internalAuthInfo{AuthInfo: authInfo}, nil
}

func (c *gateCredentials) ClientHandshake(ctx context.Context, authority string, rawConn net.Conn) (net.Conn, credentials.AuthInfo, error) {
	return c.base.ClientHandshake(ctx, authority, rawConn)
}

func (c *gateCredentials) Info() credentials.ProtocolInfo {
	return c.base.Info()
}

func (c *gateCredentials) Clone() credentials.TransportCredentials {
	return &gateCredentials{base: c.base.Clone()}
}

func (c *gateCredentials) OverrideServerName(serverName string) error {
	return c.base.OverrideServerName(serverName)
}

// IsInternal reports whether ctx belongs to a connection accepted from an
// InternalListener.
func IsInternal(ctx context.Context) bool {
	p, ok := peer.FromContext(ctx)
	if !ok || p == nil || p.AuthInfo == nil {
		return false
	}
	marker, ok := p.AuthInfo.(InternalAuthInfo)
	return ok && marker.Internal()
}

// allow returns errHidden when the RPC must not be exposed on the caller's
// listener. Malformed paths are hidden regardless of the listener identity.
func allow(ctx context.Context, fullMethod string) error {
	service, ok := parseService(fullMethod)
	if !ok {
		return errHidden
	}
	if serviceGated(service) && !IsInternal(ctx) {
		return errHidden
	}
	return nil
}

// UnaryServerInterceptor gates unary RPCs. The check runs before the handler
// is invoked, so a blocked RPC never reaches service code.
func UnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if err := allow(ctx, info.FullMethod); err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}
}

// StreamServerInterceptor gates streaming RPCs. The check runs while the
// stream is being created, before any message is read from the client.
func StreamServerInterceptor() grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if err := allow(ss.Context(), info.FullMethod); err != nil {
			return err
		}
		return handler(srv, ss)
	}
}

// ServerOptions returns the server options that install both gate
// interceptors. Combine them with grpc.Creds(GateCredentials(...)):
//
//	creds := svcgate.GateCredentials(insecure.NewCredentials())
//	opts := append(svcgate.ServerOptions(), grpc.Creds(creds))
//	srv := grpc.NewServer(opts...)
func ServerOptions() []grpc.ServerOption {
	return []grpc.ServerOption{
		grpc.ChainUnaryInterceptor(UnaryServerInterceptor()),
		grpc.ChainStreamInterceptor(StreamServerInterceptor()),
	}
}
