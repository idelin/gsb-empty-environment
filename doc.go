// Package netsec provides strict SAML NameID extraction and comment-excluding
// XML canonicalization.
//
// ParseNameID and ReadNameID reject ambiguous NameID values containing XML
// comments, CDATA sections, or extra markup. Such values can make a naive
// parser read only the first text node and accept a truncated identity.
//
// Canonicalize, CanonicalDigest, and Digest implement Canonical XML without
// comments. They are useful for reproducing SAML signature digests and for
// demonstrating that comment edits do not alter such a digest.
package netsec
