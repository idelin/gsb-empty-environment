// Package netsec provides strict JWS parsing and signature verification.
//
// Security-sensitive header parameters are read exclusively from the JWS
// protected header. The unprotected header is never used to select an
// algorithm or a key. If the protected and unprotected headers contain the
// same parameter with different values, verification fails.
package netsec
