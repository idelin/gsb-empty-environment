"""Confidence analysis for aggregate RR estimates.

Sample assumptions (stated explicitly, not implied):
  * Each participant's true value is an i.i.d. draw from the population
    distribution x, and each response is generated independently through
    that participant's known channel.
  * Observed output counts within a group are therefore multinomial with
    probabilities A_g x; we plug in the observed frequencies f_g.
  * Intervals use a normal (Wald) approximation for the *raw* linear
    estimator.  Coverage is asymptotic: for small n or true probabilities
    near 0/1 the actual coverage can deviate noticeably from the nominal
    level.  The clipped/simplex-projected estimate has a different,
    non-normal sampling distribution; these intervals do NOT strictly cover
    it (they are centered on the raw estimate and truncated to [0, 1] only
    for display).
  * Degenerate channels limit identifiability: if the joint normal
    equations are rank deficient (e.g. epsilon = 0 for all groups, or
    k = 1), no finite interval exists for the unidentifiable directions and
    this module refuses to fabricate one.
"""
from __future__ import annotations

import math
from dataclasses import dataclass, field
from statistics import NormalDist

from .estimation import ChannelGroup, _normal_equations
from .linalg import dot, jacobi_eigen, mat_vec, transpose

COVERAGE_ASSUMPTIONS = (
    "i.i.d. sampling from the population; known channel matrices; multinomial "
    "counts with plug-in frequencies; Wald (normal) approximation for the raw "
    "linear estimator — asymptotic coverage only, unreliable for small n or "
    "boundary probabilities; intervals describe the unclipped estimator."
)


@dataclass
class ConfidenceReport:
    confidence: float
    raw_estimate: list[float]
    std_errors: list[float]
    intervals: list[tuple[float, float]]          # truncated to [0, 1] for display
    untruncated_intervals: list[tuple[float, float]]
    assumptions: str
    limitations: list[str] = field(default_factory=list)


def _sandwich_covariance(groups: list[ChannelGroup], rank_tol: float = 1e-10):
    """Covariance of the raw estimator on the identifiable subspace.

    Cov = M^+ G M^+ with M = sum_g n_g A_g^T A_g and
    G = sum_g n_g^2 A_g^T Var(f_g) A_g = sum_g n_g A_g^T S_g A_g,
    S_g = diag(f_g) - f_g f_g^T.
    """
    k = groups[0].channel.k
    m, _ = _normal_equations(groups)
    gmat = [[0.0] * k for _ in range(k)]
    for g in groups:
        f = g.frequencies
        a = g.channel.matrix
        at = transpose(a)
        # n_g * A_g^T S_g A_g = n_g * ((A^T D A) - (A^T f)(A^T f)^T).
        atf = mat_vec(at, f)
        for i in range(k):
            for j in range(k):
                atda = sum(at[i][y] * f[y] * a[y][j] for y in range(k))
                gmat[i][j] += g.n * (atda - atf[i] * atf[j])
    eigenvalues, eigenvectors = jacobi_eigen(m)
    lam_max = eigenvalues[0]
    threshold = rank_tol * max(lam_max, 1.0)
    retained = [(lam, vec) for lam, vec in zip(eigenvalues, eigenvectors) if lam > threshold]
    n_null = sum(1 for lam in eigenvalues if lam <= threshold)

    # Cov = sum_{i,j} (v_i^T G v_j)/(lam_i lam_j) v_i v_j^T
    cov = [[0.0] * k for _ in range(k)]
    for lam1, v1 in retained:
        gv1 = mat_vec(gmat, v1)
        for lam2, v2 in retained:
            w = dot(v2, gv1) / (lam1 * lam2)
            if w == 0.0:
                continue
            for i in range(k):
                for j in range(k):
                    cov[i][j] += w * v1[i] * v2[j]
    return cov, n_null


def confidence_intervals(
    groups: list[ChannelGroup],
    confidence: float = 0.95,
    rank_tol: float = 1e-10,
) -> ConfidenceReport:
    """Wald confidence intervals for the raw joint estimator.

    Raises ValueError when the joint system is not identifiable (degenerate
    channels such as epsilon = 0 everywhere or k = 1), because no finite
    interval exists along unidentifiable directions.
    """
    if not 0.0 < confidence < 1.0:
        raise ValueError(f"confidence must be in (0, 1), got {confidence}")
    if not groups:
        raise ValueError("at least one channel group is required")
    k = groups[0].channel.k
    if any(g.channel.k != k for g in groups):
        raise ValueError("all channel groups must share the same alphabet size k")

    from .estimation import estimate_distribution

    report = estimate_distribution(groups, rank_tol=rank_tol)
    limitations: list[str] = []
    if not report.identifiable:
        raise ValueError(
            "joint system is not identifiable (degenerate channels, e.g. epsilon = 0 "
            "for all groups or k = 1): no finite confidence interval exists along "
            f"{len(report.null_directions)} unidentifiable direction(s)."
        )
    if k == 1:
        raise ValueError("k = 1: the distribution is trivially [1.0]; no interval is needed.")

    cov, _ = _sandwich_covariance(groups, rank_tol=rank_tol)
    z = NormalDist().inv_cdf((1.0 + confidence) / 2.0)
    std_errors = [math.sqrt(max(cov[i][i], 0.0)) for i in range(k)]
    untruncated = [
        (x - z * s, x + z * s) for x, s in zip(report.raw_estimate, std_errors)
    ]
    truncated = [(max(lo, 0.0), min(hi, 1.0)) for lo, hi in untruncated]
    if any(lo < 0.0 or hi > 1.0 for lo, hi in untruncated):
        limitations.append(
            "some untruncated intervals leave [0, 1]; the displayed intervals are "
            "truncated to [0, 1] for readability, which does not affect the raw estimator."
        )
    total_n = sum(g.n for g in groups)
    if total_n < 30 * k:
        limitations.append(
            f"total sample size {total_n} is small relative to k = {k}: normal-approximation "
            "coverage may be poor."
        )
    return ConfidenceReport(
        confidence=confidence,
        raw_estimate=report.raw_estimate,
        std_errors=std_errors,
        intervals=truncated,
        untruncated_intervals=untruncated,
        assumptions=COVERAGE_ASSUMPTIONS,
        limitations=limitations,
    )
