// Package netsec provides a WebSocket per-message-deflate receive component
// (RFC 7692) for embedding in a WebSocket message host.
//
// The Receiver reassembles fragmented data messages, keeps interleaved
// control frames out of the decompression dictionary, honors the negotiated
// context-takeover rule, and enforces a single shared byte budget for
// retained compressed input and produced output.
package netsec
