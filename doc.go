// Package policytree solves finite-horizon control decision problems with
// probes (tests) under a minimax-loss objective.
//
// A problem has up to 16 possible worlds and up to 8 terminal actions.
// Each action yields, per world, an integer loss and an observable symbol.
// Before acting, the decision maker may perform at most two probes; each
// probe has an integer cost and reveals a world-dependent observation
// symbol. Probes may be chosen adaptively based on earlier observations,
// but the policy must be non-clairvoyant: two worlds that produce the same
// observation history must lead to the same action.
//
// The solver minimizes the worst-case total loss (probe costs plus action
// loss) over all worlds; ties are broken by the expected total loss under
// rational world probabilities. It returns an explicit policy tree together
// with a per-world replay. For small instances, BruteForce enumerates all
// feasible policies to cross-check the dynamic-programming solver and the
// non-clairvoyance constraint.
package policy
