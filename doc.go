// Package netsec provides an importable TLS 1.3 handshake state machine that
// consumes events reported by a TLS stack.
//
// When a server accepts early data, the machine enters a boundary state and
// requires EndOfEarlyData before it will accept any subsequent handshake
// message. A client that sends Finished directly is rejected with
// ErrMissingEndOfEarlyData. Transports that intentionally omit
// EndOfEarlyData, such as QUIC, can opt out with WithoutEndOfEarlyData or
// TransportQUIC.
package netsec
