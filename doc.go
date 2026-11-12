// Package netsec canonicalizes HTTP request paths and resolves them against
// Ingress-style Exact and Prefix rules. The canonical path returned by a
// match is the path that must be forwarded upstream.
package netsec
