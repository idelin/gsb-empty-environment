// Package netsec checks linearizability of verification-code challenge
// histories produced by an authentication service.
//
// A history records one linearization interval [Invoke, Return] per
// operation. The five tracked operations are Create, Replace, Answer,
// Consume and Grant. Check decides whether the observed results can be
// explained by some total order of the operations whose linearization points
// lie inside their intervals and that is accepted by the sequential
// reference specification:
//
//   - exactly one challenge is current at any time and it can authorize at
//     most once through Grant;
//   - Replace atomically retires the current challenge and installs a new
//     one; replacing a stale challenge reports ErrExpired;
//   - Answer never spends the challenge but wrong answers count against a
//     fixed budget, after which the challenge is locked;
//   - Consume and a successful Grant both spend a challenge, so later
//     authorization reports ErrConsumed.
//
// When a history is not linearizable, Check returns a minimized
// counterexample with a dead-end prefix. EnumerateShortHistories exhaustively
// cross-checks bounded scenarios centered on replacement versus stale-answer
// races, including deliberately buggy result mutations.
package netsec
