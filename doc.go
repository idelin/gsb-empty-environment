// Package netsec provides cache-key and forwarded-header policy for an HTTP
// cache sitting in front of an origin server.
//
// The policy is intentionally conservative: a forwarding header is only sent
// to the origin when its value is also part of the cache key. The scheme
// forwarding header (X-Forwarded-Proto by default) is keyed by default, so a
// response produced for an http request cannot be reused for an https request.
//
// Headers named by an origin response's Vary header are always added to the
// key, including forwarding headers. A Vary value of "*" is reported as
// non-cacheable rather than being silently ignored.
package netsec
