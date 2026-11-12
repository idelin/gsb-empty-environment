// Package netsec implements a lazy binding component for a Go dynamic
// loader host.
//
// Each imported symbol owns a jump [Slot] with four states:
// unresolved, resolving, bound and failed. The first call through a slot
// runs a host-supplied [Resolver]; concurrent first calls coalesce onto
// that single attempt instead of racing to write the jump target.
//
// Re-entrant resolution (a resolver resolving dependent symbols through
// the same [Binder]) is supported. The resolution chain is tracked per
// call stack, so a recursive cycle is reported with [ErrCycle] instead of
// self-deadlocking, while unrelated goroutines simply wait for the
// in-flight attempt.
//
// A resolved address is never published until the binder verifies that
// the target module is loaded, its signature is valid and the address lies
// inside the module's [Base, Base+Size) range. Publication is an atomic
// state flip after the target is staged, so concurrent callers can never
// observe a half-written jump slot.
//
// Every invalidation (module unload or explicit [Binder.Invalidate]) bumps
// the slot generation. Callbacks belonging to an earlier generation lose
// the right to publish or fail the new slot and receive
// [ErrStaleGeneration]; woken waiters retry on the fresh generation.
package netsec
