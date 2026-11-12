// Package taskpkg verifies that two HTTP/1.x response parsers agree on where
// every response body ends.
//
// The package is intended for proxy test hosts that replay a queue of request
// methods and a raw, already-encoded response stream. ParseStrict implements
// the boundaries required by the task:
//
//   - 1xx responses never have a body and never consume a request.
//   - HEAD responses and 204 responses never have a body.
//   - Every other response is delimited by exactly one decimal
//     Content-Length value, or by connection close when no length is present.
//   - Chunked transfer coding, repeated Content-Length values, conflicting
//     framing, and premature connection close are rejected.
//
// Verify compares ParseStrict with a caller supplied alternate strategy. Every
// response is reduced to its owning request and its half-open byte interval.
// The first byte at which the two ownership/boundary maps disagree is reported
// together with the complete parse evidence from both sides. Any cross-request
// ownership difference blocks the whole connection.
package taskpkg
