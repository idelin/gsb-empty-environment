// Package netsec provides a sequencing-only state machine for the receiving
// side of a TLS 1.2 client handshake.
//
// The machine validates the inbound order
//
//	ServerHello -> Certificate -> ServerKeyExchange (ECDHE only) ->
//	[CertificateRequest] -> ServerHelloDone -> ChangeCipherSpec -> Finished
//
// and refuses to accept a ChangeCipherSpec record until the local
// ClientKeyExchange has been signalled with
// TLS12ClientHandshake.NotifyClientKeyExchangeSent. Any out-of-order,
// duplicate, or missing message aborts the handshake with
// AlertUnexpectedMessage; inbound encryption (see ReadEncrypted) is never
// activated by an aborted pre-CCS flight, so a peer cannot force the client
// onto an empty or unauthenticated key.
//
// Cryptographic verification of message contents (certificate chain, key
// shares, Finished verify_data) is deliberately out of scope: callers parse
// records and feed the machine, then apply their own crypto checks.
package netsec
