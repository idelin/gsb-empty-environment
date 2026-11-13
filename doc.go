// Package netsec implements adaptive interval recovery for an RSA prefix
// oracle.
//
// The oracle answers only whether Dec(c*s^e mod N) lies in [2B, 3B), where
// B = 2^(8*(k-2)), k is the byte length of N, and the unknown plaintext is
// already known to lie in that range.  Candidate plaintexts are kept as exact
// inclusive integer intervals.  One answer can split an interval into several
// pieces; adjacent pieces are merged.  Recovery succeeds only after a single
// integer remains and its re-encryption matches the target ciphertext.
//
// FeedbackUnavailable consumes one query from the budget without changing the
// candidates.  Checkpoints bind the public key, ciphertext, candidate state,
// and every recorded query, and can be resumed later.
package netsec
