// Package netsec provides outbound HTTP authorization for a wasi:http
// outgoing-handler implementation embedded in a Go host.
//
// A component may only call out to scheme+authority pairs that the host
// registered up front (see New and Entry). Every request is then funneled
// through Authorizer.Check, which:
//
//   - matches scheme and authority case-insensitively, filling in the
//     scheme default port (80 for http, 443 for https) and requiring
//     bracketed IPv6 literals;
//   - resolves the allowed host exactly once and refuses loopback,
//     link-local, private, unspecified addresses and the cloud metadata
//     endpoint 169.254.169.254;
//   - pins the connection to the validated addresses (Decision.DialContext
//     and Decision.Transport never re-resolve the name, preventing DNS
//     rebinding between the check and the dial);
//   - requires the Host header to match the authority and rejects
//     Connection, Transfer-Encoding and Upgrade headers;
//   - supplies an http.Client (Decision.Client) that never follows
//     redirects.
package netsec
