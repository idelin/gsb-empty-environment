// Package netsec implements X.509 name-constraint handling for the
// registeredID GeneralName form, including certificate-path checks.
//
// RFC 5280 defines registeredID as GeneralName choice [8], whose value is an
// OBJECT IDENTIFIER. Go's crypto/x509 does not expose registeredID SANs or
// registeredID name constraints, so this package reads the raw
// subjectAltName (2.5.29.17) and nameConstraints (2.5.29.30) extensions.
//
// Matching under PolicyStrict is type-aware: a registeredID constraint can
// only be matched by a registeredID SAN whose OID is exactly equal. DNS,
// rfc822Name (email), and uniformResourceIdentifier values never match
// registeredID constraints.
//
// PolicyLegacy, the package default, preserves the compatibility behaviour
// requested by this module: a dNSName whose complete value is a dotted-
// decimal OID is treated as a registeredID with that OID. For example, the
// DNS SAN "1.2.3.4" satisfies a permitted registeredID constraint 1.2.3.4.
// This is deliberately narrower than a general string comparison; no other
// name form participates.
package netsec
