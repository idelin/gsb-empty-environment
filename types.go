// Package llcache implements a two-layer (L1/L2) cache that shares a
// monotonically increasing version space, with explicit, honestly
// declared consistency semantics.
//
// Consistency contract (nothing stronger is ever claimed):
//
//   - Strong reads revalidate against L2 on every call. If L2 is
//     unreachable the read fails with ErrUnavailable; it never
//     silently downgrades to a stale L1 hit.
//   - Eventual reads may serve from L1. An L1 entry is only served
//     after validation against applied invalidation notifications
//     (per-key version watermark). If L2 and the loader both fail,
//     an unvalidatable entry may be returned with Stale/Degraded set.
//
// Data source, clock and failure injection are all plain function
// values, so the package has no dependency on any remote cache and is
// fully deterministic under test.
package llcache

import (
	"context"
	"errors"
	"time"
)

var (
	// ErrVersionRollback is returned when a write carries a version
	// less than or equal to the version already committed for the key.
	ErrVersionRollback = errors.New("llcache: version rollback rejected")

	// ErrUnavailable is returned by strong reads when L2 cannot be
	// reached. Strong consistency is never faked in this case.
	ErrUnavailable = errors.New("llcache: L2 unavailable, strong consistency cannot be guaranteed")
)

// Consistency selects the read path. The caller declares what it
// needs; the cache never upgrades the guarantee silently.
type Consistency int

const (
	// Eventual may serve a locally validated L1 entry. During the
	// invalidation-notification delay window it can return the
	// previous version; that is the declared behaviour.
	Eventual Consistency = iota

	// Strong revalidates against L2 on every read. If L2 update
	// notifications are delayed, strong reads bypass the delay by
	// consulting L2 directly; if L2 is down they fail.
	Strong
)

// Value is a cached datum plus its version. Versions are shared
// between L1 and L2 and must increase monotonically per key.
type Value struct {
	Data    string
	Version uint64
}

// Result describes how a read was satisfied, so callers can see
// exactly which guarantee they got.
type Result struct {
	Value    Value
	Source   string // "l1", "l2" or "loader"
	Stale    bool   // entry could not be validated (L2 unreachable)
	Degraded bool   // L2 was unreachable; strong consistency not provided
}

// Invalidation is broadcast by L2 after every committed update.
// Delivery may be delayed; receivers must apply it idempotently.
type Invalidation struct {
	Key     string
	Version uint64
	At      time.Time
}

// Loader fetches the authoritative value from the data source.
// Injected by the caller; no remote client lives in this package.
type Loader func(ctx context.Context, key string) (Value, error)

// Clock supplies time. Injected for deterministic tests.
type Clock func() time.Time

// FailHook injects failures. It is called before the named operation
// ("l2.update", "l2.get", "l2.watermark"); a non-nil error aborts the
// operation as if the remote tier had failed.
type FailHook func(op string) error
