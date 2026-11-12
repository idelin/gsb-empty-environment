// Package netsec implements an append-only signed timestamp chain for
// forensic evidence.
//
// A Chain binds the SHA-256 digest of the original evidence and its original
// collection time. Re-signing never replaces either value: Append only adds a
// generation carrying the parent digest, the issuance time, the issuer
// certificate, and a signature over those fields.
//
// Verification checks the evidence digest, every parent link, every issuance
// time (including that it does not move backwards), every issuer signature, and
// the issuer certificate's validity window at the generation's own issuance
// time. All failures are returned together so a caller can retain and inspect
// the complete failed chain.
package netsec
