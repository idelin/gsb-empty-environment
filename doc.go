// Package netsec verifies that a hardened compiler rewrite preserves the
// signed-integer error behavior of an earlier SSA graph.
//
// A hardening pass may replace 16/32-bit signed arithmetic with wider
// operations and reconstruct overflow checks afterward. That transformation
// is only sound if, for every one of the up to MaxInputs input bytes:
//
//   - when the old graph explicitly reports an overflow error at source node
//     N, the new graph reports the same source node N; and
//   - when the old graph returns normally, the new graph returns the equal
//     integer.
//
// The package provides three independent mechanisms:
//
//   - Graph/Block/Value define an acyclic SSA IR covering signed add/sub/mul,
//     truncation, sign/zero extension, comparisons and conditional branches.
//   - Interpret is an independent concrete interpreter used as ground truth.
//   - Verify performs symbolic search with path conditions: each graph is
//     bit-blasted into a boolean DAG, terminal outcomes are guarded by path
//     predicates, and a bounded SAT search looks for an input on which the
//     two outcomes disagree. Exhausting the configured budget returns
//     StatusUnknown rather than an unsound definitive verdict.
//
// For small 8-bit graphs, EnumerateAll runs the interpreter over the entire
// input space so the symbolic results can be cross-checked by complete
// enumeration.
package netsec
