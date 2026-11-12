// Package netsec verifies iOS App Attest assertions for request
// authentication.
//
// A Verifier checks the application-bound rpIdHash, the assertion flag byte,
// the monotonically increasing per-key counter, a single-use time-limited
// challenge, the request body binding, and the ECDSA P-256 signature. The
// counter is advanced with an atomic compare-and-swap only after every check
// succeeds, so a failed or replayed assertion can never move it forward.
package netsec
