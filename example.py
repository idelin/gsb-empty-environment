"""Runnable demo: exact variable elimination vs Gibbs sampling.

Run with:  python3.11 example.py
"""

import random

from factor_graph import (
    ContradictoryEvidenceError,
    DomainMismatchError,
    FactorGraph,
    elimination_width,
    greedy_order,
)


def build_sprinkler():
    """Cloudy -> Sprinkler/Rain -> WetGrass, all binary, as a factor graph."""
    g = FactorGraph()
    binary = (0, 1)
    g.add(("C",), {"C": binary}, [0.5, 0.5])
    g.add(("S", "C"), {"S": binary, "C": binary}, [0.5, 0.9, 0.5, 0.1])
    g.add(("R", "C"), {"R": binary, "C": binary}, [0.8, 0.2, 0.2, 0.8])
    g.add(("S", "R", "W"), {"S": binary, "R": binary, "W": binary},
          [1.0, 0.0, 0.1, 0.9, 0.1, 0.9, 0.01, 0.99])
    return g


def main():
    g = build_sprinkler()
    evidence = {"W": 1}

    print("=== Elimination order (controls intermediate table size) ===")
    star = FactorGraph()  # center C connected to 4 leaves
    star.add(("C",), {"C": (0, 1)}, [0.5, 0.5])
    for i in range(4):
        star.add((f"L{i}", "C"), {f"L{i}": (0, 1), "C": (0, 1)},
                 [0.3, 0.7, 0.6, 0.4])
    hidden = ["C", "L0", "L1", "L2", "L3"]
    scopes = [set(f.variables) for f in star.factors]
    greedy = greedy_order(scopes, hidden, star.domains)
    print(f"star graph, greedy order {greedy}: max intermediate table = "
          f"{elimination_width(scopes, greedy, star.domains)} entries")
    print(f"star graph, center-first  {hidden}: max intermediate table = "
          f"{elimination_width(scopes, hidden, star.domains)} entries")

    print("\n=== Exact (variable elimination), kind='exact' ===")
    exact = g.eliminate(["R"], evidence=evidence)
    print(f"P(R | W=1)  exact:      "
          f"P(R=1) = {exact.probability(1):.4f}   [kind={exact.kind}]")
    print(f"normalization constant Z = P(W=1) = {exact.normalization:.4f}")

    print("\n=== Empirical (Gibbs sampling), kind='empirical' ===")
    seed, samples = 2024, 20000
    rng = random.Random(seed)  # random source injected by the caller
    est = g.sample_gibbs(["R"], evidence=evidence, rng=rng,
                         samples=samples, burn_in=500, seed=seed)
    print(f"P(R=1 | W=1) empirical: {est.probability(1):.4f}   "
          f"[kind={est.kind}, seed={est.seed}, samples={est.samples}]")

    print("\n=== Cross-check: full enumeration agrees with elimination ===")
    enum = g.enumerate_marginal(["R"], evidence=evidence)
    print(f"enumeration P(R=1 | W=1) = {enum.probability(1):.4f}, "
          f"Z = {enum.normalization:.4f}")

    print("\n=== Error handling ===")
    impossible = FactorGraph()
    impossible.add(("A",), {"A": (0, 1)}, [1.0, 0.0])  # A=1 has probability 0
    try:
        impossible.eliminate([], evidence={"A": 1})  # Z = P(A=1) = 0
    except ContradictoryEvidenceError as exc:
        print(f"contradictory evidence -> {type(exc).__name__}: {exc}")
    try:
        g.add(("C", "X"), {"C": ("yes", "no"), "X": (0, 1)},
              [0.1, 0.9, 0.2, 0.8])
    except DomainMismatchError as exc:
        print(f"domain mismatch        -> {type(exc).__name__}: {exc}")


if __name__ == "__main__":
    main()
