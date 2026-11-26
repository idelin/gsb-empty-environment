// Package h2h1 provides raw HTTP/2 frame and RFC 7541 HPACK decoding plus a
// fail-closed HTTP/2-to-HTTP/1.1 downgrade boundary. It intentionally does not
// use the standard library or golang.org/x/net HTTP/2 or HPACK packages.
//
// The high-level entry point is ParseH2Requests. A returned Request has passed
// frame continuity, HPACK syntax, request pseudo-header and connection-header
// rules, Content-Length consistency, and DATA-length checks. RenderH1Request
// converts only such validated requests into a canonical HTTP/1.1 byte stream;
// ParseH1Request is the corresponding strict backend parser.
//
// GenerateAdversarialCases and VerifyDefense provide an executable security
// regression suite for H2.CL, H2.TE, CRLF/NUL injection, CONTINUATION
// smearing, HPACK state attacks, and normal multiplexed traffic.
package h2h1
