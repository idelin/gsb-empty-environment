// Package steady implements the steady-state amplification solver for a
// multi-hop service call DAG and a matching discrete-event simulation (DES)
// used to validate the solver numerically.
//
// The solver consumes:
//
//   - a DAG topology with per-edge fan-out multipliers and per-edge retry,
//     retry-budget, deadline and hedging configuration;
//   - per-node hard capacity and a monotone load -> failure-rate curve
//     p_fail(load);
//   - ingress offered load per entry node.
//
// It then runs a damped fixed-point iteration over node loads, edge failure
// rates, retry-budget-limited extra attempts and hedging demand, producing
// the effective arrival rate, actual failure rate and amplification factor
// of every node. Nodes whose steady load breaks hard capacity (or whose
// iteration cannot converge) are flagged as avalanche nodes, and a binary
// search reports the maximum safe retry budget ratio of every edge that
// restores a fully converged steady state.
//
// The DES in simulation.go reuses the exact runtime decision components of
// the retry package (EdgeController, RetryBudget, CircuitBreaker, Deadline
// and Backoff) so that the analytical model and the per-request runtime stay
// mutually consistent.
package steady
