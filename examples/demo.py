"""End-to-end demo: exact variable elimination vs. empirical sampling.

Run from the repo root:  python3 examples/demo.py
"""

import random
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

from factorelim import (
    ContradictoryEvidenceError,
    FactorGraph,
    estimate_query,
    greedy_order,
    query,
)


def build_sprinkler():
    """Rain -> Sprinkler, Rain -> WetGrass, Sprinkler -> WetGrass."""
    g = FactorGraph()
    for v in ("Rain", "Sprinkler", "WetGrass"):
        g.add_variable(v, (0, 1))
    g.add_factor(("Rain",), [0.8, 0.2])
    g.add_factor(("Rain", "Sprinkler"), [0.6, 0.4, 0.99, 0.01])
    g.add_factor(
        ("Rain", "Sprinkler", "WetGrass"),
        [0.95, 0.05, 0.10, 0.90, 0.20, 0.80, 0.01, 0.99],
    )
    return g


def main():
    g = build_sprinkler()
    evidence = {"WetGrass": 1}

    print("=== Exact inference (variable elimination) ===")
    exact = query(g, ("Rain",), evidence=evidence)
    print(exact)
    print()

    print("=== Elimination order controls intermediate table size ===")
    star = FactorGraph()
    star.add_variable("X", (0, 1))
    for i in range(4):
        star.add_variable(f"L{i}", (0, 1))
    star.add_factor(("X",), [0.3, 0.7])
    for i in range(4):
        star.add_factor(("X", f"L{i}"), [0.9, 0.1, 0.25, 0.75])
    leaves = tuple(f"L{i}" for i in range(4))
    bad = query(star, (), order=("X",) + leaves)
    good = query(star, (), order=greedy_order(
        [f.restrict({}) for f in star.factors], ()))
    print(f"  star graph, center first: sizes={bad.stats.intermediate_sizes}"
          f" max={bad.stats.max_intermediate_size}")
    print(f"  star graph, greedy order {good.stats.order}: "
          f"sizes={good.stats.intermediate_sizes}"
          f" max={good.stats.max_intermediate_size}")
    print(f"  same normalizer either way: Z={bad.normalizer:.6f}")
    print()

    print("=== Empirical estimation (caller-injected RNG) ===")
    seed = 20261001
    rng = random.Random(seed)  # caller owns the random source
    est = estimate_query(g, ("Rain",), evidence=evidence,
                         n_samples=20000, rng=rng, seed=seed)
    print(est)
    print()

    print("=== Exact vs. empirical side by side ===")
    for key in exact.probabilities:
        print(f"  Rain={key[0]}: exact={exact.probabilities[key]:.4f} "
              f"empirical={est.probabilities[key]:.4f}")
    print(f"  Z: exact={exact.normalizer:.6f} "
          f"empirical={est.normalizer:.6f}")
    print()

    print("=== Contradictory evidence ===")
    impossible = FactorGraph()
    impossible.add_variable("A", (0, 1))
    impossible.add_factor(("A",), [1.0, 0.0])  # A=1 has probability 0
    try:
        query(impossible, ("A",), evidence={"A": 1})
    except ContradictoryEvidenceError as exc:
        print(f"  raised ContradictoryEvidenceError: {exc}")


if __name__ == "__main__":
    main()
