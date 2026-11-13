// Package netsec provides a strict, importable MIME multipart decoder.
//
// The decoder follows RFC 2046 section 5.1. The boundary parameter of a
// multipart Content-Type value may be quoted; the optional double quotes are
// removed by Boundary before delimiter matching, so a quoted header boundary
// and unquoted body delimiters interoperate. Boundary values must satisfy the
// bcharsnospace grammar (no spaces or tabs, at most 70 characters), and the
// transport padding on a boundary delimiter line may contain only spaces and
// tabs immediately before the terminating CRLF.
//
// Use NewReader for streaming access or DecodeMultipart to decode a complete
// upload payload into buffered parts.
package netsec
