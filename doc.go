// Package netsec implements a small, application-specific delegation
// certificate format and verifier. It does not implement X.509.
//
// Certificates are signed with Ed25519. The signed message is deterministic,
// domain-separated, and length-prefixes every field. Set-valued fields are
// canonicalized before signing: operations and resource prefixes are sorted
// and deduplicated, and resource paths must be absolute and canonical except
// for an optional trailing slash.
//
// Verification searches for one acyclic chain beginning at a host-fixed trust
// root. At every edge, the child certificate's own constraints are intersected
// with the constraints accumulated along that exact chain. Authorization is
// never assembled from sibling certificates. Subject public keys may not repeat
// within a chain, including across differently signed certificates that use the
// same key.
//
// MaxDelegationDepth is decremented on every delegation. A child's effective
// remaining depth is min(parent's remaining depth - 1, child's declared
// maximum). Every certificate in the selected chain must satisfy the half-open
// validity interval [NotBefore, NotAfter) at the verification instant.
//
// Revocations match the exact (IssuerID, Serial) pair. If Request specifies a
// subject public key, the returned chain must terminate at a certificate for
// that key. The returned chain excludes the trusted root because roots are host
// configuration rather than input certificates.
package netsec
