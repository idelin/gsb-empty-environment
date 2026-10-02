"""Exact primal-feasibility and KKT-residual certificates.

A result may only be called "optimal" when every residual below is
exactly zero (and sign conditions hold).  Small iterative change is
never used as an optimality criterion anywhere in this package.
"""

from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction


@dataclass
class Certificates:
    # Primal feasibility, checked against ALL constraint rows
    # (including redundant DAG rows and fixed-count rows).
    max_eq_residual: Fraction          # max |C x - d|
    min_primal_value: object           # min_i x_i (only when nonneg requested)
    # KKT residuals for  min 1/2 (x-y)' W (x-y)  s.t. C x = d, x >= 0
    max_stationarity: Fraction         # max |W(x-y) + C'lam - mu|
    max_complementary: Fraction        # max |mu_i * x_i|
    min_dual: object                   # min_i mu_i (only when nonneg requested)
    verified: bool

    def __str__(self):
        parts = [
            f"verified={self.verified}",
            f"max|Cx-d|={self.max_eq_residual}",
            f"max|stationarity|={self.max_stationarity}",
            f"max|mu*x|={self.max_complementary}",
        ]
        if self.min_primal_value is not None:
            parts.append(f"min x={self.min_primal_value}")
        if self.min_dual is not None:
            parts.append(f"min mu={self.min_dual}")
        return " ".join(parts)


def build_certificates(problem, full_rows, full_rhs, reduced_rows, x, lam, mu):
    """Assemble and check the certificate for a candidate KKT point.

    - full_rows/full_rhs: every constraint row (redundant included) for
      the primal-feasibility check.
    - reduced_rows/lam: independent rows actually used in the KKT
      system, with their Lagrange multipliers.
    - mu: bound multipliers for x >= 0 (all zero when nonneg is False).
    """
    n = problem.dag.n_nodes
    eq_residuals = [
        abs(sum(row[i] * x[i] for i in range(n)) - value)
        for row, value in zip(full_rows, full_rhs)
    ]
    max_eq = max(eq_residuals, default=Fraction(0))
    stationarity = []
    for i in range(n):
        s = problem.weights[i] * (x[i] - problem.observed[i]) - mu[i]
        for r, row in enumerate(reduced_rows):
            if row[i]:
                s += lam[r] * row[i]
        stationarity.append(abs(s))
    max_station = max(stationarity, default=Fraction(0))
    max_comp = max((abs(mu[i] * x[i]) for i in range(n)), default=Fraction(0))
    min_x = min(x) if problem.nonneg else None
    min_mu = min(mu) if problem.nonneg else None
    verified = (
        max_eq == 0
        and max_station == 0
        and max_comp == 0
        and (min_x is None or min_x >= 0)
        and (min_mu is None or min_mu >= 0)
    )
    return Certificates(max_eq, min_x, max_station, max_comp, min_mu, verified)
