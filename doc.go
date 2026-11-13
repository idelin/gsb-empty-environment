// Package netsec implements resolvable-private-address lookup with
// table-version-pinned tickets and a mandatory long-term-key identity
// challenge.
//
// Address resolution is deliberately not authentication. Resolve only proves
// that one Active record in one immutable table snapshot can derive the
// address. Complete revalidates that ticket and verifies a device signature
// over a host nonce, the address, and the link-layer connection ID before an
// Identity is published.
package netsec
