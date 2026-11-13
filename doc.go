// Package netsec validates OID4VCI credential proofs with strict audience
// binding and single-use c_nonce replay protection.
//
// A Verifier issues the current c_nonce through IssueNonce. ValidateProof
// accepts a proof only when its JWS signature verifies, every "aud" value equals
// the credential endpoint, and "nonce" is the current, unused c_nonce for the
// selected scope. The nonce is consumed atomically, so exactly one concurrent
// request can succeed. Invalid signatures, wrong audiences and wrong nonces do
// not consume the legitimate current c_nonce.
//
// MemoryNonceStore is suitable for one issuer process. A multi-instance issuer
// can implement NonceStore on top of a database, provided Consume performs the
// current-value check and used-marking in one atomic operation.
package netsec
