// Package netsec models a 16-set, 2-way true-LRU cache and exhaustively
// searches Prime+Probe experiments for secret-dependent observations.
//
// Every experiment starts from an empty cache, performs the attacker's prime
// sequence, performs up to 16 victim loads, and finally performs the attacker's
// probe sequence. Victim loads and probes both update the replacement state.
// Probe results are reduced to hit/miss bits.
//
// Search reports an indistinguishability verdict only after enumerating all
// secrets 0..15 and the supplied public values. Partition helpers return
// observation-equivalence classes and never equate "different trace" with
// "unique secret".
package netsec
