// Package netsec implements SOCKS5 UDP ASSOCIATE admission control.
//
// ParseDatagram validates the RFC 1928 UDP request header. Association binds
// the relay to the TCP control connection, the client address declared by the
// request, one UDP source port, and the caller's forbidden target networks.
package netsec
