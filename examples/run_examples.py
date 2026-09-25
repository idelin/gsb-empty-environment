"""Runnable examples for the markov package (Python 3.11+, stdlib only).

Run from the repository root:  python3 examples/run_examples.py
"""

import os
import sys
from fractions import Fraction

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from markov import MarkovChain, SparseTransitionMatrix, ValidationError

H = Fraction(1, 2)


def banner(title):
    print("\n" + "=" * 68)
    print(title)
    print("=" * 68)


def example_exact_reference():
    banner("1. Exact rational reference: gambler's ruin on states 0..4")
    rows = {0: {0: 1}, 4: {4: 1}}
    for i in (1, 2, 3):
        rows[i] = {i - 1: H, i + 1: H}
    chain = MarkovChain.from_rows(5, rows)
    report = chain.absorption_analysis(method="exact")
    print("closed classes:", report.closed_classes,
          " absorbing:", report.absorbing)
    print("transient states:", report.transient)
    for i in report.transient:
        print("  start %d: P(ruin at 0)=%s  P(reach 4)=%s  E[steps]=%s"
              % (i, report.probabilities[i][0], report.probabilities[i][1],
                 report.expected_steps[i]))


def example_iterative_large():
    banner("2. Iterative analysis of a 41-state chain, with residuals")
    n = 40
    rows = {0: {0: 1}, n: {n: 1}}
    for i in range(1, n):
        rows[i] = {i - 1: H, i + 1: H}
    chain = MarkovChain.from_rows(n + 1, rows)
    report = chain.absorption_analysis(method="iterative", tol=1e-10)
    mid = 20
    print("start %d: P(absorb at 0)=%.10f  E[steps]=%.10f (exact 400)"
          % (mid, report.probabilities[mid][0], report.expected_steps[mid]))
    for label, result in sorted(report.systems.items()):
        print("  system %-16s iters=%5d  final residual=%.3e  "
              "error_bound=%s"
              % (label, result.iterations, result.residuals[-1],
                 "%.3e" % result.error_bound
                 if result.error_bound is not None else "n/a "
                 "(I-Q not strictly dominant; residual is the certificate)"))
    print("mass conservation while propagating delta_20:")
    dist = [0.0] * (n + 1)
    dist[mid] = 1.0
    for step, new, mass_error in chain.propagate(dist, 400):
        if step % 100 == 0:
            print("  step %3d: absorbed mass=%.6f  mass error=%+.2e"
                  % (step, new[0] + new[n], mass_error))


def example_row_revision_reuse():
    banner("3. Row revision with factorization reuse (Woodbury)")
    rows = {0: {0: 1}, 4: {4: 1}}
    for i in (1, 2, 3):
        rows[i] = {i - 1: H, i + 1: H}
    chain = MarkovChain.from_rows(5, rows)
    base = chain.transient_solver()
    revised = chain.revised({1: {0: Fraction(3, 4), 2: Fraction(1, 4)}})
    solver2 = revised.transient_solver(base=base)
    print("reuses base factorization:", solver2.reuses_factorization)
    got = solver2.solve([1.0, 1.0, 1.0])
    print("E[steps] after revising row 1 -> {0: 3/4, 2: 1/4}:",
          ["%.4f" % v for v in got], " (exact 9/5, 16/5, 13/5)")
    try:
        chain.revised({1: {0: 0.9}})
    except ValidationError as exc:
        print("row-sum mismatch correctly rejected:", exc)


def example_non_absorbing_closed_class():
    banner("4. Reachable non-absorbing closed class -> infinite expectation")
    rows = {
        0: {1: 1},
        1: {0: 1},
        2: {0: H, 3: H},
        3: {3: 1},
    }
    chain = MarkovChain.from_rows(4, rows)
    report = chain.absorption_analysis(method="exact")
    print("closed classes:", report.closed_classes,
          " absorbing:", report.absorbing)
    for s in sorted(report.probabilities):
        print("  state %d: P per class=%s  E[steps]=%s"
              % (s, report.probabilities[s], report.expected_steps[s]))


def example_validation_and_column_convention():
    banner("5. Validation discipline and the column convention")
    m = SparseTransitionMatrix(2)
    m.set(0, 0, 0.3)
    m.set(0, 1, 0.6)
    m.set(1, 1, 1.0)
    try:
        m.validate()
    except ValidationError as exc:
        print("row-sum mismatch correctly rejected:", exc)
    col = SparseTransitionMatrix.from_rows(
        3, {0: {0: H, 2: 1}, 1: {0: H, 1: H}, 2: {1: H}},
        convention="column")
    chain = MarkovChain(col)
    print("column-stochastic input accepted; outgoing of state 0:",
          chain.out[0])


if __name__ == "__main__":
    example_exact_reference()
    example_iterative_large()
    example_row_revision_reuse()
    example_non_absorbing_closed_class()
    example_validation_and_column_convention()
