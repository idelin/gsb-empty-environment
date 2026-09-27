"""End-to-end demo of the importance_sampling package.

Run from the repository root:  python3.11 examples/run_example.py

Output sections are labeled EXACT (closed-form reference values) or
EMPIRICAL (Monte Carlo estimates from seeded runs).
"""

import os
import random
import sys
import tempfile

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from importance_sampling import (
    AdaptiveIS,
    DiscreteDistribution,
    MixtureProposal,
    estimate,
    exact_expectation,
)

TARGET = DiscreteDistribution({0: 0.5, 1: 0.3, 2: 0.2}, name="target")
PROPOSAL = DiscreteDistribution({0: 0.2, 1: 0.3, 2: 0.5}, name="proposal")


def f(x):
    return float(x)


def show_diagnostics(result):
    if not result.diagnostics:
        print("    diagnostics: none")
    for d in result.diagnostics:
        print(f"    diagnostics: [{d.severity}] {d.code}: {d.message}")


def main():
    mu = exact_expectation(TARGET, f)
    print("=== EXACT (closed-form reference over the finite domain) ===")
    print(f"    E_target[f] = {mu}")

    print("\n=== EMPIRICAL (single run, seed=2024, n=2000) ===")
    result = estimate(TARGET, PROPOSAL, f, 2000, random.Random(2024), seed=2024)
    print(f"    ordinary IS estimate        : {result.ordinary:.6f}")
    print(f"    self-normalized IS estimate : {result.self_normalized:.6f}")
    print(f"    ESS = {result.ess:.1f} of n = {result.n} (seed={result.seed})")
    show_diagnostics(result)

    print("\n=== EMPIRICAL (bias comparison, 2000 replications, n=25 each) ===")
    rng = random.Random(99)
    ordinary_means, snis_means = [], []
    for _ in range(2000):
        r = estimate(TARGET, PROPOSAL, f, 25, rng)
        ordinary_means.append(r.ordinary)
        snis_means.append(r.self_normalized)
    mean_ord = sum(ordinary_means) / len(ordinary_means)
    mean_snis = sum(snis_means) / len(snis_means)
    print(f"    mean ordinary estimate        = {mean_ord:.6f}  (unbiased: matches EXACT {mu})")
    print(f"    mean self-normalized estimate = {mean_snis:.6f}  (finite-n bias, O(1/n); consistent as n grows)")

    print("\n=== EMPIRICAL (mixture proposal, seed=7, n=2000) ===")
    q1 = DiscreteDistribution({0: 0.9, 1: 0.08, 2: 0.02}, name="q1")
    q2 = DiscreteDistribution({0: 0.05, 1: 0.15, 2: 0.8}, name="q2")
    mix = MixtureProposal([q1, q2], weights=[0.5, 0.5], name="mixture")
    r_mix = estimate(TARGET, mix, f, 2000, random.Random(7), seed=7)
    print(f"    ordinary={r_mix.ordinary:.6f}  self-normalized={r_mix.self_normalized:.6f}  ESS={r_mix.ess:.1f}")
    print("    (weight denominators use the mixture density that generated each draw)")

    print("\n=== EMPIRICAL (adaptive proposals + checkpoint resume) ===")
    sampler = AdaptiveIS(TARGET, seed=555)
    sampler.run_iteration(q1, 500, random.Random(10))
    with tempfile.TemporaryDirectory() as tmp:
        path = os.path.join(tmp, "checkpoint.json")
        sampler.save_checkpoint(path)
        print(f"    checkpoint saved after iteration 0 (n={sampler.n}, seed={sampler.seed})")
        restored = AdaptiveIS.load_checkpoint(path, TARGET)
        restored.run_iteration(q2, 500, random.Random(20))  # resume with a NEW proposal
    r_ad = restored.estimate(f)
    print(f"    after resume with new proposal: n={r_ad.n}, ordinary={r_ad.ordinary:.6f}, "
          f"self-normalized={r_ad.self_normalized:.6f}, ESS={r_ad.ess:.1f}")
    print("    (iteration-0 weights were restored verbatim, not recomputed against q2)")

    print("\n=== DIAGNOSTIC (zero coverage -> unavailable) ===")
    blind = DiscreteDistribution({0: 1.0}, name="blind-proposal")
    r_bad = estimate(TARGET, blind, f, 100, random.Random(0), seed=0)
    print(f"    available={r_bad.available}, ordinary={r_bad.ordinary}, self_normalized={r_bad.self_normalized}")
    show_diagnostics(r_bad)

    print("\n=== DIAGNOSTIC (low ESS warning) ===")
    spike = DiscreteDistribution({x: 1.0 for x in range(100)}, name="spread-proposal")
    r_low = estimate(TARGET, spike, f, 400, random.Random(5), seed=5)
    print(f"    available={r_low.available}, ESS={r_low.ess:.1f} of n={r_low.n}")
    show_diagnostics(r_low)


if __name__ == "__main__":
    main()
