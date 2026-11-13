// Package netsec audits whether two P-256 ECDSA signatures are consistent
// with nonces related by k2 = a*k1 + b (mod n).
//
// The package is deliberately an offline analysis tool. It validates all
// scalar ranges and verifies both signatures before attempting recovery. A
// report with no candidates means only that the supplied affine relation was
// not observed; it is not a statement that the signer's nonces are safe.
package netsec
