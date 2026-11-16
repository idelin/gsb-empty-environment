package netsec_test

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
	reflectionpb "google.golang.org/grpc/reflection/grpc_reflection_v1alpha"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	netsec "example.org/netsec/q0490"
)

const (
	internalID = "internal-listener"
	externalID = "external-listener"
)

// chatService 是一个注册在受门控前缀 grpc.testing.* 下的双向流服务，
// 用于验证流式调用在读取第一条消息之前即被拦截。
type chatService struct {
	invoked atomic.Int32
}

var chatStreamDesc = &grpc.StreamDesc{
	StreamName:    "Chat",
	ServerStreams: true,
	ClientStreams: true,
}

func (s *chatService) handler(_ any, stream grpc.ServerStream) error {
	s.invoked.Add(1)
	for {
		msg := new(emptypb.Empty)
		if err := stream.RecvMsg(msg); err != nil {
			return err
		}
		if err := stream.SendMsg(msg); err != nil {
			return err
		}
	}
}

// testEnv 在同一个 gRPC 服务端上挂三个监听：内部、外部、未标记。
type testEnv struct {
	internalAddr string
	externalAddr string
	untaggedAddr string
	chat         *chatService
}

func startServer(t *testing.T) *testEnv {
	t.Helper()

	gate := netsec.NewGate(internalID)
	srv := grpc.NewServer(gate.ServerOptions()...)
	reflection.Register(srv)
	healthpb.RegisterHealthServer(srv, health.NewServer())

	chat := &chatService{}
	srv.RegisterService(&grpc.ServiceDesc{
		ServiceName: "grpc.testing.TestService",
		Streams: []grpc.StreamDesc{{
			StreamName:    "Chat",
			Handler:       chat.handler,
			ServerStreams: true,
			ClientStreams: true,
		}},
	}, chat)

	// 普通业务服务：不受门控，任何监听都应可访问。
	srv.RegisterService(&grpc.ServiceDesc{
		ServiceName: "echo.v1.Echo",
		Methods: []grpc.MethodDesc{{
			MethodName: "Ping",
			Handler: func(srv any, ctx context.Context, dec func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
				req := new(emptypb.Empty)
				if err := dec(req); err != nil {
					return nil, err
				}
				handler := func(ctx context.Context, req any) (any, error) {
					return req, nil
				}
				if interceptor == nil {
					return handler(ctx, req)
				}
				return interceptor(ctx, req, &grpc.UnaryServerInfo{
					Server:     srv,
					FullMethod: "/echo.v1.Echo/Ping",
				}, handler)
			},
		}},
	}, struct{}{})

	listen := func() net.Listener {
		lis, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("net.Listen: %v", err)
		}
		return lis
	}
	internalLis, externalLis, untaggedLis := listen(), listen(), listen()

	go srv.Serve(gate.WrapListener(internalLis, internalID))
	go srv.Serve(gate.WrapListener(externalLis, externalID))
	go srv.Serve(untaggedLis) // 未标记的监听按外部处理
	t.Cleanup(srv.Stop)

	return &testEnv{
		internalAddr: internalLis.Addr().String(),
		externalAddr: externalLis.Addr().String(),
		untaggedAddr: untaggedLis.Addr().String(),
		chat:         chat,
	}
}

func dial(t *testing.T, addr string) *grpc.ClientConn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cc, err := grpc.DialContext(ctx, addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		t.Fatalf("grpc.DialContext(%q): %v", addr, err)
	}
	t.Cleanup(func() { cc.Close() })
	return cc
}

func requireCode(t *testing.T, err error, code codes.Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error with code %v, got nil", code)
	}
	if got := status.Code(err); got != code {
		t.Fatalf("status.Code(err) = %v, want %v (err = %v)", got, code, err)
	}
}

func listServices(t *testing.T, cc *grpc.ClientConn) (*reflectionpb.ListServicesResponse, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stream, err := reflectionpb.NewServerReflectionClient(cc).ServerReflectionInfo(ctx)
	if err != nil {
		t.Fatalf("ServerReflectionInfo: %v", err)
	}
	// 服务端拒绝时 Send 可能直接报错，真正的状态码以 Recv 为准。
	sendErr := stream.Send(&reflectionpb.ServerReflectionRequest{
		Request: &reflectionpb.ServerReflectionRequest_ListServices{ListServices: "*"},
	})
	resp, err := stream.Recv()
	if err != nil {
		return nil, err
	}
	if sendErr != nil {
		return nil, sendErr
	}
	return resp.GetListServicesResponse(), nil
}

// 外部连接访问反射：应得到 UNIMPLEMENTED 而非 PERMISSION_DENIED。
func TestExternalReflectionDenied(t *testing.T) {
	env := startServer(t)
	cc := dial(t, env.externalAddr)

	_, err := listServices(t, cc)
	requireCode(t, err, codes.Unimplemented)
	if status.Code(err) == codes.PermissionDenied {
		t.Fatal("反射服务不得返回 PERMISSION_DENIED，以免暴露其存在")
	}
}

// 大小写变体的路径：同样只得到 UNIMPLEMENTED。
func TestCaseVariantReflectionDenied(t *testing.T) {
	env := startServer(t)
	cc := dial(t, env.externalAddr)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stream, err := cc.NewStream(ctx, chatStreamDesc, "/GRPC.REFLECTION.v1alpha.ServerReflection/ServerReflectionInfo")
	if err != nil {
		t.Fatalf("NewStream: %v", err)
	}
	requireCode(t, stream.RecvMsg(new(emptypb.Empty)), codes.Unimplemented)
}

// 外部连接访问健康检查（一元方法）：UNIMPLEMENTED。
func TestExternalHealthDenied(t *testing.T) {
	env := startServer(t)
	cc := dial(t, env.externalAddr)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := healthpb.NewHealthClient(cc).Check(ctx, &healthpb.HealthCheckRequest{})
	requireCode(t, err, codes.Unimplemented)
}

// 外部连接的流式调用：在读取第一条消息之前即被拒绝，
// 服务端处理器不应被调用。
func TestExternalStreamingDeniedBeforeFirstMessage(t *testing.T) {
	env := startServer(t)
	cc := dial(t, env.externalAddr)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stream, err := cc.NewStream(ctx, chatStreamDesc, "/grpc.testing.TestService/Chat")
	if err != nil {
		t.Fatalf("NewStream: %v", err)
	}
	// 不发送任何消息，直接接收：应立刻收到 UNIMPLEMENTED。
	requireCode(t, stream.RecvMsg(new(emptypb.Empty)), codes.Unimplemented)
	if n := env.chat.invoked.Load(); n != 0 {
		t.Fatalf("流式处理器被调用了 %d 次，期望 0 次（拒绝应发生在读取第一条消息之前）", n)
	}
}

// 未标记监听的连接按外部处理（fail closed）。
func TestUntaggedListenerDenied(t *testing.T) {
	env := startServer(t)
	cc := dial(t, env.untaggedAddr)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := healthpb.NewHealthClient(cc).Check(ctx, &healthpb.HealthCheckRequest{})
	requireCode(t, err, codes.Unimplemented)
}

// 非法路径一律 UNIMPLEMENTED。
func TestMalformedPathsDenied(t *testing.T) {
	env := startServer(t)
	for _, addr := range []string{env.internalAddr, env.externalAddr} {
		cc := dial(t, addr)
		paths := []string{
			"noslash",
			"/svc",
			"//Check",
			"/grpc.health.v1.Health/",
			"/grpc.health.v1.Health/Check/Extra",
			"/grpc.health.v1.Health/%43heck",
			"/grpc.health%2ev1.Health/Check",
			"/GRPC.HEALTH.V1.HEALTH/Check",
		}
		for _, path := range paths {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			err := cc.Invoke(ctx, path, new(emptypb.Empty), new(emptypb.Empty))
			cancel()
			if status.Code(err) != codes.Unimplemented {
				t.Errorf("Invoke(%q) on %s: code = %v, want Unimplemented (err = %v)", path, addr, status.Code(err), err)
			}
		}
	}
}

// 内部监听放行：反射、健康检查与受门控前缀下的流式服务均可用。
func TestInternalListenerAllowed(t *testing.T) {
	env := startServer(t)
	cc := dial(t, env.internalAddr)

	// 反射可用，且能列出受门控服务。
	list, err := listServices(t, cc)
	if err != nil {
		t.Fatalf("内部监听访问反射失败: %v", err)
	}
	names := map[string]bool{}
	for _, s := range list.GetService() {
		names[s.GetName()] = true
	}
	for _, want := range []string{
		"grpc.reflection.v1alpha.ServerReflection",
		"grpc.health.v1.Health",
		"grpc.testing.TestService",
	} {
		if !names[want] {
			t.Errorf("反射服务列表缺少 %q（得到 %v）", want, names)
		}
	}

	// 健康检查可用。
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	healthResp, err := healthpb.NewHealthClient(cc).Check(ctx, &healthpb.HealthCheckRequest{})
	if err != nil {
		t.Fatalf("内部监听访问健康检查失败: %v", err)
	}
	if healthResp.GetStatus() != healthpb.HealthCheckResponse_SERVING {
		t.Errorf("健康检查状态 = %v, want SERVING", healthResp.GetStatus())
	}

	// 受门控前缀下的流式服务可用（回声一条消息）。
	stream, err := cc.NewStream(ctx, chatStreamDesc, "/grpc.testing.TestService/Chat")
	if err != nil {
		t.Fatalf("NewStream: %v", err)
	}
	if err := stream.SendMsg(new(emptypb.Empty)); err != nil {
		t.Fatalf("SendMsg: %v", err)
	}
	if err := stream.RecvMsg(new(emptypb.Empty)); err != nil {
		t.Fatalf("RecvMsg: %v", err)
	}
	if n := env.chat.invoked.Load(); n != 1 {
		t.Errorf("流式处理器被调用 %d 次，期望 1 次", n)
	}
}

// 普通业务服务不受门控影响：外部连接照常访问。
func TestExternalBusinessServiceAllowed(t *testing.T) {
	env := startServer(t)
	cc := dial(t, env.externalAddr)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := cc.Invoke(ctx, "/echo.v1.Echo/Ping", new(emptypb.Empty), new(emptypb.Empty)); err != nil {
		t.Fatalf("外部连接访问普通业务服务失败: %v", err)
	}
}
