// Package sdf provides exact bounded-buffer feasibility analysis and periodic
// scheduling for synchronous dataflow (SDF) graphs on a hardware inference
// runtime.
//
// # Runtime model
//
// An actor firing consumes a fixed number of tokens on each incoming edge at
// launch and produces a fixed number on each outgoing edge only at
// completion. Feedback edges may start with preloaded delay tokens. The
// hardware exposes:
//
//   - one shared compute unit, so at most one firing runs at a time;
//   - a finite pool of DMA descriptors, one reserved by every in-flight
//     firing for its whole lifetime (DMA itself is concurrent);
//   - statically sized edge buffers whose output slots are reserved at
//     launch, so an in-flight firing can block a later launch even before its
//     tokens are visible.
//
// External inputs are ultimately periodic run-length streams. Long runs and
// long idle gaps are stored symbolically; time advances arithmetically to the
// next event, never one tick at a time, so idle periods up to 2^45 cost O(1).
//
// # Analysis pipeline
//
// Solve runs four exact stages:
//
//  1. Rate consistency. A primitive positive integer repetition vector q is
//     derived per weakly connected component by rational ratio propagation and
//     checked against every edge equation. Failure means no finite buffer can
//     sustain unbounded execution (StatusInconsistent).
//  2. Structural lower bounds. Each edge is analyzed in isolation over its
//     finite (backlog, reservation) state space, reproducing the classical
//     p+c-gcd(p,c) minimum for zero-delay edges.
//  3. Exact liveness. Over the finite token-backlog state space (each edge
//     holds at most MaxCap tokens), exhaustive reachability decides whether
//     every reached state still has a legal firing. A reachable state with no
//     outgoing transition is a genuine deadlock (e.g. a feedback edge whose
//     initial delay cannot seed the first firing); otherwise finiteness
//     guarantees a reachable cycle and hence an infinite firing sequence.
//  4. Timed periodicity and windowing. A deterministic self-timed event-driven
//     simulation records normalized state snapshots only at quiescent input
//     period boundaries. A repeated snapshot is a constructive proof of an
//     eventually periodic schedule; stability is never inferred from a fixed
//     number of simulated rounds. The sliding-window constraint is checked
//     with closed-form periodic output counts, sampling each input phase once
//     and extrapolating all further periods arithmetically.
//
// Capacity vectors are enumerated in increasing total capacity with edge-id
// lexicographic tie-break, so the first feasible vector is the unique required
// optimum. Certificate carries independently recomputable structural lower
// bounds plus a one-step reduction witness for every edge above its bound.
//
// When an exact search reaches its safety envelope without a repeat, the
// package reports StatusInconclusive rather than asserting stability.
package sdf
