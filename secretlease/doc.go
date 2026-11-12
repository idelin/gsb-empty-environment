// Package secretlease implements step-scoped leases for build secrets such as
// signing keys.
//
// A secret is never handed out as a long-lived value: a [Lease] exposes it
// only through a callback executed in the context of an explicitly authorized
// [Step]. Derived (child) steps do not inherit authorization; every step that
// needs the secret must be granted the lease on its own step ID.
//
// All secret material and every buffer derived from it live in a [MemPool].
// When a step finishes — normally, on callback panic, or on context
// cancellation — the step waits for every concurrent sub-step and in-flight
// callback, then zeroes all of its derived buffers before returning control
// to the caller.
package secretlease
