// Package netsec implements a tenant-safe inference batching ledger.
//
// A Ledger binds every callback row to the tenant-local request identity,
// immutable canonical row index, input length, output limit, and SHA-256
// identity digest that existed when the batch was formed. Results are applied
// atomically and delivered by row identity, never by callback completion order
// or a compacted queue position. Cancellation, deadlines, and model-generation
// switches remove delivery eligibility without renumbering other rows.
//
// SearchShortestLeak is a deterministic breadth-first search for callback
// mapping bugs. MarkerModel encodes a request identity into its output so a
// cross-tenant delivery can be recognized even when two tenants use the same
// tenant-local request ID.
package netsec
