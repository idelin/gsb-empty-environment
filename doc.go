// Package netsec maps verified X.509 client certificates to tenant
// identifiers for a Go gateway.
//
// A certificate is accepted only if its DER SHA-256 fingerprint is explicitly
// registered. Mapping then prefers unique subject identifiers and subjectAltName
// values. CommonName is used only as a fallback when it is registered to
// exactly one tenant. Before any identity is returned, the package verifies
// the issuer path against explicit trust anchors, required policy OIDs, and
// revocation status.
//
// Registry mutations publish immutable generations atomically, so a request
// resolves against one consistent mapping generation even while certificates
// are rotated.
package netsec
