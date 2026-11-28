// Package antismuggle is a raw byte-level HTTP/1.1 (RFC 9112) persistent
// stream framer with strict request-smuggling defence, intended to sit in
// front of heterogeneous backends in a reverse proxy or WAF.
//
// It never calls net/http.ReadRequest: every byte of every frame is parsed
// here so that ambiguous or obfuscated inputs cannot be interpreted
// differently by two implementations.
//
// Hard guarantees
//
//   - Exact CRLF line termination only. Bare LF and bare CR poison the
//     connection immediately, as do obs-fold header continuations.
//   - Header field names must be RFC 9110 tokens: no whitespace, control
//     characters or whitespace before ':'.
//   - Content-Length and Transfer-Encoding can never coexist. The request is
//     rejected with 400 and the connection force-closed; one header is never
//     silently dropped to "repair" the request.
//   - Multiple Content-Length fields are rejected by default even when equal.
//     A single value must be pure ASCII decimal digits with no sign, no
//     whitespace and no overflow.
//   - The only accepted Transfer-Encoding is the single exact token
//     "chunked" (case-insensitive acceptance is opt-in). Lists, unknown
//     codings and non-last chunked are rejected, as is "Connection:
//     Transfer-Encoding" (TE.TE).
//   - Chunked decoding validates hex chunk sizes, enforces the exact CRLF
//     after every chunk, bounds chunk extensions and rejects control bytes in
//     them, and only accepts allow-listed, never forbidden, trailers.
//
// Every protocol violation returns an *Error whose CloseConnection is always
// true and whose StatusCode is 400. After such an error the Conn is poisoned
// and cannot be reused, which makes closing the TCP connection the only safe
// option.
package antismuggle
