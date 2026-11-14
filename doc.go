// Package netsec provides client identity resolution and multi-dimensional
// rate limiting for Go edge services.
//
// The package deliberately treats every forwarded address header as untrusted
// until the immediate peer is known to be a trusted proxy. When that is true,
// the forwarding chain is validated and the rightmost non-trusted hop becomes
// the client address. Headers supplied directly by clients are removed at
// ingress.
//
// A resolved Identity can then be passed to Limiter, which enforces separate
// address, account and device limits for each action.
package netsec
