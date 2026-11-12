// Package earlyhints implements safe RFC 8297 Early Hints (HTTP 103)
// handling for a Go reverse-proxy cache layer.
//
// A shared cache that stores a 103 response as if it were the final response,
// or that lets attacker-controlled Link headers from a 103 flow into a cache
// entry, can make later users preload attacker-chosen resources. This package
// enforces four invariants:
//
//   - 103 responses are forwarded only to HTTP/1.1 and newer client
//     connections. HTTP/1.0 (and older) clients receive nothing interim.
//   - 103 responses are never written to the cache and cannot mutate an
//     existing cache entry. Only the terminal response is evaluated for
//     caching.
//   - A forwarded 103 contains exactly one Link header. Every other header,
//     including Set-Cookie and Content-Length, is stripped.
//   - Only Link values with rel "preload" or "preconnect", an absolute https
//     target and a host in the caller-supplied allow list survive. At most
//     three 103 responses are forwarded per request.
//
// The low-level Processor is usable inside any proxy that observes upstream
// 1xx responses through net/http/httptrace.ClientTrace. Handler is a
// turnkey http.Handler combining the processor with an injectable Cache.
package earlyhints
