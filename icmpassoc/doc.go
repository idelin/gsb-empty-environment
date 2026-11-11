// Package icmpassoc safely binds ICMP error messages to the transport
// connections they actually belong to.
//
// ICMP errors quote the beginning of the offending original IP datagram,
// but an attacker can send spoofed errors to try to force unrelated
// connections to abort. This package therefore never acts on partial
// evidence: HandleICMPError parses the quoted original IP header and only
// as many transport bytes as are present, and notifies a connection only
// when all of the following match:
//
//   - transport protocol (TCP or UDP),
//   - both addresses, after a caller-supplied, version-locked translation,
//   - both ports,
//   - the current active generation of the tuple (a tuple reused after
//     Close gets a new generation and stale errors are rejected), and
//   - for TCP, the quoted sequence number lies in the generation's modular
//     sequence window.
//
// Truncated quotes that lack the bytes required for these checks are
// returned as ErrCannotAssociate rather than matched optimistically.
//
// Address translation is provided by the caller through AddrMapper
// implementations. Each mapper handles exactly one fixed IP version and
// must reject other families, so IPv4 and IPv6 can never be conflated.
package icmpassoc
