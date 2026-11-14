// Package netsec provides an importable TLS keying-material exporter facade
// for reverse proxies that multiplex several virtual hosts over shared edge
// connections.
//
// The proxy feeds the facade an immutable handshake Digest, bumps the
// connection generation on 0-RTT, HelloRetryRequest, ECH rejection, session
// resumption and certificate hot reloads, and freezes the final certificate,
// SNI routing and ALPN decisions with Freeze. Exporting is only possible
// while frozen, and every exported value is bound to the frozen Identity.
// The cache key covers the final virtual host, the handshake digest, the raw
// label bytes and the context's presence and length, distinguishing nil from
// empty contexts. Labels with the reserved EXPORTER prefix are rejected and
// output length is capped at MaxExportLength.
package netsec
