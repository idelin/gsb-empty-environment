package netsec

import (
	"context"
	"errors"
	"fmt"
	"time"
)

var (
	// ErrInvalidSTSResult indicates that an STS result was rejected.
	ErrInvalidSTSResult = errors.New("netsec: invalid STS result")
	// ErrCredentialExpired indicates that a result cannot be used for the
	// configured safety margin before its expiry.
	ErrCredentialExpired = errors.New("netsec: credential expired or inside safety margin")
)

// STSResult is the complete result returned by an external credential
// provider. The cache rejects the result unless its tenant, exact role,
// audience, and session-tag digest match the requested identity.
type STSResult struct {
	AccessKeyID       string
	Secret            SecretHandle
	SessionToken      string
	RoleID            string
	TenantID          string
	Audience          string
	SessionTagsDigest Digest
	IssuedAt          time.Time
	ExpiresAt         time.Time
}

// Provider fetches a fresh STS result. Implementations should stop promptly
// when ctx is canceled, but the cache revalidates even results that arrive
// after cancellation.
type Provider interface {
	Fetch(ctx context.Context, identity Identity) (*STSResult, error)
}

// ProviderFunc adapts a function to Provider.
type ProviderFunc func(context.Context, Identity) (*STSResult, error)

// Fetch implements Provider.
func (f ProviderFunc) Fetch(ctx context.Context, identity Identity) (*STSResult, error) {
	return f(ctx, identity)
}

// ResultValidationError describes one rejected STS result field.
type ResultValidationError struct {
	Field  string
	Reason string
}

func (e *ResultValidationError) Error() string {
	return fmt.Sprintf("netsec: invalid STS result field %q: %s", e.Field, e.Reason)
}

// Is allows errors.Is(err, ErrInvalidSTSResult).
func (e *ResultValidationError) Is(target error) bool {
	return target == ErrInvalidSTSResult
}

type validatedCredential struct {
	accessKeyID  string
	sessionToken string
	issuedAt     time.Time
	expiresAt    time.Time
	usableUntil  time.Time
	secret       SecretHandle
}

func validateSTSResult(
	identity Identity,
	result *STSResult,
	now time.Time,
	clockSkew time.Duration,
	safetyMargin time.Duration,
	maxTTL time.Duration,
) (*validatedCredential, error) {
	if result == nil {
		return nil, &ResultValidationError{Field: "result", Reason: "provider returned nil"}
	}

	fail := func(field, reason string) (*validatedCredential, error) {
		if result.Secret != nil {
			_ = result.Secret.Destroy()
		}
		return nil, &ResultValidationError{Field: field, Reason: reason}
	}

	if result.AccessKeyID == "" {
		return fail("accessKeyID", "empty")
	}
	if result.Secret == nil {
		return fail("secret", "missing handle")
	}
	if result.SessionToken == "" {
		return fail("sessionToken", "empty")
	}
	if result.RoleID != identity.RoleID {
		return fail("roleID", fmt.Sprintf("got %q, want %q", result.RoleID, identity.RoleID))
	}
	if result.TenantID != identity.TenantID {
		return fail("tenantID", fmt.Sprintf("got %q, want %q", result.TenantID, identity.TenantID))
	}
	if result.Audience != identity.Audience {
		return fail("audience", fmt.Sprintf("got %q, want %q", result.Audience, identity.Audience))
	}
	if result.SessionTagsDigest != identity.SessionTagsDigest {
		return fail("sessionTagsDigest", "does not match requested identity")
	}
	if result.IssuedAt.IsZero() {
		return fail("issuedAt", "zero timestamp")
	}
	if result.IssuedAt.After(now.Add(clockSkew)) {
		return fail("issuedAt", "issued after current clock plus allowed skew")
	}
	if !result.ExpiresAt.After(result.IssuedAt) {
		return fail("expiresAt", "not later than issuedAt")
	}

	cacheDeadline := result.ExpiresAt
	if maxTTL > 0 {
		if capped := now.Add(maxTTL); capped.Before(cacheDeadline) {
			cacheDeadline = capped
		}
	}
	usableUntil := cacheDeadline.Add(-safetyMargin)
	if !now.Before(usableUntil) {
		if result.Secret != nil {
			_ = result.Secret.Destroy()
		}
		return nil, fmt.Errorf("%w: usable-until %s, now %s", ErrCredentialExpired, usableUntil.UTC(), now.UTC())
	}

	return &validatedCredential{
		accessKeyID:  result.AccessKeyID,
		sessionToken: result.SessionToken,
		issuedAt:     result.IssuedAt,
		expiresAt:    result.ExpiresAt,
		usableUntil:  usableUntil,
		secret:       result.Secret,
	}, nil
}
