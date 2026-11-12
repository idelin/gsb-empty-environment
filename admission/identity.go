package admission

import "context"

// Identity describes the origin of a cache write. The embedding layer
// derives it from authenticated request metadata: trusted CI jobs get
// Trusted=true, external pull-request builds stay untrusted and read-only.
type Identity struct {
	// Name identifies the writer (job id, workflow run, principal).
	Name string
	// Source labels where the build came from, e.g. "trunk", "pr/1234".
	Source string
	// Trusted is the only field the admission decision keys on.
	Trusted bool
}

type identityKey struct{}

// WithIdentity attaches the caller identity to a context.
func WithIdentity(ctx context.Context, id Identity) context.Context {
	return context.WithValue(ctx, identityKey{}, id)
}

// IdentityFromContext recovers the identity. With no identity attached the
// zero value is returned, which is untrusted -- missing authentication must
// never default to write access.
func IdentityFromContext(ctx context.Context) Identity {
	id, _ := ctx.Value(identityKey{}).(Identity)
	return id
}
