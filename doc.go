// Package netsec verifies mdoc (ISO/IEC 18013-5) device responses for
// wallets: it checks the MSO (Mobile Security Object) COSE_Sign1 signature
// and, crucially, that the digest of every disclosed value equals the
// corresponding entry in the MSO valueDigests. Verifying only the MSO
// signature is not sufficient, because an attacker can keep the signed MSO
// and replace a disclosed value.
package netsec
