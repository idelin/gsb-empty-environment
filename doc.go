// Package netsec implements a BGP graceful-restart ledger for a Go routing
// host.  It binds stale routes to the peer identity and transport generation
// that installed them, revalidates them against current policy and VRP data,
// and exposes only fresh routes to export and FlowSpec validation paths.
package netsec
