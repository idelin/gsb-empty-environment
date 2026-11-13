// Package netsec provides fail-closed DoIP routing-activation and diagnostic
// message admission for a vehicular gateway.
//
// A Gateway holds the registered tester source addresses, their authorized
// ECU target addresses, and the one-connection-per-source leases. Each TCP
// connection uses a Session. Complete frames can be passed to Session.Handle,
// while streaming loops can use ReadFrame followed by Session.HandleParts.
//
// Malformed or oversized frames, unsupported payload types, and diagnostics
// that violate the connection's activation binding produce a Decision with
// Close set. Routing activation denials instead return a negative 0x0006
// response so the TCP loop can send it without replacing the current owner.
package netsec
