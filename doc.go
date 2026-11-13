// Package netsec verifies DKIM signatures for mail-reading callers.
//
// Verification is safe by default: a DKIM signature carrying l= is rejected
// unless Options.AllowBodyLengthTruncation is set. When truncation is
// allowed, Result reports the exact canonicalized body prefix covered by the
// signature and exposes both the canonicalized and raw uncovered suffixes.
// A signature without l= is verified against the entire canonicalized body.
package netsec
