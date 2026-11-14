// Package netsec parses and verifies RFC 3161 timestamp tokens and binds their
// message imprint to a code-signing signature.
//
// A Verifier checks the CMS signature and TSA certificate chain, enforces the
// issuer allow-list and signer-certificate validity window, detects serial
// replay, and caches successful bindings by imprint plus TSA identity. Callers
// must supply trust anchors; ParseTimestampToken alone does not evaluate trust.
package netsec
