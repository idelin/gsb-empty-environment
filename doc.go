// Package netsec computes candidate trajectory sets for privacy test hosts.
//
// Point-wise generalization alone can be defeated by motion continuity: a
// cell published as allowed at time t is only a real position candidate when
// some complete feasible trajectory passes through it. The solver derives those
// support positions from the full constraint set:
//
//   - per-time allowed grid cells (Problem.Allowed),
//   - a directed road reachability graph (Problem.Graph), with an implicit
//     zero-length stay move at every cell,
//   - a per-step speed cap (Problem.MaxSpeed) and a whole-trajectory bound on
//     consecutive speed changes (Problem.MaxSpeedChange),
//   - a closed interval on the anonymously published total travelled length
//     (Problem.TotalLength).
//
// Each explicit state carries the cell, the previous speed and the accumulated
// distance. Forward reachability and backward feasibility are combined so a
// state survives only when a complete feasible trajectory exists through it.
// Time ranges whose support is a single cell are the uniquely exposed segments.
//
// Work is budgeted: Solver.Advance consumes a bounded number of state units and
// returns the not-yet-finalized time ranges, letting the host resume later.
// EnumerateTrajectories exhaustively lists all feasible trajectories on small
// grids and is intended to cross-check the dynamic-programming result; the
// local number of allowed candidates must never be treated as the anonymity
// degree.
package netsec
