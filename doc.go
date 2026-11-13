// Package netsec adjudicates decoded HTTP/2 request header blocks for a
// protocol gateway.
//
// HTTP/2 forbids the HTTP/1 connection-specific header fields "connection",
// "keep-alive", "transfer-encoding" and "upgrade" (RFC 7540, section 8.1.2.2).
// If such a field survives an HTTP/2 -> HTTP/1 downgrade it can desynchronize
// the HTTP/1 parser and enable request smuggling. Any occurrence therefore
// fails the affected stream: the gateway should answer with RST_STREAM
// (PROTOCOL_ERROR) instead of forwarding the request.
//
// Pseudo-header fields (names beginning with ":") are recognized and exempt
// from this rule; ordinary custom header fields are retained untouched.
package netsec
