"""End-to-end demo: exact analysis, simulation, lumping, row revision."""

import random
from fractions import Fraction

from markov import (Chain, absorption_probabilities, expected_hitting_times,
                    conditional_expected_time, absorption_residuals,
                    hitting_time_residuals, probability_sum_witness,
                    check_strong_lumpability, lumped_chain,
                    estimate_absorption, revise_row)

F = Fraction

# States: 0,1 transient; 2 absorbing (target); 3 absorbing; 4 self-loop-1 trap.
chain = Chain([
    [F(1, 2), F(1, 4), F(1, 8), F(1, 8), F(0)],
    [F(1, 3), F(1, 3), F(1, 6), F(0),    F(1, 6)],
    [F(0),    F(0),    F(1),    F(0),    F(0)],
    [F(0),    F(0),    F(0),    F(1),    F(0)],
    [F(0),    F(0),    F(0),    F(0),    F(1)],
])

print("== structure (exact) ==")
print("closed classes:", sorted(sorted(c) for c in chain.closed_classes()))
print("absorbing states:", chain.absorbing_states())
print("transient states:", chain.transient_states())

print("\n== absorption probabilities into target {2} (exact) ==")
probs = absorption_probabilities(chain, {2})
for s, p in sorted(probs.items()):
    print(f"  P(hit 2 | start {s}) = {p}")
parts, total = probability_sum_witness(chain, 0)
print("probability sum over closed classes from 0:",
      {tuple(sorted(k)): str(v) for k, v in parts.items()}, "total =", total,
      "(must be <= 1; exactly 1 here)")

print("\n== expected steps to absorption (exact) ==")
times = expected_hitting_times(chain)
for s, t in sorted(times.items()):
    print(f"  E[T | start {s}] = {t}")

print("\n== expected steps to hit target {2} only (exact) ==")
times2 = expected_hitting_times(chain, targets={2})
for s, t in sorted(times2.items()):
    note = "  <- never absorbed (self-loop-1 trap)" if s == 4 else ""
    print(f"  E[T_2 | start {s}] = {t}{note}")

print("\n== conditional E[T | absorbed in {2}] (exact) ==")
cond = conditional_expected_time(chain, {2})
for s, t in sorted(cond.items()):
    label = "undefined (hit probability 0)" if t is None else str(t)
    print(f"  start {s}: {label}")

print("\n== residuals of the linear equations (exact, must be 0) ==")
print("  absorption:", absorption_residuals(chain, {2}, probs))
print("  hitting time:", hitting_time_residuals(chain, {2, 3, 4}, times))

print("\n== simulation (empirical, seed recorded) ==")
SEED, N = 42, 20000
rng = random.Random(SEED)  # random source injected by the caller
est = estimate_absorption(chain, 0, rng, N, seed=SEED)
print(f"  kind={est.kind} seed={est.seed} n_samples={est.n_samples}")
print("  frequencies:", est.absorption_frequencies)
print("  mean steps:", round(est.mean_steps, 4))
print("  exact for comparison: P(hit 2) =", float(probs[0]),
      " E[T] =", float(times[0]))

print("\n== partial observation / lumping ==")
bad_groups = [{0, 1}, {2}, {3}, {4}]
ok, witness = check_strong_lumpability(chain, bad_groups)
print("  groups", bad_groups, "lumpable?", ok)
if not ok:
    print("  exact witness:", witness)
good_groups = [{0}, {1}, {2}, {3, 4}]
ok, _ = check_strong_lumpability(chain, good_groups)
print("  groups", good_groups, "lumpable?", ok)
if ok:
    lc = lumped_chain(chain, good_groups)
    print("  lumped matrix:")
    for r in lc.matrix:
        print("   ", [str(x) for x in r])

print("\n== row revision (incremental vs full re-solve) ==")
delta = [F(0), F(-1, 8), F(1, 8), F(0), F(0)]  # shift 1/8 from state 1 to 2
rep = revise_row(chain, 0, delta)
print(f"  row {rep.row}: old sum={rep.old_row_sum}, delta sum={rep.delta_sum}, "
      f"new sum={rep.new_row_sum}")
print(f"  probability bounds: [{rep.min_entry}, {rep.max_entry}] "
      f"ok={rep.bounds_ok}")
print("  affected states:", sorted(rep.affected_states))
print("  affected closed classes:", [sorted(c) for c in rep.affected_closed_classes])
for cls in rep.affected_closed_classes:
    print(f"  P(hit {sorted(cls)}) incremental:",
          {s: str(rep.incremental_probs[cls][s]) for s in sorted(rep.affected_states)})
print("  max |incremental - full| over all states:", rep.max_abs_diff)
for cls, res in rep.residuals.items():
    print(f"  residuals for class {sorted(cls)}:",
          {s: str(r) for s, r in sorted(res.items())})
