"""Aggregation: recover the population distribution from noisy counts.

Participants may use *different* known channel matrices. Participants are
grouped by channel; the population distribution ``pi`` is recovered jointly
by minimizing the weighted least-squares objective

    minimize  sum_g  n_g * || P_g^T pi - f_g ||^2
    subject to  pi >= 0, sum(pi) = 1        (constrained estimate)

where ``f_g`` is the observed output frequency vector of group g and
``n_g`` its sample size.

Three estimates are reported side by side:

- ``pi_unconstrained``: ordinary least squares / inverse estimate. It may
  leave the probability simplex (negative entries or sum != 1); that is
  expected and reported, not hidden.
- ``pi_clipped``: the unconstrained estimate with negative entries clipped
  to 0 and renormalized to sum to 1. Listed separately; it is a heuristic
  post-fix, not a principled projection.
- ``pi_constrained``: projected-gradient minimizer of the objective over
  the simplex (non-negativity and sum-to-one enforced).

Identifiability: if the stacked design matrix (blocks ``P_g^T``) is column
rank deficient, the distribution is *not* identifiable from the data. In
that case no inverse is computed at all; instead the unidentifiable
directions (a null-space basis) are reported. This includes the
``epsilon == 0`` degenerate channel, whose output is independent of the
input.

Diagnostics: per-group residuals ``P_g^T pi_hat - f_g`` and the 2-norm
condition number of the weighted design matrix (inf when rank deficient).

Confidence intervals: normal-approximation (Wald) intervals for the
unconstrained estimator. Coverage rests on explicit assumptions listed in
``EstimateResult.notes``; they are *not* exact and are unavailable for
degenerate / rank-deficient designs.
"""

import math
from dataclasses import dataclass, field

import linalg
from randomized_response import validate_channel

Z_95 = 1.959963984540054  # standard normal 97.5% quantile


@dataclass
class ChannelGroup:
    """Observed output counts for participants sharing one known channel."""

    name: str
    channel: list
    counts: list

    def __post_init__(self):
        k = validate_channel(self.channel)
        if len(self.counts) != k:
            raise ValueError("counts length must match channel size")
        for c in self.counts:
            if isinstance(c, bool) or not isinstance(c, int) or c < 0:
                raise ValueError("counts must be non-negative integers")

    @property
    def k(self):
        return len(self.channel)

    @property
    def n(self):
        return sum(self.counts)

    @property
    def freq(self):
        n = self.n
        return [c / n for c in self.counts] if n else [0.0] * self.k


@dataclass
class EstimateResult:
    identifiable: bool
    null_directions: list
    condition_number: float
    pi_unconstrained: list
    pi_clipped: list
    pi_constrained: list
    residuals: dict
    covariance: list
    ci95: list
    notes: list = field(default_factory=list)


def project_simplex(v):
    """Euclidean projection onto {x >= 0, sum(x) = 1}."""
    u = sorted(v, reverse=True)
    css = 0.0
    rho = 0
    for i, ui in enumerate(u):
        css += ui
        if ui - (css - 1.0) / (i + 1) > 0:
            rho = i + 1
    theta = (sum(u[:rho]) - 1.0) / rho
    return [max(vi - theta, 0.0) for vi in v]


def _design_matrix(groups, weighted):
    """Stacked design matrix with blocks P_g^T (optionally sqrt(n_g)-scaled)."""
    rows = []
    for g in groups:
        w = math.sqrt(g.n) if (weighted and g.n) else (1.0 if not weighted else 0.0)
        pt = linalg.transpose(g.channel)
        for j in range(g.k):
            rows.append([w * v for v in pt[j]])
    return rows


def _weighted_target(groups):
    b = []
    for g in groups:
        w = math.sqrt(g.n) if g.n else 0.0
        b.extend(w * f for f in g.freq)
    return b


def _projected_gradient(H, c, k, max_iter=20000, tol=1e-13):
    """Minimize pi^T H pi - 2 c^T pi over the simplex (projected gradient)."""
    lam_max = linalg.jacobi_eigenvalues(H)[0]
    step = 1.0 / (2.0 * max(lam_max, 1e-300))
    pi = [1.0 / k] * k
    for _ in range(max_iter):
        hp = linalg.matvec(H, pi)
        grad = [2.0 * (h - ci) for h, ci in zip(hp, c)]
        new_pi = project_simplex([p - step * g for p, g in zip(pi, grad)])
        if max(abs(a - b) for a, b in zip(new_pi, pi)) < tol:
            pi = new_pi
            break
        pi = new_pi
    return pi


def estimate_distribution(groups, z=Z_95):
    """Jointly estimate the population distribution from channel groups.

    ``groups``: non-empty list of ChannelGroup, all with the same k.
    Returns an EstimateResult. Never inverts a rank-deficient design.
    """
    if not groups:
        raise ValueError("at least one channel group is required")
    k = groups[0].k
    if any(g.k != k for g in groups):
        raise ValueError("all groups must share the same alphabet size k")

    notes = []
    design = _design_matrix(groups, weighted=False)
    null_dirs = linalg.null_space(design)
    identifiable = not null_dirs

    weighted_design = _design_matrix(groups, weighted=True)
    cond = linalg.condition_number(weighted_design)

    result = EstimateResult(
        identifiable=identifiable,
        null_directions=null_dirs,
        condition_number=cond,
        pi_unconstrained=None,
        pi_clipped=None,
        pi_constrained=None,
        residuals={},
        covariance=None,
        ci95=None,
        notes=notes,
    )

    if not identifiable:
        notes.append(
            "Design is rank deficient: the population distribution is NOT "
            "identifiable from these channels. No inverse was computed. "
            "See null_directions for the unidentifiable directions. This "
            "happens in particular when epsilon = 0 (output independent of "
            "input) for all groups."
        )
        return result

    b = _weighted_target(groups)
    H = linalg.matmul(linalg.transpose(weighted_design), weighted_design)
    c = linalg.matvec(linalg.transpose(weighted_design), b)
    H_inv = linalg.inverse(H)
    pi_ols = linalg.matvec(H_inv, c)
    result.pi_unconstrained = pi_ols

    clipped = [max(v, 0.0) for v in pi_ols]
    s = sum(clipped)
    result.pi_clipped = [v / s for v in clipped] if s > 0 else None
    if any(v < 0.0 for v in pi_ols) or abs(sum(pi_ols) - 1.0) > 1e-9:
        notes.append(
            "Unconstrained estimate leaves the probability simplex; this is "
            "expected for finite samples. The clipped version is reported "
            "separately as pi_clipped."
        )

    result.pi_constrained = _projected_gradient(H, c, k)

    # Asymptotic covariance of the unconstrained estimator (plug-in).
    # cov(pi_hat) = H^{-1} ( sum_g n_g P_g (diag(q_g) - q_g q_g^T) P_g^T ) H^{-1}
    # with q_g = P_g^T pi_hat. Wald CIs below rely on a normal approximation.
    m = [[0.0] * k for _ in range(k)]
    for g in groups:
        if g.n == 0:
            continue
        pt = linalg.transpose(g.channel)
        q_g = linalg.matvec(pt, pi_ols)
        q_g = [min(max(v, 0.0), 1.0) for v in q_g]
        cov_f = [[(q_g[i] if i == j else 0.0) - q_g[i] * q_g[j]
                  for j in range(k)] for i in range(k)]
        pcovp = linalg.matmul(linalg.matmul(g.channel, cov_f), pt)
        for i in range(k):
            for j in range(k):
                m[i][j] += g.n * pcovp[i][j]
    cov = linalg.matmul(linalg.matmul(H_inv, m), H_inv)
    result.covariance = cov
    result.ci95 = [
        (pi_ols[i] - z * math.sqrt(max(cov[i][i], 0.0)),
         pi_ols[i] + z * math.sqrt(max(cov[i][i], 0.0)))
        for i in range(k)
    ]
    notes.append(
        "Confidence intervals are Wald intervals for the UNCONSTRAINED "
        "estimator. Coverage assumptions: (1) responses are i.i.d. within "
        "each group; (2) every channel matrix is known exactly; (3) sample "
        "sizes are large enough for the normal approximation (multinomial "
        "CLT); (4) the plug-in covariance uses pi_hat in place of the true "
        "pi. Intervals may extend outside [0, 1] and are not valid for "
        "degenerate (rank-deficient) channels such as epsilon = 0."
    )

    for g in groups:
        pred = linalg.matvec(linalg.transpose(g.channel), result.pi_constrained)
        result.residuals[g.name] = [p - f for p, f in zip(pred, g.freq)]
    notes.append(
        "Residuals are per-group (predicted output frequency minus observed "
        "frequency) for the constrained estimate. The condition number is "
        "the 2-norm condition number of the sqrt(n_g)-weighted stacked "
        "design matrix; inf indicates rank deficiency."
    )
    return result
