package netsec

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestSanitizeRemovesForgedIdentityCaseInsensitively(t *testing.T) {
	md := metadata.MD{
		"x-user-id":          {"attacker"},
		"X-User-ID":          {"attacker-2"},
		"x-user-roles":       {"admin"},
		"x-tenant-id":        {"victim-tenant"},
		"x-internal-secret":  {"internal"},
		"X-Internal-Session": {"session"},
		"x-request-id":       {"request-1"},
	}

	clean, err := SanitizeMetadata(md)
	if err != nil {
		t.Fatalf("SanitizeMetadata returned error: %v", err)
	}
	for key := range clean {
		if isIdentityKey(key) || strings.HasPrefix(key, InternalPrefix) {
			t.Fatalf("untrusted key %q survived sanitation", key)
		}
	}
	if got := clean.Get("x-request-id"); len(got) != 1 || got[0] != "request-1" {
		t.Fatalf("x-request-id = %v, want [request-1]", got)
	}
}

func TestApplyIdentityKeepsOnlyGatewayValue(t *testing.T) {
	md := metadata.MD{
		"X-USER-ID":      {"attacker", "second-attacker"},
		"X-User-Roles":   {"root"},
		"X-Tenant-Id":    {"other-tenant"},
		"x-request-id":   {"request-1"},
		"x-internal-abc": {"secret"},
	}

	identity := Identity{UserID: "user-42", Roles: []string{"admin", "billing"}, TenantID: "tenant-42"}
	out, err := ApplyIdentity(md, identity)
	if err != nil {
		t.Fatalf("ApplyIdentity returned error: %v", err)
	}
	if got := out.Get(UserIDKey); len(got) != 1 || got[0] != "user-42" {
		t.Fatalf("%s = %v, want exactly [user-42]", UserIDKey, got)
	}
	if got := out.Get(UserRolesKey); len(got) != 1 || got[0] != "admin,billing" {
		t.Fatalf("%s = %v, want exactly [admin,billing]", UserRolesKey, got)
	}
	if got := out.Get(TenantIDKey); len(got) != 1 || got[0] != "tenant-42" {
		t.Fatalf("%s = %v, want exactly [tenant-42]", TenantIDKey, got)
	}
	if got := out.Get("x-request-id"); len(got) != 1 || got[0] != "request-1" {
		t.Fatalf("x-request-id = %v, want [request-1]", got)
	}
	for key := range out {
		if strings.HasPrefix(key, InternalPrefix) {
			t.Fatalf("internal key %q survived identity injection", key)
		}
	}
}

func TestSanitizeDropsReservedPseudoAndTransportHeaders(t *testing.T) {
	md := metadata.MD{
		"grpc-timeout":            {"1S"},
		"grpc-status":             {"0"},
		"Grpc-Status-Details-Bin": {"AA=="},
		":path":                   {"/v1/users"},
		":authority":              {"backend"},
		"te":                      {"trailers"},
		"TE":                      {"gzip"},
		"content-type":            {"application/grpc"},
		"Content-Type":            {"application/json"},
		"user-agent":              {"gateway"},
		"x-request-id":            {"request-1"},
	}

	clean, err := SanitizeMetadata(md)
	if err != nil {
		t.Fatalf("SanitizeMetadata returned error: %v", err)
	}
	for _, key := range []string{"grpc-timeout", "grpc-status", "grpc-status-details-bin", ":path", ":authority", "te", "content-type"} {
		if got := clean.Get(key); len(got) != 0 {
			t.Fatalf("%q survived with values %v", key, got)
		}
	}
	if got := clean.Get("user-agent"); len(got) != 1 || got[0] != "gateway" {
		t.Fatalf("user-agent = %v, want [gateway]", got)
	}
	if got := clean.Get("x-request-id"); len(got) != 1 || got[0] != "request-1" {
		t.Fatalf("x-request-id = %v, want [request-1]", got)
	}
}

func TestBinaryMetadataValidation(t *testing.T) {
	padded := base64.StdEncoding.EncodeToString([]byte("hello"))
	unpadded := strings.TrimRight(padded, "=")
	md := metadata.MD{
		"x-payload-bin": {padded, unpadded, "not-base64!", "aGVsbG8==="},
		"x-invalid-bin": {"%%%", "A"},
		"x-not-binary":  {"not-base64!"},
	}

	clean, err := SanitizeMetadata(md)
	if err != nil {
		t.Fatalf("SanitizeMetadata returned error: %v", err)
	}
	if got := clean.Get("x-payload-bin"); len(got) != 2 || got[0] != padded || got[1] != unpadded {
		t.Fatalf("x-payload-bin = %v, want two valid values", got)
	}
	if got := clean.Get("x-invalid-bin"); len(got) != 0 {
		t.Fatalf("x-invalid-bin = %v, want none", got)
	}
	if got := clean.Get("x-not-binary"); len(got) != 1 || got[0] != "not-base64!" {
		t.Fatalf("x-not-binary = %v, want [not-base64!]", got)
	}

	maxSized := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("a", MaxBinaryValueBytes)))
	if got := mustSanitize(t, metadata.MD{"x-max-sized-bin": {maxSized}}).Get("x-max-sized-bin"); len(got) != 1 {
		t.Fatalf("x-max-sized-bin = %v, want the 4096-byte value", got)
	}

	tooLarge := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("a", MaxBinaryValueBytes+1)))
	if got := mustSanitize(t, metadata.MD{"x-too-large-bin": {tooLarge}}).Get("x-too-large-bin"); len(got) != 0 {
		t.Fatalf("x-too-large-bin = %v, want none", got)
	}
}

func TestMetadataSizeLimit(t *testing.T) {
	tooLarge := metadata.MD{"x-data": {strings.Repeat("a", MaxMetadataBytes+1)}}
	if _, err := SanitizeMetadata(tooLarge); !errors.Is(err, ErrMetadataTooLarge) {
		t.Fatalf("SanitizeMetadata error = %v, want ErrMetadataTooLarge", err)
	}

	atLimit := metadata.MD{"x-data": {strings.Repeat("a", MaxMetadataBytes-len("x-data"))}}
	if got := MetadataSize(atLimit); got != MaxMetadataBytes {
		t.Fatalf("MetadataSize = %d, want %d", got, MaxMetadataBytes)
	}
	if _, err := SanitizeMetadata(atLimit); err != nil {
		t.Fatalf("metadata exactly at the limit was rejected: %v", err)
	}
}

func TestUnaryInterceptorUsesVerifiedIdentity(t *testing.T) {
	incoming := metadata.MD{
		"x-user-id":    {"attacker"},
		"x-user-roles": {"root"},
		"x-tenant-id":  {"victim"},
		"X-Internal-X": {"secret"},
		"grpc-timeout": {"1S"},
		"x-request-id": {"request-1"},
	}
	ctx := metadata.NewIncomingContext(context.Background(), incoming)

	var authSaw metadata.MD
	authenticate := func(ctx context.Context, md metadata.MD) (Identity, error) {
		authSaw = md.Copy()
		return Identity{UserID: "user-42", Roles: []string{"admin"}, TenantID: "tenant-42"}, nil
	}

	interceptor := UnaryServerInterceptor(authenticate)
	var handlerSaw metadata.MD
	_, err := interceptor(ctx, "request", &grpc.UnaryServerInfo{FullMethod: "/svc/Get"}, func(ctx context.Context, req any) (any, error) {
		handlerSaw, _ = metadata.FromIncomingContext(ctx)
		return "response", nil
	})
	if err != nil {
		t.Fatalf("interceptor returned error: %v", err)
	}
	for key := range authSaw {
		if isIdentityKey(key) || strings.HasPrefix(key, InternalPrefix) || strings.HasPrefix(key, "grpc-") {
			t.Fatalf("authenticator saw untrusted key %q", key)
		}
	}
	if got := handlerSaw.Get(UserIDKey); len(got) != 1 || got[0] != "user-42" {
		t.Fatalf("handler %s = %v, want exactly [user-42]", UserIDKey, got)
	}
	if got := handlerSaw.Get(UserRolesKey); len(got) != 1 || got[0] != "admin" {
		t.Fatalf("handler %s = %v, want exactly [admin]", UserRolesKey, got)
	}
	if got := handlerSaw.Get(TenantIDKey); len(got) != 1 || got[0] != "tenant-42" {
		t.Fatalf("handler %s = %v, want exactly [tenant-42]", TenantIDKey, got)
	}
}

func TestUnaryInterceptorRejectsAuthenticationFailure(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.MD{"x-user-id": {"attacker"}})
	called := false
	interceptor := UnaryServerInterceptor(func(context.Context, metadata.MD) (Identity, error) {
		return Identity{}, errors.New("bad token")
	})

	_, err := interceptor(ctx, nil, &grpc.UnaryServerInfo{}, func(context.Context, any) (any, error) {
		called = true
		return nil, nil
	})
	if err == nil {
		t.Fatal("interceptor accepted a failed authentication")
	}
	if called {
		t.Fatal("handler ran after authentication failure")
	}
	if got := status.Code(err); got != codes.Unauthenticated {
		t.Fatalf("status.Code = %v, want Unauthenticated", got)
	}
}

func TestUnaryInterceptorRejectsOversizedMetadata(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.MD{
		"x-data": {strings.Repeat("a", MaxMetadataBytes)},
	})
	called := false
	interceptor := UnaryServerInterceptor(nil)

	_, err := interceptor(ctx, nil, &grpc.UnaryServerInfo{}, func(context.Context, any) (any, error) {
		called = true
		return nil, nil
	})
	if !errors.Is(err, ErrMetadataTooLarge) {
		t.Fatalf("interceptor error = %v, want metadata-too-large", err)
	}
	if got := status.Code(err); got != codes.ResourceExhausted {
		t.Fatalf("status.Code = %v, want ResourceExhausted", got)
	}
	if called {
		t.Fatal("handler ran with oversized metadata")
	}
}

func TestUnaryInterceptorUsesIdentityFromContextWithoutAuthenticator(t *testing.T) {
	ctx := metadata.NewIncomingContext(
		WithIdentity(context.Background(), Identity{UserID: "user-42", TenantID: "tenant-42"}),
		metadata.MD{"x-user-id": {"attacker"}},
	)
	interceptor := UnaryServerInterceptor()

	var handlerSaw metadata.MD
	_, err := interceptor(ctx, nil, &grpc.UnaryServerInfo{}, func(ctx context.Context, req any) (any, error) {
		handlerSaw, _ = metadata.FromIncomingContext(ctx)
		return nil, nil
	})
	if err != nil {
		t.Fatalf("interceptor returned error: %v", err)
	}
	if got := handlerSaw.Get(UserIDKey); len(got) != 1 || got[0] != "user-42" {
		t.Fatalf("%s = %v, want exactly [user-42]", UserIDKey, got)
	}
	if got := handlerSaw.Get(TenantIDKey); len(got) != 1 || got[0] != "tenant-42" {
		t.Fatalf("%s = %v, want exactly [tenant-42]", TenantIDKey, got)
	}
}

func TestStreamInterceptorSanitizesHandlerContext(t *testing.T) {
	incoming := metadata.MD{
		"X-User-Id":        {"attacker"},
		"x-user-roles":     {"root"},
		"x-internal-token": {"secret"},
		"x-request-id":     {"request-1"},
	}
	stream := &testServerStream{ctx: metadata.NewIncomingContext(context.Background(), incoming)}
	interceptor := StreamServerInterceptor(func(_ context.Context, md metadata.MD) (Identity, error) {
		return Identity{UserID: "user-42", TenantID: "tenant-42"}, nil
	})

	var handlerSaw metadata.MD
	err := interceptor(nil, stream, &grpc.StreamServerInfo{FullMethod: "/svc/Watch"}, func(_ any, stream grpc.ServerStream) error {
		handlerSaw, _ = metadata.FromIncomingContext(stream.Context())
		return nil
	})
	if err != nil {
		t.Fatalf("interceptor returned error: %v", err)
	}
	if got := handlerSaw.Get(UserIDKey); len(got) != 1 || got[0] != "user-42" {
		t.Fatalf("%s = %v, want exactly [user-42]", UserIDKey, got)
	}
	if got := handlerSaw.Get(UserRolesKey); len(got) != 0 {
		t.Fatalf("%s = %v, want none", UserRolesKey, got)
	}
	if got := handlerSaw.Get(TenantIDKey); len(got) != 1 || got[0] != "tenant-42" {
		t.Fatalf("%s = %v, want exactly [tenant-42]", TenantIDKey, got)
	}
	if got := handlerSaw.Get("x-internal-token"); len(got) != 0 {
		t.Fatalf("x-internal-token = %v, want none", got)
	}
}

func mustSanitize(t *testing.T, md metadata.MD) metadata.MD {
	t.Helper()
	clean, err := SanitizeMetadata(md)
	if err != nil {
		t.Fatalf("SanitizeMetadata returned error: %v", err)
	}
	return clean
}

type testServerStream struct {
	ctx context.Context
}

func (s *testServerStream) SetHeader(metadata.MD) error  { return nil }
func (s *testServerStream) SendHeader(metadata.MD) error { return nil }
func (s *testServerStream) SetTrailer(metadata.MD)       {}
func (s *testServerStream) Context() context.Context     { return s.ctx }
func (s *testServerStream) SendMsg(any) error            { return nil }
func (s *testServerStream) RecvMsg(any) error            { return nil }
