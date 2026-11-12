// Package taskpkg implements counterexample reduction for a Go
// vulnerability-analysis host investigating an attachment decoder's
// out-of-bounds read.
//
// The host describes the decoder input as a record tree with integer
// length fields and relative references, and supplies two callbacks: an
// independent format validator and a deterministic executor. The executor
// must report the object actually read, the out-of-bounds offset, and the
// allocation site. Reduce searches for the globally smallest candidate -
// fewest nodes, then fewest encoded bytes - that still passes validation
// and reproduces the out-of-bounds read at the same allocation site.
// Ordinary parse failures never count as reproductions. When the budget
// runs out, the result is flagged as not proven minimal. Exhaustive
// provides a brute-force reference used to cross-check reduction results
// and per-step vulnerability evidence.
package taskpkg
