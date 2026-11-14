package netsec

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Caller is the authenticated identity supplied by the infrastructure
// service. The service must derive these values from its authentication
// layer, not from request-controlled input.
//
// Token is an optional opaque credential. When a CallerVerifier is configured
// on the Store, the verifier authenticates Token and returns the authoritative
// identity. Callers without a verifier are trusted as already authenticated by
// the host service.
type Caller struct {
	ID          string
	WorkspaceID string
	AccountID   string
	Token       string
}

// CallerVerifier authenticates an opaque caller token. Implementations should
// verify a signed token, a session, or a workload identity and return the
// identity it establishes. Returning an error denies all store operations.
type CallerVerifier interface {
	VerifyCaller(ctx context.Context, caller Caller) (Caller, error)
}

// CallerVerifierFunc adapts a function to CallerVerifier.
type CallerVerifierFunc func(ctx context.Context, caller Caller) (Caller, error)

// VerifyCaller implements CallerVerifier.
func (f CallerVerifierFunc) VerifyCaller(ctx context.Context, caller Caller) (Caller, error) {
	return f(ctx, caller)
}

type callerContextKey struct{}

// ContextWithCaller stores an authenticated caller in a context. Store
// operations fall back to this value when the explicit Caller is empty.
func ContextWithCaller(ctx context.Context, caller Caller) context.Context {
	return context.WithValue(ctx, callerContextKey{}, caller)
}

// CallerFromContext returns the caller stored by ContextWithCaller.
func CallerFromContext(ctx context.Context) (Caller, bool) {
	caller, ok := ctx.Value(callerContextKey{}).(Caller)
	return caller, ok
}

func resolveCaller(ctx context.Context, caller Caller) (Caller, error) {
	ctxCaller, ok := CallerFromContext(ctx)
	if ok {
		merged, err := mergeCaller(caller, ctxCaller)
		if err != nil {
			return Caller{}, err
		}
		caller = merged
	}
	caller.ID = strings.TrimSpace(caller.ID)
	caller.WorkspaceID = strings.TrimSpace(caller.WorkspaceID)
	caller.AccountID = strings.TrimSpace(caller.AccountID)
	if caller.ID == "" {
		return Caller{}, fmt.Errorf("%w: caller id is required", ErrUnauthenticated)
	}
	if caller.WorkspaceID == "" {
		return Caller{}, fmt.Errorf("%w: caller workspace is required", ErrWorkspaceMismatch)
	}
	if caller.AccountID == "" {
		return Caller{}, fmt.Errorf("%w: caller account is required", ErrAccountMismatch)
	}
	return caller, nil
}

func mergeCaller(explicit, fromContext Caller) (Caller, error) {
	if explicit.ID != "" && fromContext.ID != "" && explicit.ID != fromContext.ID {
		return Caller{}, fmt.Errorf("%w: caller id conflicts with the authenticated context", ErrUnauthenticated)
	}
	if explicit.WorkspaceID != "" && fromContext.WorkspaceID != "" && explicit.WorkspaceID != fromContext.WorkspaceID {
		return Caller{}, fmt.Errorf("%w: caller workspace conflicts with the authenticated context", errors.Join(ErrUnauthenticated, ErrWorkspaceMismatch))
	}
	if explicit.AccountID != "" && fromContext.AccountID != "" && explicit.AccountID != fromContext.AccountID {
		return Caller{}, fmt.Errorf("%w: caller account conflicts with the authenticated context", errors.Join(ErrUnauthenticated, ErrAccountMismatch))
	}
	if explicit.Token != "" && fromContext.Token != "" && explicit.Token != fromContext.Token {
		return Caller{}, fmt.Errorf("%w: caller token conflicts with the authenticated context", ErrUnauthenticated)
	}
	if explicit.ID == "" {
		explicit.ID = fromContext.ID
	}
	if explicit.WorkspaceID == "" {
		explicit.WorkspaceID = fromContext.WorkspaceID
	}
	if explicit.AccountID == "" {
		explicit.AccountID = fromContext.AccountID
	}
	if explicit.Token == "" {
		explicit.Token = fromContext.Token
	}
	return explicit, nil
}

// Validate checks that caller and store identity match. It is exported so
// services can fail closed before opening sensitive resources.
func (s *Store) Validate(caller Caller) error {
	c, err := resolveCaller(context.Background(), caller)
	if err != nil {
		return err
	}
	_, err = s.checkCallerIdentity(context.Background(), c)
	return err
}

func (s *Store) checkCallerIdentity(ctx context.Context, caller Caller) (Caller, error) {
	if caller.WorkspaceID != s.workspaceID {
		return Caller{}, fmt.Errorf("%w: caller=%q store=%q", ErrWorkspaceMismatch, caller.WorkspaceID, s.workspaceID)
	}
	if caller.AccountID != s.accountID {
		return Caller{}, fmt.Errorf("%w: caller=%q store=%q", ErrAccountMismatch, caller.AccountID, s.accountID)
	}
	if s.verifyCaller != nil {
		verified, err := s.verifyCaller.VerifyCaller(ctx, caller)
		if err != nil {
			return Caller{}, fmt.Errorf("%w: %v", ErrUnauthenticated, err)
		}
		if verified.ID != "" {
			caller.ID = verified.ID
		}
		if verified.WorkspaceID != "" && verified.WorkspaceID != s.workspaceID {
			return Caller{}, fmt.Errorf("%w: verifier workspace=%q store=%q", ErrWorkspaceMismatch, verified.WorkspaceID, s.workspaceID)
		}
		if verified.AccountID != "" && verified.AccountID != s.accountID {
			return Caller{}, fmt.Errorf("%w: verifier account=%q store=%q", ErrAccountMismatch, verified.AccountID, s.accountID)
		}
	}
	return caller, nil
}
