package sessiontakeover

import (
	"context"
	"time"
)

// CredentialInspector decides whether a presented credential is still valid
// at takeover time. A revoked credential must never resume an existing
// session, even if the subject identity matches.
//
// Implementations must be safe for concurrent use. IsRevoked returning
// (false, err) is treated as a denial (fail-closed) and the reason is
// reported as ReasonInspectorUnavailable.
type CredentialInspector interface {
	IsRevoked(ctx context.Context, ref CredentialRef) (bool, error)
}

// AllowAllInspector is a CredentialInspector that never revokes anything.
// It is the default when Config.Inspector is nil.
type AllowAllInspector struct{}

// IsRevoked always reports false.
func (AllowAllInspector) IsRevoked(context.Context, CredentialRef) (bool, error) {
	return false, nil
}

// Clock abstracts time so expiry behaviour is deterministic in tests.
type Clock interface {
	Now() time.Time
}

type wallClock struct{}

func (wallClock) Now() time.Time { return time.Now() }

// Config configures a Registry.
type Config struct {
	// ConflictPolicy selects reject vs. isolate behaviour for same-client-ID
	// connections from different subjects. Defaults to ConflictReject.
	ConflictPolicy ConflictPolicy

	// Inspector checks credential revocation on every connect. When nil,
	// AllowAllInspector is used.
	Inspector CredentialInspector

	// Clock is used for all time decisions. When nil, the wall clock is used.
	Clock Clock

	// DefaultSessionExpiry is used when a ConnectRequest leaves Expiry <= 0.
	// A zero value means sessions never expire while the registry runs.
	DefaultSessionExpiry time.Duration

	// MaxOfflineMessages bounds the per-session offline queue. Zero means
	// unlimited.
	MaxOfflineMessages int

	// InboundQoS2Window bounds how many recent inbound QoS 2 packet IDs are
	// remembered for replay protection. Zero uses a sane default.
	InboundQoS2Window int

	// ReapInterval, when > 0, starts a background goroutine that discards
	// expired sessions at this interval.
	ReapInterval time.Duration
}
