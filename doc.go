// Package netsec provides certificate parsing and verification helpers that
// reject elliptic-curve public keys encoded with explicit curve parameters.
//
// Only named curves (selected by an OBJECT IDENTIFIER in the
// SubjectPublicKeyInfo AlgorithmIdentifier) are accepted. Explicit ECParameters
// are rejected even when they describe a standard curve or use a public point
// whose size matches P-256.
package netsec
