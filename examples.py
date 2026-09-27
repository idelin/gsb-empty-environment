"""Runnable examples: python3.11 examples.py

Every printed conclusion is labeled [EXACT] (analytic) or [EMPIRICAL]
(finite-sample Monte Carlo).
"""

import random
import tempfile
from pathlib import Path

from importance_sampling import (
    AdaptiveIS,
    DiscreteDistribution,
    MixtureProposal,
    check_discrete_coverage,
    exact_discrete_expectation,
    run_importance_sampling,
)

DOMAIN = list(range(10))
F = lambda x: x


def show(result, label):
    print(f"  {label}: ordinary={result.ordinary:.4f}  "
          f"self_normalized={result.self_normalized:.4f}  "
          f"ESS={result.ess:.1f}/{result.n}  seed={result.seed}")
    for d in result.diagnostics:
        print(f"    [{d.severity.upper()}] {d.code}: {d.message}")


def main():
    target = DiscreteDistribution({x: (x + 1) ** 2 for x in DOMAIN})
    uniform = DiscreteDistribution({x: 1.0 for x in DOMAIN})
    exact = exact_discrete_expectation(target, F)
    print(f"[EXACT] E_target[X] on finite domain = {exact:.6f}")

    print("\n--- 1. Ordinary vs self-normalized (single run, n=2000, seed=42) ---")
    res = run_importance_sampling(2000, target.logpdf, uniform.logpdf,
                                  uniform.sample, F,
                                  rng=random.Random(42), seed=42)
    show(res, "[EMPIRICAL]")

    print("\n--- 2. Bias comparison over 5000 replications, n=20 ---")
    rng = random.Random(2026)
    ord_acc, sn_acc = [], []
    for _ in range(5000):
        r = run_importance_sampling(20, target.logpdf, uniform.logpdf,
                                    uniform.sample, F, rng=rng, seed=2026)
        ord_acc.append(r.ordinary)
        sn_acc.append(r.self_normalized)
    mo = sum(ord_acc) / len(ord_acc)
    ms = sum(sn_acc) / len(sn_acc)
    print(f"  [EXACT]     ordinary estimator is unbiased; self-normalized is "
          f"biased for finite n (ratio of random variables) but consistent")
    print(f"  [EMPIRICAL] mean ordinary       = {mo:.4f} (exact {exact:.4f})")
    print(f"  [EMPIRICAL] mean self-normalized= {ms:.4f} (bias {ms - exact:+.4f} at n=20)")

    print("\n--- 3. Mixture proposal (0.3 uniform + 0.7 increasing) ---")
    increasing = DiscreteDistribution({x: (x + 1) for x in DOMAIN})
    mix = MixtureProposal([uniform, increasing], [0.3, 0.7])
    res = run_importance_sampling(5000, target.logpdf, mix.logpdf,
                                  mix.sample, F, rng=random.Random(7), seed=7)
    show(res, "[EMPIRICAL]")

    print("\n--- 4. Adaptive rounds with checkpoint/restore ---")
    adaptive = AdaptiveIS(target.logpdf, F, rng=random.Random(100), seed=100)
    adaptive.run_round(200, uniform.logpdf, uniform.sample,
                       description="round0: uniform")
    with tempfile.TemporaryDirectory() as tmp:
        ckpt = Path(tmp) / "adaptive_ckpt.json"
        adaptive.save_checkpoint(ckpt)
        print(f"  checkpoint saved: {ckpt.name} "
              f"(round0 weights frozen with uniform proposal)")
        restored = AdaptiveIS.load_checkpoint(ckpt, target.logpdf, F,
                                              rng=random.Random(200))
    # Weights from round 0 are reused verbatim, NOT recomputed with the
    # adapted proposal of round 1.
    assert restored.rounds[0].log_weights == adaptive.rounds[0].log_weights
    restored.run_round(800, mix.logpdf, mix.sample,
                       description="round1: adapted mixture")
    show(restored.estimates(), "[EMPIRICAL] pooled (weights frozen per round)")

    print("\n--- 5. Diagnostics ---")
    partial = DiscreteDistribution({x: 1.0 for x in DOMAIN if x < 5})
    diag = check_discrete_coverage(DOMAIN, target.pdf, partial.pdf)
    print(f"  [EXACT] zero coverage -> {diag.severity}: {diag.message}")
    skewed = DiscreteDistribution({x: 0.5 ** x for x in DOMAIN})
    res = run_importance_sampling(500, target.logpdf, skewed.logpdf,
                                  skewed.sample, F,
                                  rng=random.Random(9), seed=9)
    show(res, "[EMPIRICAL] skewed proposal (decaying 0.5^x)")


if __name__ == "__main__":
    main()
