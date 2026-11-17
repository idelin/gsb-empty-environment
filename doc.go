// Package netsec composes syscall filters for a sandbox host.
//
// A caller supplies pure decision tables. A Chain evaluates every installed
// filter and keeps the highest-priority result:
//
//	terminate > deny > notify > allow
//
// When filters return the same action, the most recently installed filter
// supplies the action data (for example, an errno). Appending a filter can
// therefore only preserve or narrow the composed behavior; a later allow can
// never override an earlier deny.
//
// Chains are immutable and safe to share between threads. Host applies a new
// chain to all registered threads as a transaction: the candidate chain is
// validated before any thread is touched, and a host-side installation
// failure rolls back already-applied threads without publishing a partial
// chain.
package netsec
