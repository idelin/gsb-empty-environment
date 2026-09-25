"""Iterative solvers with residual-based convergence diagnostics.

Convergence is declared only from the residual, never from a small
step increment: a slowly converging iteration can produce tiny
increments while the residual (and the error) is still large.
"""

from dataclasses import dataclass, field


@dataclass
class IterativeResult:
    x: list
    residuals: list
    increments: list
    converged: bool
    iterations: int
    error_bound: object
    warnings: list = field(default_factory=list)


@dataclass
class PowerResult:
    distribution: list
    residuals: list
    mass_errors: list
    converged: bool
    iterations: int


def assess_convergence(residual_norm, increment_norm, tol):
    """Return (converged, warning).  The residual alone decides."""
    if residual_norm <= tol:
        return True, None
    if increment_norm <= tol:
        return False, (
            "increment below tolerance but residual %.3e exceeds tol=%.1e: "
            "small change is not a convergence proof"
            % (residual_norm, tol))
    return False, None


def _dominance_margin(a):
    """Row diagonal-dominance margin; >0 bounds ||A^-1||_inf by 1/margin."""
    margin = None
    for i, row in enumerate(a):
        off = sum(abs(v) for j, v in enumerate(row) if j != i)
        m = abs(row[i]) - off
        margin = m if margin is None else min(margin, m)
    return margin


def gauss_seidel(a, b, x0=None, tol=1e-10, max_iter=100000):
    """Gauss-Seidel for A x = b with residual and reliable error bound.

    When A is strictly row diagonally dominant with margin d, the true
    error satisfies ||x - x*||_inf <= ||r||_inf / d; otherwise the bound
    is reported as None and only the residual is trustworthy.
    """
    n = len(a)
    x = [0.0] * n if x0 is None else list(x0)
    residuals, increments, warnings = [], [], []
    converged = False
    for _ in range(max_iter):
        old = list(x)
        for i in range(n):
            s = sum(a[i][j] * x[j] for j in range(n) if j != i)
            x[i] = (b[i] - s) / a[i][i]
        rnorm = max(abs(b[i] - sum(a[i][j] * x[j] for j in range(n)))
                    for i in range(n))
        inorm = max(abs(x[i] - old[i]) for i in range(n))
        residuals.append(rnorm)
        increments.append(inorm)
        converged, warning = assess_convergence(rnorm, inorm, tol)
        if warning and warning not in warnings:
            warnings.append(warning)
        if converged:
            break
    margin = _dominance_margin(a)
    error_bound = None
    if margin > 0 and residuals:
        error_bound = residuals[-1] / margin
    return IterativeResult(x, residuals, increments, converged,
                           len(residuals), error_bound, warnings)


def multiply_distribution(out, dist):
    """One propagation step: new[j] = sum_i dist[i] * P(i -> j)."""
    new = [0.0] * len(dist)
    for i, p in enumerate(dist):
        if p:
            for j, q in out[i].items():
                new[j] += p * q
    return new


def power_iteration(out, dist0, tol=1e-12, max_iter=100000):
    """Stationary-distribution iteration with residual diagnostics.

    The reported residual is the true stationarity defect ||x P - x||_1,
    and the mass conservation error sum(x P) - sum(x) is tracked at
    every step.  Convergence is declared only when the residual falls
    below tol, never from the increment between iterates alone.
    """
    dist = list(dist0)
    residuals, mass_errors = [], []
    converged = False
    iterations = 0
    for _ in range(max_iter):
        new = multiply_distribution(out, dist)
        mass_errors.append(sum(new) - sum(dist))
        residual = sum(abs(new[i] - dist[i]) for i in range(len(dist)))
        residuals.append(residual)
        dist = new
        iterations += 1
        if residual <= tol:
            converged = True
            break
    return PowerResult(dist, residuals, mass_errors, converged, iterations)
