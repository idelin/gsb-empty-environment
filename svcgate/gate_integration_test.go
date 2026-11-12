package svcgate_test

import (
	"context"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"example.org/netsec/q0490/svcgate"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
	reflectionpb "google.golang.org/grpc/reflection/grpc_reflection_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

const (
	echoServiceName = "example.echo.Echo"
	pingMethod      = "/" + echoServiceName + "/Ping"
	uploadMethod    = "/" + echoServiceName + "/Upload"

	// Lives under a gated prefix so the gate protects a streaming handler we
	// control and can instrument in the tests.
	gatedStreamService = "grpc.testing.GatedStream"
	gatedUploadMethod  = "/" + gatedStreamService + "/Upload"
)

var (
	pingHandlerCalls   atomic.Int64
	uploadHandlerCalls atomic.Int64
)

var echoServiceDesc = grpc.ServiceDesc{
	ServiceName: echoServiceName,
	HandlerType: (*interface{})(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "Ping",
			Handler: func(srv any, ctx context.Context, dec func(any) error, _ grpc.UnaryServerInterceptor) (any, error) {
				pingHandlerCalls.Add(1)
				in := new(emptypb.Empty)
				if err := dec(in); err != nil {
					return nil, err
				}
				return &emptypb.Empty{}, nil
			},
		},
	},
	Streams: []grpc.StreamDesc{
		{
			StreamName:    "Upload",
			ClientStreams: true,
			Handler: func(srv any, stream grpc.ServerStream) error {
				uploadHandlerCalls.Add(1)
				for {
					in := new(emptypb.Empty)
					if err := stream.RecvMsg(in); err == io.EOF {
						return stream.SendAndClose(&emptypb.Empty{})
					} else if err != nil {
						return err
					}
				}
			},
		},
	},
}

var gatedStreamServiceDesc = grpc.ServiceDesc{
	ServiceName: gatedStreamService,
	HandlerType: (*interface{})(nil),
	Streams: []grpc.StreamDesc{
		{
			StreamName:    "Upload",
			ClientStreams: true,
			Handler: func(srv any, stream grpc.ServerStream) error {
				uploadHandlerCalls.Add(1)
				for {
					in := new(emptypb.Empty)
					if err := stream.RecvMsg(in); err == io.EOF {
						return stream.SendAndClose(&emptypb.Empty{})
					} else if err != nil {
						return err
					}
				}
			},
		},
	},
}

type testServers struct {
	externalAddr string
	internalAddr string
	external     *grpc.Server
	internal     *grpc.Server
}

func newTestServers(t *testing.T) *testServers {
	t.Helper()

	externalListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("external listen: %v", err)
	}
	internalListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("internal listen: %v", err)
	}

	newServer := func() *grpc.Server {
		creds := svcgate.GateCredentials(insecure.NewCredentials())
		opts := append(svcgate.ServerOptions(), grpc.Creds(creds))
		srv := grpc.NewServer(opts...)
		srv.RegisterService(&echoServiceDesc, nil)
		srv.RegisterService(&gatedStreamServiceDesc, nil)
		healthServer := health.NewServer()
		healthpb.RegisterHealthServer(srv, healthServer)
		reflection.Register(srv)
		return srv
	}

	external := newServer()
	internal := newServer()
	go func() { _ = external.Serve(externalListener) }()
	go func() { _ = internal.Serve(svcgate.InternalListener(internalListener)) }()

	t.Cleanup(func() {
		external.Stop()
		internal.Stop()
	})

	return &testServers{
		externalAddr: externalListener.Addr().String(),
		internalAddr: internalListener.Addr().String(),
		external:     external,
		internal:     internal,
	}
}

func dial(t *testing.T, addr string) *grpc.ClientConn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := grpc.DialContext(ctx, addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func wantUnimplemented(t *testing.T, err error, label string) {
	t.Helper()
	if status.Code(err) != codes.Unimplemented {
		t.Fatalf("%s: code = %v, want Unimplemented; err = %v", label, status.Code(err), err)
	}
}

func listServices(t *testing.T, ctx context.Context, conn *grpc.ClientConn) error {
	t.Helper()
	client := reflectionpb.NewServerReflectionClient(conn)
	stream, err := client.ServerReflectionInfo(ctx)
	if err != nil {
		return err
	}
	if err := stream.Send(&reflectionpb.ServerReflectionRequest{
		MessageRequests: &reflectionpb.ServerReflectionRequest_ListServices{ListServices: "*"},
	}); err != nil {
		return err
	}
	_, err = stream.Recv()
	return err
}

func TestExternalReflectionAndHealthAreHidden(t *testing.T) {
	servers := newTestServers(t)
	conn := dial(t, servers.externalAddr)
	ctx := context.Background()

	// The reflection service itself is a bidi streaming RPC; the rejection
	// arrives on stream creation, before the first request is read.
	wantUnimplemented(t, listServices(t, ctx, conn), "external reflection stream")

	healthClient := healthpb.NewHealthClient(conn)
	_, err := healthClient.Check(ctx, &healthpb.HealthCheckRequest{})
	wantUnimplemented(t, err, "external health check")

	watchStream, err := healthClient.Watch(ctx, &healthpb.HealthCheckRequest{})
	if err == nil {
		err = watchStream.Recv()
	}
	wantUnimplemented(t, err, "external health Watch")
}

func TestExternalUppercaseAndMalformedPathsAreHidden(t *testing.T) {
	servers := newTestServers(t)
	conn := dial(t, servers.externalAddr)
	ctx := context.Background()

	out := new(emptypb.Empty)
	wantUnimplemented(t,
		conn.Invoke(ctx, "/GRPC.HEALTH.V1.HEALTH/Check", &emptypb.Empty{}, out),
		"uppercase health path",
	)
	wantUnimplemented(t,
		conn.Invoke(ctx, "/GRPC.REFLECTION.v1.ServerReflection/Info", &emptypb.Empty{}, out),
		"uppercase reflection path",
	)
	wantUnimplemented(t,
		conn.Invoke(ctx, "/grpc.health.v1.Health", &emptypb.Empty{}, out),
		"path missing method",
	)
	wantUnimplemented(t,
		conn.Invoke(ctx, "/grpc.health.v1.Health/Check/Extra", &emptypb.Empty{}, out),
		"path with extra segment",
	)
	wantUnimplemented(t,
		conn.Invoke(ctx, "//Check", &emptypb.Empty{}, out),
		"empty service name",
	)
	wantUnimplemented(t,
		conn.Invoke(ctx, "/grpc.health.v1.Health/Che%63k", &emptypb.Empty{}, out),
		"percent-encoded path",
	)
}

func TestExternalStreamingRejectedBeforeFirstMessage(t *testing.T) {
	servers := newTestServers(t)
	conn := dial(t, servers.externalAddr)
	ctx := context.Background()

	uploadHandlerCalls.Store(0)
	stream, err := conn.NewStream(ctx, &grpc.StreamDesc{ClientStreams: true}, gatedUploadMethod)
	if err != nil {
		wantUnimplemented(t, err, "external gated streaming call")
	} else {
		err = stream.SendMsg(&emptypb.Empty{})
		if err == nil {
			err = stream.RecvMsg(new(emptypb.Empty))
		}
		wantUnimplemented(t, err, "external gated streaming call")
	}

	if got := uploadHandlerCalls.Load(); got != 0 {
		t.Fatalf("gated stream handler ran %d times on external connection; want 0 (rejected before first message)", got)
	}

	// Non-gated services keep working, including streaming methods.
	uploadHandlerCalls.Store(0)
	openStream, err := conn.NewStream(ctx, &grpc.StreamDesc{ClientStreams: true}, uploadMethod)
	if err != nil {
		t.Fatalf("open NewStream: %v", err)
	}
	if err := openStream.SendMsg(&emptypb.Empty{}); err != nil {
		t.Fatalf("open SendMsg: %v", err)
	}
	if err := openStream.CloseSend(); err != nil {
		t.Fatalf("open CloseSend: %v", err)
	}
	if err := openStream.RecvMsg(new(emptypb.Empty)); err != nil {
		t.Fatalf("open RecvMsg: %v", err)
	}
	if got := uploadHandlerCalls.Load(); got != 1 {
		t.Fatalf("open stream handler ran %d times; want 1", got)
	}
}

func TestInternalListenerAllowsGatedServices(t *testing.T) {
	servers := newTestServers(t)
	conn := dial(t, servers.internalAddr)
	ctx := context.Background()

	resp, err := reflectionpb.NewServerReflectionClient(conn).ServerReflectionInfo(ctx)
	if err != nil {
		t.Fatalf("internal reflection stream create: %v", err)
	}
	if err := resp.Send(&reflectionpb.ServerReflectionRequest{
		MessageRequests: &reflectionpb.ServerReflectionRequest_ListServices{ListServices: "*"},
	}); err != nil {
		t.Fatalf("internal reflection Send: %v", err)
	}
	reflectionResp, err := resp.Recv()
	if err != nil {
		t.Fatalf("internal reflection Recv: %v", err)
	}
	var foundHealth, foundEcho bool
	for _, service := range reflectionResp.GetListServicesResponse().GetService() {
		switch service.Name {
		case "grpc.health.v1.Health":
			foundHealth = true
		case echoServiceName:
			foundEcho = true
		}
	}
	if !foundHealth || !foundEcho {
		t.Fatalf("internal reflection listed services = %v; want health and echo services", reflectionResp.GetListServicesResponse().GetService())
	}

	healthResp, err := healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{})
	if err != nil {
		t.Fatalf("internal health check: %v", err)
	}
	if healthResp.GetStatus() != healthpb.HealthCheckResponse_SERVING {
		t.Fatalf("internal health status = %v; want SERVING", healthResp.GetStatus())
	}

	uploadHandlerCalls.Store(0)
	gatedStream, err := conn.NewStream(ctx, &grpc.StreamDesc{ClientStreams: true}, gatedUploadMethod)
	if err != nil {
		t.Fatalf("internal gated NewStream: %v", err)
	}
	if err := gatedStream.SendMsg(&emptypb.Empty{}); err != nil {
		t.Fatalf("internal gated SendMsg: %v", err)
	}
	if err := gatedStream.CloseSend(); err != nil {
		t.Fatalf("internal gated CloseSend: %v", err)
	}
	if err := gatedStream.RecvMsg(new(emptypb.Empty)); err != nil {
		t.Fatalf("internal gated RecvMsg: %v", err)
	}
	if got := uploadHandlerCalls.Load(); got != 1 {
		t.Fatalf("internal gated stream handler ran %d times; want 1", got)
	}

	pingHandlerCalls.Store(0)
	if err := conn.Invoke(ctx, pingMethod, &emptypb.Empty{}, new(emptypb.Empty)); err != nil {
		t.Fatalf("internal echo Ping: %v", err)
	}
	if got := pingHandlerCalls.Load(); got != 1 {
		t.Fatalf("echo handler ran %d times; want 1", got)
	}
}

func TestInternalListenerRejectsMalformedPaths(t *testing.T) {
	servers := newTestServers(t)
	conn := dial(t, servers.internalAddr)
	ctx := context.Background()

	out := new(emptypb.Empty)
	for _, path := range []string{
		"/grpc.health.v1.Health",
		"//Check",
		"/grpc.health.v1.Health/Check/Extra",
		"/grpc.health.v1.Health/Che%63k",
	} {
		err := conn.Invoke(ctx, path, &emptypb.Empty{}, out)
		wantUnimplemented(t, err, "internal malformed path "+path)
	}
}

func TestExternalOpenServiceRemainsAvailable(t *testing.T) {
	servers := newTestServers(t)
	conn := dial(t, servers.externalAddr)
	ctx := context.Background()

	pingHandlerCalls.Store(0)
	if err := conn.Invoke(ctx, pingMethod, &emptypb.Empty{}, new(emptypb.Empty)); err != nil {
		t.Fatalf("external echo Ping: %v", err)
	}
	if got := pingHandlerCalls.Load(); got != 1 {
		t.Fatalf("echo handler ran %d times; want 1", got)
	}
}
