// Package absexec is a small abstract-execution engine for proving whether a
// fixed-size output buffer is fully initialized before every send point.
//
// The input is a finite control-flow graph whose operations are:
//
//   - fixed-interval writes ([lo, hi) is definitely written);
//   - dynamic writes whose concrete [lo, hi) is only known through the
//     input-given bounds lo in [LoMin, LoMax], hi in [HiMin, HiMax]; only the
//     guaranteed portion [LoMax, HiMin) counts as written;
//   - conditional branches (both successors are considered feasible);
//   - bounded loops expressed as back edges with a per-edge iteration cap,
//     so zero-iteration paths are always retained;
//   - send points that inspect a required interval of the buffer.
//
// The analysis computes, at every send point, the interval written on ALL
// reaching paths (the must-written set): bytes that are only written on some
// paths are never treated as necessarily written. Each byte missing from the
// must-written set is reported together with a concrete counterexample path
// that reaches the send without writing it. If the configured visit budget is
// exhausted before the fixed point is reached, unproven send points are
// reported as unknown instead of making unsound claims.
package absexec
