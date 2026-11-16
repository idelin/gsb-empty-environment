// Package netsec implements a security-focused Alt-Svc cache for Go HTTP
// clients.
//
// Advertisements are only accepted from HTTPS responses. Alternative services
// are keyed by their origin, and an alternative endpoint must always be
// authenticated against the original origin's hostname. The package provides
// TLS helpers that set the verification name to the origin rather than to the
// alternative host.
package netsec
