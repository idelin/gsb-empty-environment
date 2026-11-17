// Package descriptor validates untrusted device command descriptors.
//
// A descriptor is a table of instructions. Instructions can address device
// memory for reads and writes, and they can point at further descriptor
// tables. Validation is deliberately separate from execution:
//
//  1. Validate recursively decodes every reachable instruction from a frozen
//     snapshot of the backing pages.
//  2. Every write reachable through the descriptor graph is checked against
//     the caller's Authorizer.
//  3. The result is an immutable Plan. Submit freezes every referenced page
//     before handing the complete operation list to the caller's Submitter.
//
// Validation never performs device operations. If any reachable entry is
// malformed, unauthorized, cyclic, or truncated, no Plan is returned and no
// operation can be submitted, so a bad tail entry cannot cause a valid
// prefix to execute.
package descriptor
