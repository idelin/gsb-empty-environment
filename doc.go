// Package netsec provides differential-trail analysis for a custom
// 16-bit substitution-permutation network (SPN) with 1 to 4 rounds.
//
// Each round XORs a round key at the round start, applies the same
// 16-entry bijective S-box to each of the 4 nibbles, and then permutes
// the 16 bits with a caller-supplied bijection (bit 0 is the least
// significant bit; input bit i moves to position perm[i]). Non-bijective
// S-boxes or bit permutations are rejected. Differences and round keys
// are 16-bit values (0..65535). Round keys do not influence differential
// propagation, so trail search is key-independent.
//
// The package computes the S-box difference distribution table (DDT),
// scores every concrete trail by the exact rational product of its
// active S-box transition counts divided by 16, and searches for the
// maximum-score trail with best-first branch-and-bound. When the
// expansion budget is exhausted the result carries both the best known
// trail and a probability upper bound on everything not yet excluded;
// only when the two have closed may the trail be called optimal.
//
// CountDifferential independently verifies true differential
// probabilities by exhaustive plaintext enumeration for caller-specified
// keys, and Report keeps the single-trail probability strictly separate
// from the combined true probability of all trails.
package netsec
