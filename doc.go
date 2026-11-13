// Package netsec verifies SD-JWT key-binding JWTs (kb-jwt).
//
// A key-binding JWT proves that the holder presenting an SD-JWT controls
// the key referenced by the issuer JWT's cnf.jkt claim. Verification
// enforces all of the following:
//
//   - the kb-jwt "typ" header is exactly "kb+jwt";
//   - the kb-jwt "sd_hash" claim equals the base64url-encoded SHA-256 of
//     the canonical disclosure bytes (issuer JWT and disclosures, each
//     terminated by "~"), so a proof cannot be replayed against an added
//     or removed disclosure;
//   - the issuer JWT's cnf.jkt equals the RFC 7638 JWK thumbprint of the
//     proof key;
//   - the kb-jwt carries a valid EdDSA signature from the proof key.
//
// Issuer-JWT signature verification is out of scope; callers are expected
// to validate the issuer signature through their own trust chain.
package netsec
