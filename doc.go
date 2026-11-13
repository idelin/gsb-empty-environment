// Package netsec implements stateless SYN cookies for a TCP listener.
//
// Cookies authenticate the client/server endpoint pair, the client's initial
// sequence number, the host's full 64-bit time slot, and the negotiated SYN
// options. The option bits stored in the cookie are part of the authenticated
// input, so a forged ACK cannot change the restored MSS, window scale, or
// SACK-permitted state.
package netsec
