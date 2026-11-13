// Package netsec verifies the selective-disclosure structure of an SD-JWT.
//
// Verify recursively resolves disclosures against every reachable _sd layer.
// A digest may occur only once in the complete document: a digest that is used
// as a decoy in one slot and as a real disclosure in another is rejected. Only
// the sha-256 disclosure algorithm is accepted. Unmatched _sd entries are
// retained in the result and reported separately as undisclosed.
//
// JWT signature verification is intentionally out of scope for this package;
// callers should verify the compact JWT signature before invoking Verify.
package netsec
