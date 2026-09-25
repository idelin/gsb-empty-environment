"""Autoregressive least-squares fit with ill-conditioning degradation.

The fit only ever sees confirmed (originally observed) samples supplied
by the caller; it never fabricates history. When the normal equations
are (near-)singular the order is reduced step by step and every rejected
attempt is recorded as evidence.
"""

import math
from dataclasses import dataclass, field

from .constants import CONDITION_TOLERANCE


class IllConditionedError(Exception):
    def __init__(self, order: int, pivot_ratio: float):
        self.order = order
        self.pivot_ratio = pivot_ratio
        super().__init__(
            f"AR({order}) normal equations ill-conditioned "
            f"(pivot ratio {pivot_ratio:.3e})"
        )


class InsufficientDataError(Exception):
    def __init__(self, order: int, n: int):
        self.order = order
        self.n = n
        super().__init__(
            f"AR({order}) needs at least {2 * order + 2} samples, got {n}"
        )


@dataclass
class ARFit:
    order: int
    intercept: float
    coeffs: list
    sigma2: float
    evidence: dict = field(default_factory=dict)


def _solve_normal_equations(matrix, rhs, order):
    size = len(matrix)
    aug = [row[:] + [rhs[i]] for i, row in enumerate(matrix)]
    pivots = []
    for col in range(size):
        pivot_row = max(range(col, size), key=lambda r: abs(aug[r][col]))
        pivot = abs(aug[pivot_row][col])
        pivots.append(pivot)
        if pivot == 0.0:
            raise IllConditionedError(order, 0.0)
        aug[col], aug[pivot_row] = aug[pivot_row], aug[col]
        for row in range(col + 1, size):
            factor = aug[row][col] / aug[col][col]
            for k in range(col, size + 1):
                aug[row][k] -= factor * aug[col][k]
    solution = [0.0] * size
    for row in range(size - 1, -1, -1):
        residual = aug[row][size] - sum(
            aug[row][k] * solution[k] for k in range(row + 1, size)
        )
        solution[row] = residual / aug[row][row]
    ratio = min(pivots) / max(pivots)
    if ratio < CONDITION_TOLERANCE:
        raise IllConditionedError(order, ratio)
    return solution, ratio


def fit_ar(values, order: int) -> ARFit:
    n = len(values)
    if n - order < order + 2:
        raise InsufficientDataError(order, n)
    dim = order + 1
    ata = [[0.0] * dim for _ in range(dim)]
    atb = [0.0] * dim
    for t in range(order, n):
        feats = [1.0] + [values[t - k] for k in range(1, order + 1)]
        for i in range(dim):
            atb[i] += feats[i] * values[t]
            for j in range(dim):
                ata[i][j] += feats[i] * feats[j]
    solution, ratio = _solve_normal_equations(ata, atb, order)
    intercept, coeffs = solution[0], solution[1:]
    rss = 0.0
    for t in range(order, n):
        pred = intercept + sum(
            coeffs[k - 1] * values[t - k] for k in range(1, order + 1)
        )
        rss += (values[t] - pred) ** 2
    dof = max(1, n - order - dim)
    return ARFit(order, intercept, coeffs, rss / dof,
                 {"pivot_ratio": ratio, "samples": n})


def fit_ar_degrading(values, max_order: int) -> ARFit:
    """Fit AR, lowering the order on ill-conditioning; mean as last resort."""
    evidence = {"attempts": [], "degraded": False}
    order = min(max_order, len(values) - 3)
    while order >= 1:
        try:
            fit = fit_ar(values, order)
        except (IllConditionedError, InsufficientDataError) as exc:
            evidence["attempts"].append(
                {"order": order, "status": "rejected", "reason": str(exc)}
            )
            order -= 1
            continue
        evidence["attempts"].append(
            {"order": order, "status": "accepted",
             "pivot_ratio": fit.evidence["pivot_ratio"]}
        )
        evidence["degraded"] = order < max_order
        fit.evidence.update(evidence)
        return fit
    mean = sum(values) / len(values)
    var = sum((v - mean) ** 2 for v in values) / max(1, len(values) - 1)
    evidence["attempts"].append({"order": 0, "status": "fallback_mean"})
    evidence["degraded"] = True
    return ARFit(0, mean, [], var, evidence)


def forecast(fit: ARFit, history, steps: int):
    """Iterative forecast; variance grows via MA(inf) psi-weights."""
    buf = list(history[-fit.order:]) if fit.order else []
    preds, variances = [], []
    psi = [1.0]
    cum = 0.0
    for h in range(steps):
        pred = fit.intercept
        for k in range(fit.order):
            pred += fit.coeffs[k] * buf[-1 - k]
        preds.append(pred)
        buf.append(pred)
        if h >= 1:
            psi_h = 0.0
            for i in range(1, min(h, fit.order) + 1):
                psi_h += fit.coeffs[i - 1] * psi[h - i]
            psi.append(psi_h)
        cum += psi[h] * psi[h]
        variances.append(fit.sigma2 * cum)
    return preds, variances
