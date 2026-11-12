package grpc

import (
	"context"
	"net"
	"sync"

	"google.golang.org/grpc/metadata"
)

type UnaryServerInfo struct {
	Server     any
	FullMethod string
}

type UnaryHandler func(ctx context.Context, req any) (any, error)

type UnaryServerInterceptor func(ctx context.Context, req any, info *UnaryServerInfo, handler UnaryHandler) (any, error)

type StreamServerInfo struct {
	FullMethod     string
	IsClientStream bool
	IsServerStream bool
}

type ServerStream interface {
	SetHeader(metadata.MD) error
	SendHeader(metadata.MD) error
	SetTrailer(metadata.MD)
	Context() context.Context
	SendMsg(any) error
	RecvMsg(any) error
}

type StreamHandler func(srv any, stream ServerStream) error

type StreamServerInterceptor func(srv any, stream ServerStream, info *StreamServerInfo, handler StreamHandler) error

type ServerOption interface {
	apply(*serverOptions)
}

type serverOptions struct {
	unaryInterceptors  []UnaryServerInterceptor
	streamInterceptors []StreamServerInterceptor
}

type funcServerOption struct {
	f func(*serverOptions)
}

func (o funcServerOption) apply(options *serverOptions) {
	o.f(options)
}

func WithUnaryInterceptor(interceptor UnaryServerInterceptor) ServerOption {
	return funcServerOption{f: func(options *serverOptions) {
		options.unaryInterceptors = append(options.unaryInterceptors, interceptor)
	}}
}

func UnaryInterceptor(interceptor UnaryServerInterceptor) ServerOption {
	return WithUnaryInterceptor(interceptor)
}

func ChainUnaryInterceptor(interceptors ...UnaryServerInterceptor) ServerOption {
	return funcServerOption{f: func(options *serverOptions) {
		options.unaryInterceptors = append(options.unaryInterceptors, interceptors...)
	}}
}

func WithStreamInterceptor(interceptor StreamServerInterceptor) ServerOption {
	return funcServerOption{f: func(options *serverOptions) {
		options.streamInterceptors = append(options.streamInterceptors, interceptor)
	}}
}

func StreamInterceptor(interceptor StreamServerInterceptor) ServerOption {
	return WithStreamInterceptor(interceptor)
}

func ChainStreamInterceptor(interceptors ...StreamServerInterceptor) ServerOption {
	return funcServerOption{f: func(options *serverOptions) {
		options.streamInterceptors = append(options.streamInterceptors, interceptors...)
	}}
}

type ServiceDesc struct {
	ServiceName string
	HandlerType any
	Methods     []MethodDesc
	Streams     []StreamDesc
	Metadata    any
}

type MethodDesc struct {
	MethodName string
	Method     any
}

type StreamDesc struct {
	StreamName    string
	Handler       any
	ServerStreams bool
	ClientStreams bool
}

type ServiceInfo struct {
	ServiceType string
	Methods     []any
	Streams     []any
}

type Server struct {
	mu       sync.Mutex
	options  serverOptions
	stop     chan struct{}
	stopOnce sync.Once
}

func NewServer(opts ...ServerOption) *Server {
	s := &Server{stop: make(chan struct{})}
	for _, opt := range opts {
		opt.apply(&s.options)
	}
	return s
}

func (s *Server) RegisterService(desc *ServiceDesc, impl any) {}

func (s *Server) Serve(lis net.Listener) error {
	<-s.stop
	return nil
}

func (s *Server) Stop() {
	s.stopOnce.Do(func() { close(s.stop) })
}

func (s *Server) GracefulStop() {
	s.Stop()
}

func (s *Server) GetServiceInfo() map[string]ServiceInfo {
	return map[string]ServiceInfo{}
}
