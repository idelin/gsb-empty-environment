// Package netsec compiles ordered first-match firewall rules into disjoint
// hardware shards without changing the per-packet decision.
//
// A Rule matches on four numeric dimensions — source and destination IPv4
// intervals, source and destination port intervals — plus a set of IP protocol
// numbers, and carries either ActionAllow or ActionDeny. Rules are evaluated in
// caller order; traffic that no rule matches is denied by default.
//
// Compress derives elementary cells from every interval endpoint. Each cell's
// decision is constant because no rule boundary crosses its interior, so the
// cells can be swept and merged along all four dimensions and across protocol
// groups to reduce entry count. Minimality is not attempted, but provenance is
// retained: every Shard records the source rule index, ID and an explanation.
// Cells from different rules are never merged, even with equal actions.
//
// Publish enforces the hardware entry budget and runs Verify before returning.
// Verify probes the finite lattice induced by both rule and shard endpoints at
// each cell's lower and upper corners, covering interval endpoints, numeric
// upper bounds (2^32-1, 2^16-1, protocol 255), nested first-match exceptions
// and the implicit default deny. It also proves that published shards are
// pairwise disjoint. When the budget cannot be met, Compress and Publish fail
// with ErrBudgetExceeded and nothing is released for programming.
package netsec
