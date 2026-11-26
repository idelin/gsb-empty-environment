package plancache

import "errors"

// Sentinel errors. Callers can use errors.Is to distinguish hard security
// failures (no operator ran) from ordinary cache misses.
var (
	// ErrUnauthorized means compile-time or revalidation-time authorization
	// failed. No operator callback from the rejected plan is executed.
	ErrUnauthorized = errors.New("plancache: authorization denied")

	// ErrStalePlan means a cache hit failed pre-execution revalidation and
	// must be treated as a miss / recompiled. No operator callback runs.
	ErrStalePlan = errors.New("plancache: stale plan, revalidation failed")

	// ErrImmediateInvalidation means an immediate REVOKE or immediate
	// RLS/view tightening invalidated a waiting plan. The failure pierces
	// the transaction snapshot and no operator callback ever runs.
	ErrImmediateInvalidation = errors.New("plancache: immediate invalidation of waiting plan")

	// ErrPrincipalInactive means the principal was disabled or dropped.
	ErrPrincipalInactive = errors.New("plancache: principal is no longer active")

	// ErrBinding means execution-time structured bindings did not match the
	// compiled parameter type list. No operator callback runs.
	ErrBinding = errors.New("plancache: structured parameter binding mismatch")

	// ErrHandleExpired means a session-local handle was invalidated by a
	// pooled-connection principal switch.
	ErrHandleExpired = errors.New("plancache: session handle expired after principal switch")

	// ErrCompileRejected means recompilation itself failed authorization.
	ErrCompileRejected = errors.New("plancache: recompilation rejected")
)
