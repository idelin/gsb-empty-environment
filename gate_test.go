package netsec_test

import (
	"context"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/interop/grpc_testing"
	"google.golang.org/grpc/reflection"
	rpb "google.golang.org/grpc/reflection/grpc_reflection_v1alpha"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	"example.org/netsec/q0490"
)

// testSvc 是受保护的 grpc.testing.TestService 实现，
// 用于验证流式方法在读取第一条消息之前被拦截。
type testSvc struct {
	grpc_testing.UnimplementedTestServiceServer
	streamingCalled atomic.Bool
}

func (s *testSvc) UnaryCall(ctx context.Context, req *grpc_testing.SimpleRequest) (*grpc_testing.SimpleResponse, error) {
	return &grpc_testing.SimpleResponse{}, nil
}

func (s *testSvc) StreamingCall(stream grpc_testing.TestService_StreamingCallServer) error {
	s.streamingCalled.Store(true)
	for {
		req, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if err := stream.Send(&grpc_testing.StreamingCallResponse{Payload: req.GetPayload()}); err != nil {
			return err
		}
	}
}

// pingService 是普通（非受保护）服务，验证正常流量不受影响。
type pingService interface {
	Ping(context.Context, *emptypb.Empty) (*emptypb.Empty, error)
}

type pingImpl struct{}

func (pingImpl) Ping(ctx context.Context, e *emptypb.Empty) (*emptypb.Empty, error) {
	return e, nil
}

var pingDesc = grpc.ServiceDesc{
	ServiceName: "example.ping.v1.Ping",
	HandlerType: (*pingService)(nil),
	Methods: []grpc.MethodDesc{
		{MethodName: "Ping", Handler: pingHandler},
	},
}

func pingHandler(srv any, ctx context.Context, dec func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	in := new(emptypb.Empty)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(pingService).Ping(ctx, in)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/example.ping.v1.Ping/Ping"}
	handler := func(ctx context.Context, req any) (any, error) {
		return srv.(pingService).Ping(ctx, req.(*emptypb.Empty))
	}
	return interceptor(ctx, in, info, handler)
}

type testEnv struct {
	internalConn *grpc.ClientConn
	externalConn *grpc.ClientConn
	svc          *testSvc
}

func setup(t *testing.T) *testEnv {
	t.Helper()

	internalLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	externalLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	gate := netsec.NewExposureGate(internalLis)
	srv := grpc.NewServer(
		grpc.ChainUnaryInterceptor(gate.UnaryInterceptor()),
		grpc.ChainStreamInterceptor(gate.StreamInterceptor()),
	)
	reflection.Register(srv)

	healthSrv := health.NewServer()
	healthSrv.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	healthpb.RegisterHealthServer(srv, healthSrv)

	svc := &testSvc{}
	grpc_testing.RegisterTestServiceServer(srv, svc)
	srv.RegisterService(&pingDesc, pingImpl{})

	go srv.Serve(internalLis)
	go srv.Serve(externalLis)
	t.Cleanup(srv.Stop)

	dial := func(addr string) *grpc.ClientConn {
		conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { conn.Close() })
		return conn
	}
	return &testEnv{
		internalConn: dial(internalLis.Addr().String()),
		externalConn: dial(externalLis.Addr().String()),
		svc:          svc,
	}
}

func rpcCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func requireCode(t *testing.T, err error, code codes.Code) {
	t.Helper()
	if status.Code(err) != code {
		t.Fatalf("err = %v, want code %v", err, code)
	}
}

// 外部连接访问反射：UNIMPLEMENTED。
func TestExternalReflectionBlocked(t *testing.T) {
	env := setup(t)
	client := rpb.NewServerReflectionClient(env.externalConn)
	stream, err := client.ServerReflectionInfo(rpcCtx(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Send(&rpb.ServerReflectionRequest{
		MessageRequest: &rpb.ServerReflectionRequest_ListServices{ListServices: ""},
	}); err != nil {
		t.Fatal(err)
	}
	_, err = stream.Recv()
	requireCode(t, err, codes.Unimplemented)
}

// 内部连接访问反射：放行，能列出已注册服务。
func TestInternalReflectionAllowed(t *testing.T) {
	env := setup(t)
	client := rpb.NewServerReflectionClient(env.internalConn)
	stream, err := client.ServerReflectionInfo(rpcCtx(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Send(&rpb.ServerReflectionRequest{
		MessageRequest: &rpb.ServerReflectionRequest_ListServices{ListServices: ""},
	}); err != nil {
		t.Fatal(err)
	}
	resp, err := stream.Recv()
	if err != nil {
		t.Fatalf("internal reflection should be allowed: %v", err)
	}
	names := map[string]bool{}
	for _, s := range resp.GetListServicesResponse().GetService() {
		names[s.GetName()] = true
	}
	if !names["example.ping.v1.Ping"] || !names["grpc.health.v1.Health"] {
		t.Fatalf("reflection list missing expected services: %v", names)
	}
}

// 健康检查：外部 UNIMPLEMENTED，内部放行。
func TestHealthGating(t *testing.T) {
	env := setup(t)

	_, err := healthpb.NewHealthClient(env.externalConn).Check(rpcCtx(t), &healthpb.HealthCheckRequest{})
	requireCode(t, err, codes.Unimplemented)

	resp, err := healthpb.NewHealthClient(env.internalConn).Check(rpcCtx(t), &healthpb.HealthCheckRequest{})
	if err != nil {
		t.Fatalf("internal health check should be allowed: %v", err)
	}
	if resp.GetStatus() != healthpb.HealthCheckResponse_SERVING {
		t.Fatalf("status = %v, want SERVING", resp.GetStatus())
	}
}

// grpc.testing.* 一元方法：外部 UNIMPLEMENTED，内部放行。
func TestGrpcTestingGating(t *testing.T) {
	env := setup(t)

	_, err := grpc_testing.NewTestServiceClient(env.externalConn).UnaryCall(rpcCtx(t), &grpc_testing.SimpleRequest{})
	requireCode(t, err, codes.Unimplemented)

	if _, err := grpc_testing.NewTestServiceClient(env.internalConn).UnaryCall(rpcCtx(t), &grpc_testing.SimpleRequest{}); err != nil {
		t.Fatalf("internal UnaryCall should be allowed: %v", err)
	}
}

// 大小写变体：一律 UNIMPLEMENTED，内部连接也不例外。
func TestCaseVariantBlocked(t *testing.T) {
	env := setup(t)
	for _, conn := range []*grpc.ClientConn{env.externalConn, env.internalConn} {
		err := conn.Invoke(rpcCtx(t), "/GRPC.HEALTH.V1.HEALTH/Check", &emptypb.Empty{}, &emptypb.Empty{})
		requireCode(t, err, codes.Unimplemented)

		err = conn.Invoke(rpcCtx(t), "/Grpc.Reflection.V1.ServerReflection/ServerReflectionInfo", &emptypb.Empty{}, &emptypb.Empty{})
		requireCode(t, err, codes.Unimplemented)
	}
}

// 非法路径：多余段、百分号编码、空服务名，一律 UNIMPLEMENTED。
func TestMalformedPathBlocked(t *testing.T) {
	env := setup(t)
	for _, path := range []string{
		"/grpc.health.v1.Health/Check/Extra",
		"/grpc.health.v1.Health/Che%63k",
		"//Check",
	} {
		for _, conn := range []*grpc.ClientConn{env.externalConn, env.internalConn} {
			err := conn.Invoke(rpcCtx(t), path, &emptypb.Empty{}, &emptypb.Empty{})
			requireCode(t, err, codes.Unimplemented)
		}
	}
}

// 流式方法：外部连接在读取第一条消息之前被拒绝，服务端 handler 不执行。
func TestStreamingBlockedBeforeFirstMessage(t *testing.T) {
	env := setup(t)

	client := grpc_testing.NewTestServiceClient(env.externalConn)
	stream, err := client.StreamingCall(rpcCtx(t))
	if err != nil {
		t.Fatal(err)
	}
	_ = stream.Send(&grpc_testing.StreamingCallRequest{})
	_, err = stream.Recv()
	requireCode(t, err, codes.Unimplemented)
	if env.svc.streamingCalled.Load() {
		t.Fatal("streaming handler must not run for blocked external call")
	}

	// 内部连接放行，handler 正常执行。
	istream, err := grpc_testing.NewTestServiceClient(env.internalConn).StreamingCall(rpcCtx(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := istream.Send(&grpc_testing.StreamingCallRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err := istream.Recv(); err != nil {
		t.Fatalf("internal streaming should be allowed: %v", err)
	}
	if !env.svc.streamingCalled.Load() {
		t.Fatal("streaming handler should run for internal call")
	}
}

// 普通服务在对外监听上不受影响。
func TestNormalServiceAllowedExternally(t *testing.T) {
	env := setup(t)
	if err := env.externalConn.Invoke(rpcCtx(t), "/example.ping.v1.Ping/Ping", &emptypb.Empty{}, &emptypb.Empty{}); err != nil {
		t.Fatalf("normal service should be reachable externally: %v", err)
	}
}
