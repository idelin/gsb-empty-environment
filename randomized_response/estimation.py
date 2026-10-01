"""Aggregate estimation of a population distribution from RR outputs.

Model
-----
Each participant holds a true value in {0, ..., k-1} drawn i.i.d. from an
unknown population distribution ``x`` (the estimand) and reports through a
*known* response channel.  Different participants may use different known
channel matrices; participants sharing the same channel form a *channel
group*.  For group g with channel ``A_g`` and observed output frequencies
``f_g`` we have approximately ``f_g ~= A_g x``, and we recover ``x`` by the
weighted least-squares problem

    minimize  sum_g  n_g * || A_g x - f_g ||^2

whose normal equations are  M x = b  with

    M = sum_g n_g A_g^T A_g,   b = sum_g n_g A_g^T f_g.

Identifiability
---------------
``x`` is identifiable iff M has full rank.  Near-zero eigenvalues of M span
the *unidentifiable directions*: components of x along those directions
cannot be recovered from the data.  We never force a raw inverse; instead we
use the Moore-Penrose pseudoinverse on the identifiable subspace and report
the null directions explicitly (e.g. epsilon = 0 for every group leaves only
the uniform direction identifiable).

Constraints
-----------
The raw pseudoinverse estimate is linear in the data and may leave the
natural range [0, 1] or fail to sum to 1.  We report it unchanged, and
*separately* report a constrained estimate obtained by Euclidean projection
onto the probability simplex {x >= 0, sum x = 1}.  Which coordinates were
clipped is listed explicitly.
"""
from __future__ import annotations

import math
from dataclasses import dataclass, field

from .channels import RandomizedResponseChannel, validate_counts
from .linalg import dot, jacobi_eigen, mat_vec, project_to_simplex, transpose


@dataclass(frozen=True)
class ChannelGroup:
    """Observed output counts for participants sharing one known channel."""

    channel: RandomizedResponseChannel
    counts: tuple[int, ...]

    def __init__(self, channel: RandomizedResponseChannel, counts):
        object.__setattr__(self, "channel", channel)
        object.__setattr__(self, "counts", tuple(counts))
        validate_counts(list(self.counts), channel.k)

    @property
    def n(self) -> int:
        return sum(self.counts)

    @property
    def frequencies(self) -> list[float]:
        return [c / self.n for c in self.counts]


@dataclass
class EstimationReport:
    """Result of joint distribution recovery.

    raw_estimate:      unconstrained pseudoinverse estimate (may leave [0,1]
                       and may not sum to 1; reported as-is).
    estimate:          constrained estimate (projected onto the simplex:
                       non-negative, sums to 1).
    clipped:           per-coordinate flag: raw value fell outside [0, 1].
    identifiable:      True iff the joint normal-equations matrix has full
                       rank (no unidentifiable directions).
    null_directions:   orthonormal basis of the unidentifiable subspace
                       (empty when identifiable).
    residual:          weighted RMS fit residual
                       sqrt( sum_g n_g ||A_g x - f_g||^2 / sum_g n_g )
                       evaluated at the raw estimate.
    condition_number:  condition number of M restricted to the identifiable
                       subspace (inf when nothing is identifiable).
    eigenvalues:       eigenvalues of M (descending).
    notes:             human-readable diagnostics and caveats.
    """

    raw_estimate: list[float]
    estimate: list[float]
    clipped: list[bool]
    identifiable: bool
    null_directions: list[list[float]]
    residual: float
    condition_number: float
    eigenvalues: list[float]
    notes: list[str] = field(default_factory=list)


def _normal_equations(groups: list[ChannelGroup]):
    k = groups[0].channel.k
    m = [[0.0] * k for _ in range(k)]
    b = [0.0] * k
    for g in groups:
        a = g.channel.matrix
        at = transpose(a)
        f = g.frequencies
        atf = mat_vec(at, f)
        for i in range(k):
            b[i] += g.n * atf[i]
            for j in range(k):
                m[i][j] += g.n * sum(at[i][y] * a[y][j] for y in range(k))
    return m, b


def estimate_distribution(groups: list[ChannelGroup], rank_tol: float = 1e-10) -> EstimationReport:
    """Jointly recover the population distribution from one or more groups."""
    if not groups:
        raise ValueError("at least one channel group is required")
    k = groups[0].channel.k
    if any(g.channel.k != k for g in groups):
        raise ValueError("all channel groups must share the same alphabet size k")

    notes: list[str] = []
    m, b = _normal_equations(groups)
    eigenvalues, eigenvectors = jacobi_eigen(m)
    lam_max = eigenvalues[0]
    threshold = rank_tol * max(lam_max, 1.0)

    retained = [(lam, vec) for lam, vec in zip(eigenvalues, eigenvectors) if lam > threshold]
    null_directions = [vec for lam, vec in zip(eigenvalues, eigenvectors) if lam <= threshold]
    identifiable = not null_directions

    # Pseudoinverse applied to b on the identifiable subspace only.
    raw = [0.0] * k
    for lam, vec in retained:
        coef = dot(vec, b) / lam
        for i in range(k):
            raw[i] += coef * vec[i]

    # Weighted RMS residual of the fit.
    total_n = sum(g.n for g in groups)
    sse = 0.0
    for g in groups:
        fit = mat_vec(g.channel.matrix, raw)
        sse += g.n * sum((fi - fi_hat) ** 2 for fi, fi_hat in zip(g.frequencies, fit))
    residual = math.sqrt(sse / total_n)

    if retained:
        lam_min_retained = min(lam for lam, _ in retained)
        condition = math.sqrt(lam_max / lam_min_retained)
    else:
        condition = math.inf

    projected = project_to_simplex(raw)
    clipped = [not (0.0 <= xi <= 1.0) for xi in raw]

    if not identifiable:
        notes.append(
            f"channel group(s) are not jointly identifiable: {len(null_directions)} "
            "unidentifiable direction(s) detected; the raw estimate is the "
            "minimum-norm solution and no unique inverse exists (no forced inversion)."
        )
        if all(g.channel.epsilon == 0.0 for g in groups):
            notes.append(
                "all groups use epsilon = 0: outputs are independent of inputs, so only "
                "the uniform direction is identifiable."
            )
    if k == 1:
        notes.append("k = 1 special case: the alphabet has one value; the estimate is trivially [1.0].")
    if any(clipped):
        idx = [i for i, c in enumerate(clipped) if c]
        notes.append(
            f"raw estimate left the natural range at coordinate(s) {idx}; the constrained "
            "estimate (simplex projection) is reported separately in `estimate`."
        )
    if condition > 1e8:
        notes.append(f"large condition number ({condition:.3e}): estimates are ill-conditioned.")

    return EstimationReport(
        raw_estimate=raw,
        estimate=projected,
        clipped=clipped,
        identifiable=identifiable,
        null_directions=null_directions,
        residual=residual,
        condition_number=condition,
        eigenvalues=eigenvalues,
        notes=notes,
    )


def estimate_from_counts(channel: RandomizedResponseChannel, counts) -> EstimationReport:
    """Convenience wrapper for a single homogeneous channel group."""
    return estimate_distribution([ChannelGroup(channel, counts)])
