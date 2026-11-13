// Package netsec implements unicast reverse path forwarding (uRPF) checks
// for a routing forwarding plane.
//
// A Table is built once from Route entries and can then be shared safely
// across goroutines. Table.Check supports strict, feasible, and loose modes,
// considers all equal-cost best paths in strict mode, and rejects sources
// reached only through blackhole routes. Source addresses are parsed with
// net/netip and support both IPv4 and IPv6.
package netsec
