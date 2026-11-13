// Package netsec provides admission checks for a secondary DNS zone server's
// zone-transfer entry points.
//
// NOTIFY messages cause a refresh only when the packet source is one of the
// zone's configured primary servers and the caller-supplied TSIG verification
// succeeds. IXFR responses are checked in full before any zone mutation:
// serials use RFC 1982 arithmetic, every change chunk must form a continuous
// chain, and the terminating SOA must be identical to the response's first
// SOA. Broken responses are discarded atomically and reported as requiring an
// AXFR fallback.
package netsec
