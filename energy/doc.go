// Package energy implements coverage-rarity-driven energy allocation for a
// security fuzzing host.
//
// The scheduler keeps, for every corpus sample, its set of covered features
// (for example AFL-style edge identifiers) and its number of valid executions.
// Every feature carries a running frequency equal to the sum of the valid
// execution counts of the samples that currently cover it. Samples can be
// added and removed, so when a sample is evicted every feature frequency is
// reduced immediately: the scheduler never keeps stale frequency from dead
// corpus entries and therefore cannot starve the paths that are genuinely rare
// after pruning.
//
// At each round a fixed integer energy budget is distributed. The raw weight
// of a sample is the reciprocal of the smallest frequency among its covered
// features (the inverse frequency of its rarest coverage); samples with no
// coverage or no valid executions have weight zero and receive no energy. The
// integer split is computed with the largest-remainder method using exact
// rational arithmetic, which keeps the total budget conserved and makes the
// result deterministic. When two samples are tied at every comparison step the
// one with the lexicographically smaller sample ID wins.
//
// Allocator updates its frequencies incrementally and is safe for concurrent
// use. RecomputeAllocate is a pure full-recomputation oracle over an explicit
// sample slice, intended for tests and hosts that want to cross-check the
// incremental state. Batched mutations are available through Tx / Merge: a
// batch whose validation fails (for example because it contains a failed
// sample that does not exist) is rolled back atomically and leaves the
// allocator untouched.
package energy
