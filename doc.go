// Package netsec implements a small TLS record state machine that reassembles
// handshake messages across record boundaries.
//
// Machine.FeedRecord accepts decoded records from a TLS stack. Complete
// handshake messages are returned, while an incomplete message remains
// buffered. A close_notify alert ends the connection and discards the
// incomplete message, so bytes from later records can never be joined to it.
// Other alerts also end the connection and are reported as *AlertError.
package netsec
