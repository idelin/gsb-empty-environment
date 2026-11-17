// Package netsec implements a security-ordered CONNECT tunnel ingress.
//
// The ingress authorizes the destination before dialing it, dials before
// reporting success, and reports success before forwarding any byte read from
// the client. Early tunnel data is held in a bounded queue, so an unauthorized
// or failed tunnel cannot leak client bytes to a target.
package netsec
