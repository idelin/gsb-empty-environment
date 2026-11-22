// Package tokenmigrate plans and applies tenant-domain merges and splits for a
// tokenization host.
//
// Tokens are unique only inside an issuing (domain, generation) scope. Legacy
// reads therefore carry an Origin scope. The planner minimizes the number of
// records that need a new token, merges commitments only through verified
// two-party equality proofs, and exposes every denied compatibility read as
// ErrNotFound so tenants cannot distinguish "forbidden" from "absent".
//
// Build is pure. Plan.Materialize applies a plan to a copy of the input and
// returns no partial snapshot on failure.
package tokenmigrate
