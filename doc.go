// Package netsec provides cryptographic helpers for handshake key transport.
//
// DecryptKeyTransport implements RSA PKCS #1 v1.5 decryption with implicit
// rejection: padding and length failures never surface as distinguishable
// errors, so the handshake cannot be turned into a Bleichenbacher oracle.
package netsec
