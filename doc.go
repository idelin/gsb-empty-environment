// Package netsec implements connection-local demultiplexing and validation of
// HTTP/3 Datagrams as defined by RFC 9297.
//
// A Dispatcher is scoped to one HTTP/3 connection. The embedding server
// reports request-stream creation, CONNECT request completion, and stream
// closure to the Dispatcher, then passes every received QUIC DATAGRAM payload
// to HandleDatagram. Datagrams are never delivered to an application merely
// because their Quarter Stream ID is numerically valid: the associated stream
// must also have been created by the peer as a bidirectional stream and must
// have been established as an HTTP Datagram channel by a CONNECT request.
package netsec
