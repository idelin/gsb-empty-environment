// Package netsec provides password-history verification for an identity
// host's password-change service.
//
// Each history record pins the algorithm, cost, salt and version used to
// derive its hash. Reuse checks always re-derive a candidate password with
// each record's own parameters, so historical parameters are never applied
// to new input. Password changes append atomically under an optimistic
// version condition: a stale version fails without touching the existing
// history, and over-limit trimming only removes the oldest records, never
// the current one.
package netsec
