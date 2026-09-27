"""Statistical core of the empirical audit.

Definitions
-----------
Neighboring datasets
    Two synthetic datasets D0, D1 that differ in exactly one record. Here
    each dataset is a single bit, so D0 = 0 and D1 = 1.
Event
    A measurable subset S of the mechanism's output space, given as a
    predicate ``event(output) -> bool`` plus a human-readable description.
Probability ratio
    r = P[M(D1) in S] / P[M(D0) in S]. For an eps-DP mechanism, both r and
    1/r must be <= e^eps for *every* event S. We estimate r from samples.

Statistical notes
-----------------
* Confidence intervals for the two event probabilities are Wilson score
  intervals, each at level 1 - alpha/2, so the resulting ratio interval
  (lo1/hi2, hi1/lo2) has joint coverage >= 1 - alpha (Bonferroni).
* Zero observed successes is NEVER treated as a true probability of 0.
  With k = 0 out of n we report the one-sided Clopper-Pearson upper bound
  1 - gamma^(1/n) (with gamma = alpha/4 to keep the tail allocation
  consistent with the two-sided Wilson interval). The all-successes case
  is symmetric.
* A verdict of "consistent" means only that this finite experiment did
  not detect a violation; it is NOT a proof of differential privacy.
"""

from __future__ import annotations

import math
import random
from dataclasses import dataclass, field
from statistics import NormalDist
from typing import Callable

Mechanism = Callable[[int, float, random.Random], int]


def wilson_interval(successes, trials, gamma):
    """Two-sided Wilson score interval at confidence level 1 - gamma.

    For zero (or all) successes, falls back to the exact one-sided
    Clopper-Pearson bound so that an unobserved event keeps a strictly
    positive upper probability bound.
    """
    if not 0 <= successes <= trials:
        raise ValueError("successes must satisfy 0 <= successes <= trials")
    if trials <= 0:
        raise ValueError("trials must be positive")
    if not 0 < gamma < 1:
        raise ValueError("gamma must be in (0, 1)")

    if successes == 0:
        # One-sided Clopper-Pearson: P[p <= 1 - gamma^(1/n)] = 1 - gamma.
        # gamma/2 keeps the same per-tail allocation as the Wilson branch.
        return (0.0, 1.0 - (gamma / 2.0) ** (1.0 / trials))
    if successes == trials:
        return ((gamma / 2.0) ** (1.0 / trials), 1.0)

    z = NormalDist().inv_cdf(1.0 - gamma / 2.0)
    p_hat = successes / trials
    denom = 1.0 + z * z / trials
    center = (p_hat + z * z / (2.0 * trials)) / denom
    half = (z / denom) * math.sqrt(
        p_hat * (1.0 - p_hat) / trials + z * z / (4.0 * trials * trials)
    )
    return (max(0.0, center - half), min(1.0, center + half))


@dataclass(frozen=True)
class AuditResult:
    """Complete, reproducible record of one audit experiment."""

    mechanism_name: str
    eps_claimed: float
    event_description: str
    trials_per_dataset: int
    successes_d0: int
    successes_d1: int
    p_d0_hat: float
    p_d1_hat: float
    ci_d0: tuple
    ci_d1: tuple
    ratio_hat: float
    ratio_ci: tuple
    alpha: float
    seed: int
    verdict: str
    notes: tuple = field(default_factory=tuple)

    def render(self):
        lines = [
            f"mechanism          : {self.mechanism_name}",
            f"claimed eps        : {self.eps_claimed}",
            f"event S            : {self.event_description}",
            f"trials per dataset : {self.trials_per_dataset}",
            f"seed               : {self.seed}",
            f"alpha              : {self.alpha}",
            f"D0=0: hits {self.successes_d0}/{self.trials_per_dataset}"
            f"  p_hat={self.p_d0_hat:.6f}"
            f"  CI=[{self.ci_d0[0]:.6f}, {self.ci_d0[1]:.6f}]",
            f"D1=1: hits {self.successes_d1}/{self.trials_per_dataset}"
            f"  p_hat={self.p_d1_hat:.6f}"
            f"  CI=[{self.ci_d1[0]:.6f}, {self.ci_d1[1]:.6f}]",
            f"ratio p1/p0        : hat={self.ratio_hat:.6f}"
            f"  CI=[{self.ratio_ci[0]:.6f}, {self.ratio_ci[1]:.6f}]"
            f"  (e^eps={math.exp(self.eps_claimed):.6f})",
            f"verdict            : {self.verdict}",
        ]
        lines.extend(f"note               : {n}" for n in self.notes)
        return "\n".join(lines)


def _count_hits(mechanism, record, eps, trials, event, rng):
    return sum(
        1 for _ in range(trials) if event(mechanism(record, eps, rng))
    )


def audit_mechanism(
    mechanism,
    eps_claimed,
    event,
    event_description,
    trials_per_dataset=100_000,
    alpha=0.05,
    seed=0,
):
    """Run a synthetic two-dataset audit of ``mechanism``.

    Parameters
    ----------
    mechanism:
        Injectable mechanism ``f(record, eps, rng) -> output``.
    eps_claimed:
        The privacy budget the mechanism *claims* to satisfy.
    event, event_description:
        The audited output event S and its human-readable definition.
    trials_per_dataset:
        Number of independent mechanism invocations per neighboring
        dataset (recorded in the result for reproducibility).
    alpha:
        Family-wise significance level for the ratio confidence interval.
    seed:
        Seed for ``random.Random``; identical seeds reproduce identical
        results bit-for-bit.

    Verdicts
    --------
    violation_detected
        Lower confidence bound of the probability ratio exceeds e^eps:
        statistical evidence of a DP violation at level alpha. The event
        description plus seed fully reproduce the finding.
    consistent_not_proof
        Upper confidence bound is at most e^eps: no violation detected.
        This is NOT a proof of privacy.
    inconclusive
        Interval straddles e^eps; increase ``trials_per_dataset``.
    """
    if trials_per_dataset <= 0:
        raise ValueError("trials_per_dataset must be positive")
    if not 0 < alpha < 1:
        raise ValueError("alpha must be in (0, 1)")
    if eps_claimed < 0:
        raise ValueError("eps_claimed must be non-negative")

    rng = random.Random(seed)
    hits0 = _count_hits(mechanism, 0, eps_claimed, trials_per_dataset, event, rng)
    hits1 = _count_hits(mechanism, 1, eps_claimed, trials_per_dataset, event, rng)

    # Each probability CI at level 1 - alpha/2 -> ratio interval coverage
    # >= 1 - alpha by Bonferroni.
    ci0 = wilson_interval(hits0, trials_per_dataset, alpha / 2.0)
    ci1 = wilson_interval(hits1, trials_per_dataset, alpha / 2.0)

    p0 = hits0 / trials_per_dataset
    p1 = hits1 / trials_per_dataset
    ratio_hat = math.inf if p0 == 0.0 else p1 / p0
    ratio_lo = 0.0 if ci1[0] == 0.0 and ci0[1] == 0.0 else ci1[0] / ci0[1]
    ratio_hi = math.inf if ci0[0] == 0.0 else ci1[1] / ci0[0]

    bound = math.exp(eps_claimed)
    notes = []
    if hits0 == 0 or hits1 == 0:
        notes.append(
            "zero observations on one side: upper bound is the one-sided "
            "Clopper-Pearson limit, NOT a true probability of 0"
        )
    if ratio_lo > bound:
        verdict = "violation_detected"
        notes.append(
            f"reproducible violation: event '{event_description}' with "
            f"seed={seed}, trials={trials_per_dataset}"
        )
    elif ratio_hi <= bound:
        verdict = "consistent_not_proof"
        notes.append(
            "empirical consistency is statistical evidence only and does "
            "NOT prove differential privacy"
        )
    else:
        verdict = "inconclusive"
        notes.append("interval straddles e^eps; increase trials_per_dataset")

    return AuditResult(
        mechanism_name=getattr(mechanism, "__name__", repr(mechanism)),
        eps_claimed=eps_claimed,
        event_description=event_description,
        trials_per_dataset=trials_per_dataset,
        successes_d0=hits0,
        successes_d1=hits1,
        p_d0_hat=p0,
        p_d1_hat=p1,
        ci_d0=ci0,
        ci_d1=ci1,
        ratio_hat=ratio_hat,
        ratio_ci=(ratio_lo, ratio_hi),
        alpha=alpha,
        seed=seed,
        verdict=verdict,
        notes=tuple(notes),
    )
