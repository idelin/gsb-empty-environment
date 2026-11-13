// Package netsec implements a bounded store-to-load forwarding pipeline used
// to study partial-address aliasing between private and public addresses.
//
// Run executes one concrete input and returns architectural and speculative
// probe traces, per-load forwarding/rollback details, and cycle-by-cycle
// snapshots. Search compares secrets with identical public inputs and reports
// architectural leaks separately from purely speculative leaks.
package netsec
