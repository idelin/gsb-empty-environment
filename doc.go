// Package netsec provides validated listener identifiers for remote
// port-forwarding authorization on a jump host.
//
// A listening endpoint is classified along two independent axes:
//
//   - Family: IPv4, IPv6, or unspecified for the empty host.
//   - Class: empty, wildcard ("0.0.0.0"/"::"), loopback, or a literal IP.
//
// The empty host is deliberately not a wildcard. It carries no address
// decision and can only be widened through an explicit EmptyPolicy; the
// default policy denies it. IPv4-mapped IPv6 literals are canonicalized to
// their embedded IPv4 address and flagged with ListenID.Mapped, so they can
// never satisfy a native IPv6 permission.
//
// Grant requests a listener from a Driver, which owns port allocation: a
// zero port is replaced by the driver's chosen port. The concrete returned
// listener is re-validated against the original Permission before a Ticket
// is bound, and any lease that fails validation or is canceled is released
// back to the driver so no allocation leaks.
package netsec
