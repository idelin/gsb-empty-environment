// Package netsec verifies COSE_Sign1 messages under a strict header policy:
//
//   - the alg parameter is read exclusively from the protected header; an
//     alg carried only in the unprotected header is never consulted and
//     causes rejection;
//   - the protected header must be definite-length canonical (deterministic)
//     CBOR whose canonical re-encoding is byte-identical to the received
//     bytes;
//   - only signatures whose protected header contains alg and is byte-stable
//     under re-encoding are accepted.
//
// Verification components call VerifySign1 with the encoded message, the
// signer's public key, and any external AAD:
//
//	payload, err := netsec.VerifySign1(msg, pubKey, nil)
//
// Supported algorithms are EdDSA (-8, ed25519.PublicKey) and ES256 (-7,
// *ecdsa.PublicKey on P-256).
package netsec
