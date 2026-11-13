// Package netsec implements ASPA-based AS_PATH verification for a BGP routing
// host.
//
// The package treats AS_PATH as an AS_SEQUENCE in propagation order, from the
// origin AS to the current session neighbor. Adjacent prepends are compressed
// before loop and valley checks while repetition evidence is retained.
// Customer-learned routes are export-eligible after the session edge is
// authenticated. Provider- and peer-learned routes must be valley-free:
// customer-to-provider hops, at most one peer hop, then provider-to-customer
// hops.
//
// RouteStore atomically publishes a dataset version together with all route
// evaluations. A dataset swap re-evaluates only routes containing a customer
// AS whose ASPA record changed.
package netsec
