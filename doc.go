// Package streamguard defends long-lived XML streams against entity-expansion
// attacks, StartTLS stripping, and plaintext-buffer stanza smuggling.
//
// The package uses only the Go standard library. Its hand-written parser is
// incremental, rejects DTD/ENTITY declarations, never expands custom entities,
// validates UTF-8, and bounds depth, attributes, names, text, comments, and
// buffered bytes.
//
// A Guard enforces the ordered flow:
//
//	StreamOpen -> StartTLS -> ResetBufferAndStream -> SASLAuth -> ResourceBind
//
// Feed returns after StartTLS without parsing pipelined trailing bytes.
// UpgradeTLS/ResetBufferAndStream then clears every unprocessed pre-TLS byte
// before the TLS handshake begins.
package streamguard
