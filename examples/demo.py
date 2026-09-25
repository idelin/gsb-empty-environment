"""End-to-end demo of the markov package (pure standard library).

Run from the repository root:  python3.11 examples/demo.py
"""

import math
import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from markov import Convention, MarkovChain, SparseTransitionMatrix


def banner(title):
    print("\n" + "=" * 68)
    print(title)
    print("=" * 68)


def gamblers_ruin(n, p=0.5):
    lines = {0: {0: 1.0}, n: {n: 1.0}}
    for i in range(1, n):
        lines[i] = {i - 1: 1.0 - p, i + 1: p}
    return SparseTransitionMatrix(n + 1, lines)


# ----------------------------------------------------------------------
banner("1. Small chain: gambler's ruin (dense vs exact rational reference)")
chain = MarkovChain(gamblers_ruin(4))
print("diagnostics:", chain.diagnostics())
dense = chain.absorption(method="dense")
exact = chain.absorption(method="exact")
for s in (1, 2, 3):
    print(f"  state {s}: P(absorbed at 4) dense={dense.probabilities[s][1]:.6f} "
          f"exact={exact.probabilities[s][1]}  "
          f"E[steps] dense={dense.expected_steps[s]:.6f} "
          f"exact={exact.expected_steps[s]}")

# ----------------------------------------------------------------------
banner("2. Non-absorbing closed class reachable -> infinite expectation")
m = SparseTransitionMatrix(5, {
    0: {0: 1.0}, 1: {1: 1.0},
    2: {3: 1.0}, 3: {2: 1.0},      # closed period-2 class, never absorbing
    4: {0: 0.5, 2: 0.5},
})
res = MarkovChain(m).absorption(method="dense")
print("closed classes:", [cc.states for cc in res.closed_classes])
print("P(end in each closed class | start 4):", res.probabilities[4])
print("E[steps to absorption | start 4]:", res.expected_steps[4])
print("non-absorbing closed classes:", res.diagnostics["non_absorbing_closed_classes"])

# ----------------------------------------------------------------------
banner("3. Mass conservation while propagating a distribution")
chain = MarkovChain(gamblers_ruin(6))
for rec in chain.propagate([0, 0, 0, 1.0, 0, 0, 0], 400):
    if rec.step % 100 == 0:
        absorbed = rec.distribution[0] + rec.distribution[6]
        print(f"  step {rec.step:4d}: mass error={rec.mass_error:+.2e}  "
              f"mass at absorbing states={absorbed:.6f}")

# ----------------------------------------------------------------------
banner("4. Large sparse model (n=300): iterative SOR with true residuals")
n = 300
chain = MarkovChain(gamblers_ruin(n))
omega = 2.0 / (1.0 + math.sin(math.pi / n))
res = chain.absorption(method="iterative", tol=1e-10, omega=omega,
                       max_iter=300000)
mid = n // 2
print(f"  P(hit {n} | start {mid}) = {res.probabilities[mid][1]:.8f} "
      f"(closed form 0.5)")
print(f"  E[steps | start {mid}] = {res.expected_steps[mid]:.2f} "
      f"(closed form {mid * (n - mid)})")
for name, rhs in res.diagnostics["rhs"].items():
    print(f"  rhs {name}: iterations={rhs['iterations']} "
          f"residual={rhs['residual_norm']:.2e} "
          f"last increment={rhs['increment_norm']:.2e} "
          f"converged={rhs['converged']}")

# ----------------------------------------------------------------------
banner("5. Row revision with factorization reuse")
lines = {0: {0: 1.0}, 4: {4: 1.0},
         1: {0: 0.4, 2: 0.6}, 2: {1: 0.4, 3: 0.6}, 3: {2: 0.4, 4: 0.6}}
chain = MarkovChain(SparseTransitionMatrix(5, lines))
before = chain.absorption(method="dense")
print("  before revision: reused_factorization =",
      before.diagnostics["reused_factorization"])
chain.revise_line(2, {1: 0.5, 3: 0.5})
after = chain.absorption(method="dense")
print("  after revision:  reused_factorization =",
      after.diagnostics["reused_factorization"])
print("  P(hit 4 | start 2): before={:.6f} after={:.6f}".format(
    before.probabilities[2][1], after.probabilities[2][1]))

# ----------------------------------------------------------------------
banner("6. Row vs column convention give identical dynamics")
lines = {0: {0: 1.0}, 1: {0: 0.5, 2: 0.5}, 2: {1: 0.25, 2: 0.75}}
row_chain = MarkovChain(SparseTransitionMatrix(3, lines, Convention.ROW))
col_chain = MarkovChain(SparseTransitionMatrix(3, lines, Convention.COLUMN))
d0 = [0.1, 0.3, 0.6]
print("  row-convention step:", row_chain.apply(d0))
print("  col-convention step:", col_chain.apply(d0))

# ----------------------------------------------------------------------
banner("7. Stationary iteration: residual, not increment, proves convergence")
n = 5
ring = SparseTransitionMatrix(
    n, {i: {(i + 1) % n: 0.6, (i - 1) % n: 0.4} for i in range(n)})
result = MarkovChain(ring).stationary_iterative([1.0, 0, 0, 0, 0], tol=1e-12)
print(f"  converged={result.converged} after {result.iterations} iterations; "
      f"residual={result.residual_norm:.2e}")
print("  stationary distribution:",
      [round(v, 6) for v in result.distribution])
