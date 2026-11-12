// Package netsec contains authorization policy for the DeltaV (RFC 3253)
// UPDATE, LABEL, and CHECKIN methods.
//
// The package owns the version-history metadata needed to make those decisions
// atomically: version-to-history mappings, per-version read grants,
// per-history write grants, labels, and checkout ownership. Handlers should
// call its methods immediately before performing the corresponding protocol
// operation and translate the returned sentinel errors to HTTP status codes.
package netsec
