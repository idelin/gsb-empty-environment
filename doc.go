// Package netsec provides an embeddable, bounded regular-expression matcher
// for security-sensitive gateways.
//
// Patterns are compiled to a Thompson NFA and executed by advancing sets of
// states, so matching never backtracks. Every match takes a caller-supplied
// step budget; budget exhaustion produces a fail-closed block decision instead
// of an unbounded match attempt.
package netsec
