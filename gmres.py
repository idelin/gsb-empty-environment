"""Restarted GMRES(m) for real linear systems A x = b.

Pure Python 3.11+, standard library only (no NumPy/SciPy).

Design notes
------------
* Dimensions are explicit: the caller passes ``n`` and every vector
  (``b``, ``x0``, matvec output) is validated against it.  Error
  messages name the exact index and the exact location (restart cycle /
  Arnoldi step) where a problem was detected.
* The matrix is only accessed through a ``matvec(x) -> sequence``
  callback, so A can be dense, sparse, or fully matrix-free.
* Working memory is bounded by the restart length ``m``: at most
  ``m + 1`` Krylov basis vectors of length ``n`` plus an
  ``(m + 1) x m`` Hessenberg matrix are alive at any moment.
* Convergence is never faked: a claimed convergence is verified against
  the true residual ``||b - A x||`` before being reported, and an
  exhausted iteration budget reports ``converged=False`` with reason
  ``"budget_exhausted"``.
"""

from __future__ import annotations

import math
from dataclasses import dataclass, field
from typing import Callable, List, Optional, Sequence

__all__ = [
    "GmresResult",
    "GmresError",
    "MatvecShapeError",
    "NonFiniteError",
    "gmres",
]


class GmresError(Exception):
    """Base class for GMRES input/callback errors."""


class MatvecShapeError(GmresError):
    """A vector has the wrong length (message carries expected/actual)."""


class NonFiniteError(GmresError):
    """A NaN or infinite value was detected (message carries its index)."""


@dataclass
class GmresResult:
    """Outcome of :func:`gmres`.

    ``reason`` is one of:
    ``"zero_rhs"``          -- b == 0, x0 returned without any matvec;
    ``"converged"``         -- true residual <= tol * ||b||;
    ``"happy_breakdown"``   -- Arnoldi subspace became A-invariant (exact);
    ``"stagnation"``        -- a full restart cycle made no real progress;
    ``"budget_exhausted"``  -- maxiter reached without convergence.
    """

    x: List[float]
    converged: bool
    reason: str
    iterations: int            # total Arnoldi steps across all cycles
    restart_cycles: int        # restarts actually performed (0 = solved in 1st cycle)
    residual_history: List[float]   # Arnoldi (Givens) residual estimate per inner step
    cycle_residuals: List[float]    # residual estimate at the end of each cycle
    true_residual: Optional[float]  # ||b - A x|| at return (None for zero_rhs)
    matvec_calls: int
    peak_krylov_vectors: int        # max live basis vectors (<= restart + 1)


def _norm(v: Sequence[float]) -> float:
    return math.sqrt(math.fsum(t * t for t in v))


def _dot(u: Sequence[float], v: Sequence[float]) -> float:
    return math.fsum(ui * vi for ui, vi in zip(u, v))


def _as_finite_vector(name: str, values: Sequence[float], n: int) -> List[float]:
    try:
        seq = list(values)
    except TypeError as exc:
        raise GmresError(f"{name} is not a sequence: {exc}") from exc
    if len(seq) != n:
        raise MatvecShapeError(
            f"{name} has length {len(seq)}, expected n={n}"
        )
    out = []
    for i, value in enumerate(seq):
        try:
            fv = float(value)
        except (TypeError, ValueError) as exc:
            raise GmresError(
                f"{name}[{i}] = {value!r} is not a real number"
            ) from exc
        if not math.isfinite(fv):
            raise NonFiniteError(
                f"{name}[{i}] = {value!r} is not finite (NaN or inf)"
            )
        out.append(fv)
    return out


class _CheckedMatvec:
    """Wraps the user callback; validates size and finiteness per call."""

    def __init__(self, func: Callable[[List[float]], Sequence[float]], n: int):
        self._func = func
        self._n = n
        self.calls = 0

    def __call__(self, x: List[float], where: str) -> List[float]:
        y = self._func(list(x))  # pass a copy: callback cannot mutate our state
        self.calls += 1
        try:
            seq = list(y)
        except TypeError as exc:
            raise MatvecShapeError(
                f"matvec returned a non-sequence ({type(y).__name__}) at {where}; "
                f"expected a sequence of length {self._n}"
            ) from exc
        if len(seq) != self._n:
            raise MatvecShapeError(
                f"matvec returned length {len(seq)} at {where}; "
                f"expected n={self._n}"
            )
        out = []
        for i, value in enumerate(seq):
            try:
                fv = float(value)
            except (TypeError, ValueError) as exc:
                raise GmresError(
                    f"matvec output y[{i}] = {value!r} is not a real number "
                    f"at {where}"
                ) from exc
            if not math.isfinite(fv):
                raise NonFiniteError(
                    f"matvec output y[{i}] = {value!r} is not finite at {where}"
                )
            out.append(fv)
        return out


def _gmres_cycle(mv, b, x, n, m, tol_abs, budget, cycle_index, residual_history):
    """Run one GMRES(m) cycle.

    Returns ``(x, residual_estimate, status, steps_used, peak_vectors)``
    with ``status`` in {"converged", "happy_breakdown", "cycle_done",
    "stagnation"}.
    """
    ax = mv(x, f"restart cycle {cycle_index}, residual evaluation")
    r = [bi - ai for bi, ai in zip(b, ax)]
    beta = _norm(r)
    if beta <= tol_abs:
        return x, beta, "converged", 0, 1

    V = [[ri / beta for ri in r]]
    H = [[0.0] * m for _ in range(m + 1)]
    cs = [0.0] * m
    sn = [0.0] * m
    g = [0.0] * (m + 1)
    g[0] = beta
    peak = 1
    steps = 0
    k = 0  # Arnoldi steps with a usable triangular factor
    status = "cycle_done"

    for j in range(min(m, budget)):
        w = mv(V[j], f"restart cycle {cycle_index}, Arnoldi step {j}")
        # modified Gram-Schmidt, two passes for numerical robustness
        for _ in range(2):
            for i in range(j + 1):
                hij = _dot(w, V[i])
                H[i][j] += hij
                Vi = V[i]
                w = [wk - hij * vk for wk, vk in zip(w, Vi)]
        H[j + 1][j] = _norm(w)
        if H[j + 1][j] > 0.0:
            V.append([wk / H[j + 1][j] for wk in w])
            if len(V) > peak:
                peak = len(V)

        # apply previous Givens rotations to column j
        for i in range(j):
            c, s = cs[i], sn[i]
            hij, hi1j = H[i][j], H[i + 1][j]
            H[i][j] = c * hij + s * hi1j
            H[i + 1][j] = c * hi1j - s * hij

        # new Givens rotation to eliminate H[j+1][j]
        a, sub = H[j][j], H[j + 1][j]
        r_norm = math.hypot(a, sub)
        if r_norm == 0.0:
            # A-invariant Krylov subspace with a singular Hessenberg
            # matrix: no further progress is possible in this subspace.
            status = "stagnation"
            steps = j + 1
            break
        c, s = a / r_norm, sub / r_norm
        cs[j], sn[j] = c, s
        H[j][j] = r_norm
        H[j + 1][j] = 0.0
        gj, gj1 = g[j], g[j + 1]
        g[j] = c * gj + s * gj1
        g[j + 1] = c * gj1 - s * gj

        steps = j + 1
        k = j + 1
        estimate = abs(g[j + 1])
        residual_history.append(estimate)
        if estimate <= tol_abs:
            status = "happy_breakdown" if sub == 0.0 else "converged"
            break

    # solve the k x k upper triangular system R y = g[0:k]
    if k > 0:
        y: Optional[List[float]] = [0.0] * k
        for i in range(k - 1, -1, -1):
            diag = H[i][i]
            if diag == 0.0:
                y = None  # singular R: cannot trust this correction
                break
            acc = g[i] - math.fsum(H[i][t] * y[t] for t in range(i + 1, k))
            y[i] = acc / diag
        if y is not None:
            for i in range(k):
                yi = y[i]
                if yi != 0.0:
                    Vi = V[i]
                    x = [xj + yi * vij for xj, vij in zip(x, Vi)]

    estimate = abs(g[k]) if k > 0 else beta
    return x, estimate, status, steps, peak


def gmres(
    matvec: Callable[[List[float]], Sequence[float]],
    b: Sequence[float],
    *,
    n: int,
    x0: Optional[Sequence[float]] = None,
    restart: int = 30,
    tol: float = 1e-8,
    maxiter: Optional[int] = None,
    stagnation_rtol: float = 1e-8,
) -> GmresResult:
    """Solve A x = b with restarted GMRES(m), m = min(restart, n).

    Parameters
    ----------
    matvec : callback computing A @ x; receives a list of length ``n``
        and must return a finite sequence of length ``n``.
    b : right-hand side, length ``n``.
    n : problem dimension (explicit, required).
    x0 : initial guess (default: zero vector).
    restart : maximum Arnoldi steps per cycle; bounds working memory to
        ``restart + 1`` vectors of length ``n``.
    tol : relative tolerance on the true residual, ``||b - A x|| <=
        tol * ||b||``.
    maxiter : total Arnoldi-step budget across all restarts
        (default: ``10 * restart``).  Exhaustion is reported as
        ``converged=False, reason="budget_exhausted"``.
    stagnation_rtol : a restart cycle whose relative residual
        improvement is below this threshold is declared stagnated.
    """
    if not callable(matvec):
        raise GmresError("matvec must be callable")
    if isinstance(n, bool) or not isinstance(n, int) or n < 1:
        raise GmresError(f"n must be a positive integer, got {n!r}")
    if isinstance(restart, bool) or not isinstance(restart, int) or restart < 1:
        raise GmresError(f"restart must be a positive integer, got {restart!r}")
    if not (isinstance(tol, (int, float)) and math.isfinite(tol) and tol > 0):
        raise GmresError(f"tol must be a positive finite number, got {tol!r}")
    if not (isinstance(stagnation_rtol, (int, float)) and stagnation_rtol >= 0):
        raise GmresError(
            f"stagnation_rtol must be a non-negative number, got {stagnation_rtol!r}"
        )

    b_vec = _as_finite_vector("b", b, n)
    x = _as_finite_vector("x0", x0, n) if x0 is not None else [0.0] * n
    m = min(restart, n)

    if maxiter is None:
        maxiter = 10 * m
    if isinstance(maxiter, bool) or not isinstance(maxiter, int) or maxiter < 1:
        raise GmresError(f"maxiter must be a positive integer, got {maxiter!r}")

    bnorm = _norm(b_vec)
    if bnorm == 0.0:
        # Zero right-hand side: return x0 immediately, no matvec calls.
        return GmresResult(
            x=x,
            converged=True,
            reason="zero_rhs",
            iterations=0,
            restart_cycles=0,
            residual_history=[],
            cycle_residuals=[],
            true_residual=None,
            matvec_calls=0,
            peak_krylov_vectors=0,
        )

    tol_abs = tol * bnorm
    mv = _CheckedMatvec(matvec, n)
    residual_history: List[float] = []
    cycle_residuals: List[float] = []
    iterations = 0
    restart_cycles = 0
    peak = 0
    converged = False
    reason = "budget_exhausted"
    true_residual: Optional[float] = None
    cycle_index = 0

    while True:
        budget = maxiter - iterations
        if budget <= 0:
            reason = "budget_exhausted"
            break
        x, estimate, status, used, peak_c = _gmres_cycle(
            mv, b_vec, x, n, m, tol_abs, budget, cycle_index, residual_history
        )
        iterations += used
        if peak_c > peak:
            peak = peak_c

        if status in ("converged", "happy_breakdown"):
            # Verify against the true residual before claiming success.
            ax = mv(x, "final residual verification")
            true_residual = _norm([bi - ai for bi, ai in zip(b_vec, ax)])
            if true_residual <= tol_abs:
                converged = True
                reason = status
                break
            # Estimate was over-optimistic; treat as an ordinary cycle end.
            status = "cycle_done"
            estimate = true_residual

        if status == "stagnation":
            reason = "stagnation"
            break

        cycle_residuals.append(estimate)
        if len(cycle_residuals) >= 2:
            prev, cur = cycle_residuals[-2], cycle_residuals[-1]
            if prev - cur <= stagnation_rtol * prev:
                reason = "stagnation"
                break
        if iterations >= maxiter:
            reason = "budget_exhausted"
            break
        restart_cycles += 1
        cycle_index += 1

    if true_residual is None:
        ax = mv(x, "final residual evaluation")
        true_residual = _norm([bi - ai for bi, ai in zip(b_vec, ax)])

    return GmresResult(
        x=x,
        converged=converged,
        reason=reason,
        iterations=iterations,
        restart_cycles=restart_cycles,
        residual_history=residual_history,
        cycle_residuals=cycle_residuals,
        true_residual=true_residual,
        matvec_calls=mv.calls,
        peak_krylov_vectors=peak,
    )
