// Package hostresolve resolves the authoritative host of an HTTP/1.1 request
// for use as a single, shared routing and cache key.
//
// The rules implement RFC 9110 section 7.2 and RFC 9112 section 3.2.2:
//
//   - A request with an absolute-form request target is routed by the
//     authority embedded in the request target. The Host header is never used
//     as the authority in that case (it is still structurally validated and a
//     malformed Host header is rejected).
//   - Requests with origin-form request targets are routed by the Host
//     header.
//   - A missing, duplicated, empty, userinfo-bearing, or path-bearing Host
//     header is rejected with a 400-class error.
//   - X-Forwarded-Host overrides the resolved authority only when the direct
//     peer is a trusted proxy; headers arriving from untrusted peers are
//     ignored.
//
// Because routing, caching and authentication must agree on one authority,
// callers must derive every host-based key from [Result.Key] instead of
// reading req.Host or a forwarding header independently.
package hostresolve
