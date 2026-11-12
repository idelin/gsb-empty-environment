package netsec

import (
	"context"
	"errors"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// Authenticator validates an already-sanitized incoming request and returns
// the identity established by the authentication step.
type Authenticator func(ctx context.Context, md Metadata) (Identity, error)

// UnaryServerInterceptor strips untrusted metadata, authenticates the caller,
// and exposes only the gateway-written identity to the handler.
//
// If authenticate is omitted, an identity already placed in ctx by an earlier
// authentication middleware is used; requests without one continue with
// sanitized metadata and no identity.
func UnaryServerInterceptor(authenticators ...Authenticator) grpc.UnaryServerInterceptor {
	authenticate := firstAuthenticator(authenticators)
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		cleanCtx, err := sanitizeContext(ctx, authenticate)
		if err != nil {
			return nil, err
		}
		return handler(cleanCtx, req)
	}
}

// StreamServerInterceptor is the stream equivalent of
// UnaryServerInterceptor. The handler receives a ServerStream whose Context
// contains the sanitized metadata.
func StreamServerInterceptor(authenticators ...Authenticator) grpc.StreamServerInterceptor {
	authenticate := firstAuthenticator(authenticators)
	return func(srv any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		cleanCtx, err := sanitizeContext(stream.Context(), authenticate)
		if err != nil {
			return err
		}
		return handler(srv, &contextServerStream{ServerStream: stream, ctx: cleanCtx})
	}
}

// NewUnaryServerInterceptor is an alias for UnaryServerInterceptor.
func NewUnaryServerInterceptor(authenticators ...Authenticator) grpc.UnaryServerInterceptor {
	return UnaryServerInterceptor(authenticators...)
}

// NewStreamServerInterceptor is an alias for StreamServerInterceptor.
func NewStreamServerInterceptor(authenticators ...Authenticator) grpc.StreamServerInterceptor {
	return StreamServerInterceptor(authenticators...)
}

func firstAuthenticator(authenticators []Authenticator) Authenticator {
	if len(authenticators) == 0 {
		return nil
	}
	return authenticators[0]
}

func sanitizeContext(ctx context.Context, authenticate Authenticator) (context.Context, error) {
	incoming, _ := metadata.FromIncomingContext(ctx)
	sanitized, err := SanitizeMetadata(incoming)
	if err != nil {
		return nil, metadataStatus(err)
	}

	authCtx := metadata.NewIncomingContext(ctx, sanitized)
	var identity Identity
	if authenticate != nil {
		identity, err = authenticate(authCtx, sanitized)
		if err != nil {
			return nil, authenticationStatus(err)
		}
	} else {
		identity, _ = IdentityFromContext(ctx)
	}

	forwarded, err := ApplyIdentity(sanitized, identity)
	if err != nil {
		return nil, metadataStatus(err)
	}

	ctx = metadata.NewIncomingContext(ctx, forwarded)
	ctx = metadata.NewOutgoingContext(ctx, forwarded)
	ctx = WithIdentity(ctx, identity)
	return ctx, nil
}

func metadataStatus(err error) error {
	code := codes.InvalidArgument
	if errors.Is(err, ErrMetadataTooLarge) {
		code = codes.ResourceExhausted
	}
	return newStatusError(code, err.Error(), err)
}

func authenticationStatus(err error) error {
	if status.Code(err) != codes.Unknown {
		return err
	}
	return newStatusError(codes.Unauthenticated, "authentication failed: "+err.Error(), err)
}

type grpcStatusError struct {
	code    codes.Code
	message string
	cause   error
}

func newStatusError(code codes.Code, message string, cause error) error {
	return &grpcStatusError{code: code, message: message, cause: cause}
}

func (e *grpcStatusError) Error() string {
	return status.New(e.code, e.message).Error()
}

func (e *grpcStatusError) GRPCStatus() *status.Status {
	return status.New(e.code, e.message)
}

func (e *grpcStatusError) Unwrap() error {
	return e.cause
}

type contextServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *contextServerStream) Context() context.Context {
	return s.ctx
}
