// Package netsec provides a tamper-resistant price-time priority order
// sorting component for a Go matching engine.
//
// # Security properties
//
// Ordering is derived exclusively from data the engine controls:
//   - price priority (higher bid / lower ask first), and
//   - a monotonically increasing sequence number assigned by the engine when
//     an order is accepted (time priority within one price level).
//
// Client-supplied sequence numbers and timestamps are never read for
// ordering. Cancelling an order removes it from its price-level queue; a
// resubmitted order is treated as a brand new order and receives the next
// sequence number, so it can only land at the tail of its level.
//
// The optional Monitor adds surveillance for self-trade prevention (including
// linked beneficial-owner account groups), wash trade reporting and abnormal
// submission/cancel frequency detection over engine-clock sliding windows.
package netsec
