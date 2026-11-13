// Package netsec implements replay-resistant RPKI cache synchronisation for a
// Go routing host. Cache responses are canonically encoded, signed with
// Ed25519, bound to a transport generation and committed atomically after the
// complete VRP root has been verified.
package netsec
