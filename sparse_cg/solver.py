"""Preconditioned conjugate gradient with trust in the *true* residual only.

Convergence contract (fixed semantics)
--------------------------------------
Let r_true = b - A x be the explicitly recomputed residual and
bnorm = ||b||_2.  The iterate is declared converged iff

    ||r_true||_2 <= max(atol, rtol * bnorm)

The recursively updated residual is used only as a cheap hint that tells
the solver *when* to spend a matvec on the true residual; it can never
declare convergence by itself.  The true residual is also recomputed
every ``recompute_interval`` iterations, and stagnation is judged from
the recorded history of true-residual norms.

Memory: a fixed number of O(n) work vectors; nothing grows with the
iteration count (no Krylov basis is stored).
"""

from __future__ import annotations

import math
from dataclasses import dataclass, field
from enum import Enum

from .matrix import SparseMatrix
from .preconditioner import DiagonalPreconditioner


class CGStatus(Enum):
    CONVERGED = "converged"
    MAX_ITERATIONS = "max_iterations"
    STAGNATED = "stagnated"
    NOT_POSITIVE_DEFINITE = "not_positive_definite"
    ASYMMETRIC_INPUT = "asymmetric_input"
    INVALID_PRECONDITIONER = "invalid_preconditioner"
    MATVEC_FAILURE = "matvec_failure"
    CANCELLED = "cancelled"


class VersionMismatchError(RuntimeError):
    """Checkpoint belongs to a different matrix version."""

    def __init__(self, message, evidence):
        super().__init__(message)
        self.evidence = evidence


class _MatvecFailed(Exception):
    def __init__(self, original):
        super().__init__(str(original))
        self.original = original


@dataclass
class Checkpoint:
    """Restart state.  Only x and scalars are stored: search directions
    are never checkpointed, so a restore can never smuggle stale
    Krylov directions into a changed operator."""
    x: list
    iteration: int
    matrix_version: int
    true_residual_norm: float
    rhs_norm: float


@dataclass
class CGResult:
    status: CGStatus
    x: list
    iterations: int
    true_residual_norm: float
    recurrence_residual_norm: float
    initial_residual_norm: float
    rhs_norm: float
    tolerance: float
    matrix_version: int
    hot_start: bool
    residual_history: list = field(default_factory=list)
    premature_convergence_events: list = field(default_factory=list)
    evidence: dict = field(default_factory=dict)

    @property
    def converged(self):
        return self.status is CGStatus.CONVERGED


def _dot(u, v):
    return math.fsum(ui * vi for ui, vi in zip(u, v))


def _norm(u):
    return math.sqrt(_dot(u, u))


class CGSolver:
    def __init__(self, rtol=1e-8, atol=1e-12, max_iter=None,
                 recompute_interval=25, stagnation_window=4,
                 stagnation_min_reduction=0.5,
                 check_symmetry=False, symmetry_rtol=1e-10):
        if rtol < 0 or atol < 0:
            raise ValueError("tolerances must be non-negative")
        if recompute_interval < 1:
            raise ValueError("recompute_interval must be >= 1")
        self.rtol = rtol
        self.atol = atol
        self.max_iter = max_iter
        self.recompute_interval = recompute_interval
        self.stagnation_window = stagnation_window
        self.stagnation_min_reduction = stagnation_min_reduction
        self.check_symmetry = check_symmetry
        self.symmetry_rtol = symmetry_rtol
        self._cancelled = False
        self._last_matrix_version = None
        self._last_state = None  # for save_checkpoint()

    # ------------------------------------------------------------------ API

    def cancel(self):
        """Request cancellation; the running solve stops at the next iterate."""
        self._cancelled = True

    def save_checkpoint(self):
        if self._last_state is None:
            raise RuntimeError("no solve has run yet; nothing to checkpoint")
        return Checkpoint(**self._last_state)

    def restore_checkpoint(self, matrix, checkpoint, *, allow_matrix_change=False):
        """Return x to warm-start from.  Refuses to restore across a matrix
        version change unless the caller explicitly accepts a hot start
        (x only; Krylov state is always rebuilt from scratch)."""
        if checkpoint.matrix_version != matrix.version and not allow_matrix_change:
            raise VersionMismatchError(
                "checkpoint was saved for matrix version "
                f"{checkpoint.matrix_version} but the matrix is now version "
                f"{matrix.version}; refusing to reuse state saved under a "
                "different operator",
                evidence={
                    "checkpoint_matrix_version": checkpoint.matrix_version,
                    "current_matrix_version": matrix.version,
                    "checkpoint_iteration": checkpoint.iteration,
                    "checkpoint_true_residual_norm": checkpoint.true_residual_norm,
                },
            )
        return list(checkpoint.x)

    def solve(self, A, b, x0=None, precond=None, matvec=None, callback=None):
        """Run PCG.  Every call fully re-initialises the Krylov state
        (r, z, p) from the supplied x0; only x itself is ever reused."""
        self._cancelled = False
        if not isinstance(A, SparseMatrix):
            raise TypeError("A must be a SparseMatrix")
        if A.n_rows != A.n_cols:
            raise ValueError(f"CG needs a square matrix, got {A.n_rows}x{A.n_cols}")
        n = A.n_rows
        if len(b) != n:
            raise ValueError(f"rhs length {len(b)} != matrix order {n}")
        x = [float(v) for v in x0] if x0 is not None else [0.0] * n
        if len(x) != n:
            raise ValueError(f"x0 length {len(x)} != matrix order {n}")
        mv = matvec if matvec is not None else A.matvec
        max_iter = self.max_iter if self.max_iter is not None else 10 * n

        def true_residual(xv):
            ax = mv(xv)
            if len(ax) != n:
                raise _MatvecFailed(
                    ValueError(f"matvec returned length {len(ax)}, expected {n}"))
            return [bi - ai for bi, ai in zip(b, ax)]

        def safe_mv(v):
            try:
                out = mv(v)
            except _MatvecFailed:
                raise
            except Exception as exc:  # user matvec blew up
                raise _MatvecFailed(exc) from exc
            return out

        bnorm = _norm(b)
        tol = max(self.atol, self.rtol * bnorm)
        hot_start = (self._last_matrix_version is not None
                     and self._last_matrix_version != A.version)

        def finish(status, it, r_true_norm, r_rec_norm, history,
                   premature, evidence, initial_norm):
            self._last_matrix_version = A.version
            self._last_state = {
                "x": list(x),
                "iteration": it,
                "matrix_version": A.version,
                "true_residual_norm": r_true_norm,
                "rhs_norm": bnorm,
            }
            return CGResult(
                status=status, x=list(x), iterations=it,
                true_residual_norm=r_true_norm,
                recurrence_residual_norm=r_rec_norm,
                initial_residual_norm=initial_norm,
                rhs_norm=bnorm, tolerance=tol,
                matrix_version=A.version, hot_start=hot_start,
                residual_history=history,
                premature_convergence_events=premature,
                evidence=evidence,
            )

        # --- structural checks, each with concrete numerical evidence ------
        if self.check_symmetry:
            defect, i, j, aij, aji, scale = A.symmetry_defect()
            if defect > self.symmetry_rtol * max(scale, 1.0):
                return finish(
                    CGStatus.ASYMMETRIC_INPUT, 0, math.inf, math.inf, [], [],
                    {"reason": "matrix is not symmetric",
                     "max_defect": defect, "row": i, "col": j,
                     "a_ij": aij, "a_ji": aji, "entry_scale": scale,
                     "allowed": self.symmetry_rtol * max(scale, 1.0)},
                    math.inf)

        if precond is None:
            precond = DiagonalPreconditioner.from_matrix(A)
        if precond.size != n:
            raise ValueError(
                f"preconditioner size {precond.size} != matrix order {n}")
        zero_idx = precond.zero_entry_index()
        if zero_idx is not None:
            return finish(
                CGStatus.INVALID_PRECONDITIONER, 0, math.inf, math.inf, [], [],
                {"reason": "zero diagonal entry in preconditioner",
                 "zero_diagonal_index": zero_idx},
                math.inf)

        # --- Krylov state initialisation (always from scratch) -------------
        try:
            r = true_residual(x)
        except _MatvecFailed as exc:
            return finish(CGStatus.MATVEC_FAILURE, 0, math.inf, math.inf,
                          [], [], {"reason": "matvec raised during initial "
                                             "residual evaluation",
                                   "exception": repr(exc.original)}, math.inf)
        r_true_norm = _norm(r)
        initial_norm = r_true_norm
        history = [r_true_norm]
        premature = []
        if r_true_norm <= tol:
            return finish(CGStatus.CONVERGED, 0, r_true_norm, r_true_norm,
                          history, premature, {}, initial_norm)

        z = precond.apply(r)
        rz = _dot(r, z)
        if rz <= 0.0:
            return finish(
                CGStatus.INVALID_PRECONDITIONER, 0, r_true_norm, r_true_norm,
                history, premature,
                {"reason": "preconditioner is not positive definite: r'z <= 0",
                 "r_dot_z": rz}, initial_norm)
        p = list(z)
        r_rec_norm = r_true_norm

        # --- main loop ------------------------------------------------------
        it = 0
        while it < max_iter:
            it += 1
            try:
                Ap = safe_mv(p)
            except _MatvecFailed as exc:
                return finish(CGStatus.MATVEC_FAILURE, it, r_true_norm,
                              r_rec_norm, history, premature,
                              {"reason": "matvec raised", "iteration": it,
                               "exception": repr(exc.original)}, initial_norm)
            pAp = _dot(p, Ap)
            if pAp <= 0.0:
                return finish(
                    CGStatus.NOT_POSITIVE_DEFINITE, it, r_true_norm,
                    r_rec_norm, history, premature,
                    {"reason": "p'Ap <= 0: operator lost positive "
                               "definiteness along the search direction",
                     "iteration": it, "pAp": pAp, "p_norm": _norm(p)},
                    initial_norm)
            alpha = rz / pAp
            for k in range(n):
                x[k] += alpha * p[k]
                r[k] -= alpha * Ap[k]
            r_rec_norm = _norm(r)

            if self._cancelled or (callback is not None
                                   and callback(it, x, r_rec_norm) is False):
                try:
                    r = true_residual(x)
                    r_true_norm = _norm(r)
                except _MatvecFailed:
                    pass
                return finish(CGStatus.CANCELLED, it, r_true_norm, r_rec_norm,
                              history, premature,
                              {"reason": "cancelled", "iteration": it},
                              initial_norm)

            # Decide when the true residual must be recomputed: periodically,
            # and always when the recurrence residual claims convergence.
            rec_claims_convergence = r_rec_norm <= tol
            recomputed = False
            if it % self.recompute_interval == 0 or rec_claims_convergence:
                try:
                    r = true_residual(x)
                except _MatvecFailed as exc:
                    return finish(CGStatus.MATVEC_FAILURE, it, r_true_norm,
                                  r_rec_norm, history, premature,
                                  {"reason": "matvec raised during true "
                                             "residual recomputation",
                                   "iteration": it,
                                   "exception": repr(exc.original)},
                                  initial_norm)
                r_true_norm = _norm(r)
                history.append(r_true_norm)
                recomputed = True
                if rec_claims_convergence and r_true_norm > tol:
                    premature.append({
                        "iteration": it,
                        "recurrence_residual_norm": r_rec_norm,
                        "true_residual_norm": r_true_norm,
                        "tolerance": tol,
                    })
                r_rec_norm = r_true_norm
                # stagnation: judge only by true-residual history
                w = self.stagnation_window
                if len(history) > w and r_true_norm > (
                        history[-1 - w] * self.stagnation_min_reduction):
                    return finish(
                        CGStatus.STAGNATED, it, r_true_norm, r_rec_norm,
                        history, premature,
                        {"reason": "true residual failed to decrease by the "
                                   "required factor over the stagnation "
                                   "window",
                         "iteration": it,
                         "window": w,
                         "min_reduction": self.stagnation_min_reduction,
                         "norm_then": history[-1 - w],
                         "norm_now": r_true_norm,
                         "history": list(history)}, initial_norm)

            if r_true_norm <= tol:
                # Convergence is declared ONLY on the true residual.
                return finish(CGStatus.CONVERGED, it, r_true_norm,
                              r_rec_norm, history, premature, {},
                              initial_norm)

            z = precond.apply(r)
            rz_new = _dot(r, z)
            if rz_new <= 0.0:
                return finish(
                    CGStatus.INVALID_PRECONDITIONER, it, r_true_norm,
                    r_rec_norm, history, premature,
                    {"reason": "preconditioner is not positive definite: "
                               "r'z <= 0", "iteration": it,
                     "r_dot_z": rz_new}, initial_norm)
            if recomputed:
                # The residual was just replaced by its true value, so the
                # old search direction is no longer A-conjugate to it.
                # Soft-restart the Krylov recurrence instead of propagating
                # a corrupted direction.
                p = list(z)
            else:
                beta = rz_new / rz
                for k in range(n):
                    p[k] = z[k] + beta * p[k]
            rz = rz_new

        return finish(CGStatus.MAX_ITERATIONS, it, r_true_norm, r_rec_norm,
                      history, premature,
                      {"reason": "iteration budget exhausted; this is NOT a "
                                 "proof that no solution exists",
                       "max_iter": max_iter}, initial_norm)
