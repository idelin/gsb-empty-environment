package netsec

import "context"

type identityContextKey struct{}

// WithIdentity stores an authenticated identity in ctx. It is useful when
// authentication happens before the gRPC interceptor runs.
func WithIdentity(ctx context.Context, identity Identity) context.Context {
	roles := append([]string(nil), identity.Roles...)
	identity.Roles = roles
	return context.WithValue(ctx, identityContextKey{}, identity)
}

// IdentityFromContext returns the identity stored by WithIdentity.
func IdentityFromContext(ctx context.Context) (Identity, bool) {
	identity, ok := ctx.Value(identityContextKey{}).(Identity)
	if !ok {
		return Identity{}, false
	}
	identity.Roles = append([]string(nil), identity.Roles...)
	return identity, true
}

// WithVerifiedIdentity is an alias for WithIdentity.
func WithVerifiedIdentity(ctx context.Context, identity Identity) context.Context {
	return WithIdentity(ctx, identity)
}

// VerifiedIdentityFromContext is an alias for IdentityFromContext.
func VerifiedIdentityFromContext(ctx context.Context) (Identity, bool) {
	return IdentityFromContext(ctx)
}
