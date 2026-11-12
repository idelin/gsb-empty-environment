// Package netsec implements a signal delivery host for restricted sender
// processes.
//
// Standard signals (1..31) coalesce per target identity generation, while
// realtime signals (32..64) queue individually with an opaque payload. Every
// queued envelope is billed to a tenant under both a per-tenant quota and
// queue slots reserved against a single noisy tenant. The target identity
// generation is captured at enqueue time and authorization is re-evaluated
// against that fixed generation when the envelope is delivered. Cancellation,
// target restart, and delivery each release the billed slot at most once.
package netsec
