// Package netsec provides dependency-free OCSP response parsing and
// certificate status validation.
//
// The package is intended to be called from certificate path validation code.
// A BasicOCSPResponse may contain more than one SingleResponse for the same
// certificate. Such entries are accepted only when they agree. In particular,
// a response containing both "good" and "revoked" entries for one certificate
// is rejected as a conflict; it never silently selects the "good" entry.
package netsec
