"""Preconditioned conjugate gradients with honest termination criteria.

Fixed conventions
-----------------
* Residual: the 2-norm of ``r = b - A x``.
* Tolerance: converged iff ``||b - A x||_2 <= max(atol, rtol * ||b||_2)``.
* The *true* residual (explicitly recomputed ``b - A x``) is the only
  quantity allowed to declare convergence. The recursive residual kept by
  the CG recurrences is never sufficient; when it drops below the
  tolerance it merely triggers a true-residual recomputation.
* The true residual is recomputed every ``true_residual_period``
  iterations and drives the convergence and stagnation decisions. The
  recursive residual itself is left untouched: resetting the recursion to
  the true residual can destabilise the CG recurrence for ill-conditioned
  problems, so the true residual is used for *decisions*, not for
  rewriting the iteration state.

Working memory
--------------
O(n): a fixed set of vectors ``x, r, p, z, Ap`` plus the right-hand side.
No Krylov basis is stored, so vector memory does not grow with the
iteration count (only a scalar norm history is kept).

Matrix versions
---------------
``SparseMatrix.version`` identifies the matrix data. Checkpoints record
the version and :meth:`CGSolver.restore` refuses to resurrect search
directions for a different version. Warm starts after a matrix change are
done by passing the old solution as ``x0`` to :meth:`CGSolver.solve`,
which always reinitialises the Krylov state from scratch.
"""

import math

STATUS_CONVERGED = "converged"
STATUS_MAX_ITER = "max_iter"
STATUS_STAGNATED = "stagnated"
STATUS_INDEFINITE = "indefinite"
STATUS_INDEFINITE_PRECONDITIONER = "indefinite_preconditioner"
STATUS_ASYMMETRIC = "asymmetric"
STATUS_CANCELLED = "cancelled"


def _dot(a, b):
    return math.fsum(ai * bi for ai, bi in zip(a, b))


def _norm(a):
    return math.sqrt(math.fsum(ai * ai for ai in a))


class MatrixVersionError(RuntimeError):
    """Checkpoint belongs to a different matrix version than the one given."""

    def __init__(self, expected, found):
        self.expected = expected
        self.found = found
        super().__init__(
            f"checkpoint was saved for matrix version {expected}, but the "
            f"supplied matrix has version {found}; refusing to reuse stale "
            f"search directions"
        )


class CGResult:
    """Outcome of a solve. ``converged`` is True only for STATUS_CONVERGED."""

    __slots__ = (
        "status",
        "converged",
        "x",
        "iterations",
        "true_residual_norm",
        "recursive_residual_norm",
        "tolerance",
        "initial_residual_norm",
        "history",
        "evidence",
    )

    def __init__(self, status, x, iterations, true_residual_norm,
                 recursive_residual_norm, tolerance, initial_residual_norm,
                 history, evidence=None):
        self.status = status
        self.converged = status == STATUS_CONVERGED
        self.x = x
        self.iterations = iterations
        self.true_residual_norm = true_residual_norm
        self.recursive_residual_norm = recursive_residual_norm
        self.tolerance = tolerance
        self.initial_residual_norm = initial_residual_norm
        # history entries: (iteration, true_norm_or_None, recursive_norm)
        self.history = history
        self.evidence = evidence or {}

    def __repr__(self):
        return (
            f"CGResult(status={self.status!r}, iterations={self.iterations}, "
            f"true_residual_norm={self.true_residual_norm!r}, "
            f"tolerance={self.tolerance!r})"
        )


class CGSolver:
    """Preconditioned CG for SPD systems given as explicit sparse matrices."""

    #: O(n) working vectors; memory does not grow with the iteration count.
    WORKING_VECTORS = ("x", "r", "p", "z", "Ap")

    def __init__(self, matrix, preconditioner=None, *, rtol=1e-8, atol=1e-12,
                 true_residual_period=50, stagnation_window=8,
                 stagnation_factor=0.95, check_symmetry=False,
                 symmetry_tol=1e-10):
        from .preconditioner import DiagonalPreconditioner

        self.matrix = matrix
        self.preconditioner = (
            preconditioner
            if preconditioner is not None
            else DiagonalPreconditioner(matrix.n_rows)
        )
        self.rtol = float(rtol)
        self.atol = float(atol)
        self.true_residual_period = int(true_residual_period)
        self.stagnation_window = int(stagnation_window)
        self.stagnation_factor = float(stagnation_factor)
        self.check_symmetry = check_symmetry
        self.symmetry_tol = float(symmetry_tol)
        self._state = None
        self._cancel_requested = False

    # ------------------------------------------------------------------ API

    def solve(self, b, x0=None, max_iter=None, iteration_callback=None):
        """Solve ``A x = b``. Always reinitialises the Krylov state.

        ``x0`` may be a reused (warm-started) solution, e.g. from a previous
        matrix version; the residual and search direction are rebuilt from
        the *current* matrix. ``iteration_callback(k, recursive_norm)`` may
        return ``False`` to request cancellation at an iteration boundary.
        """
        from .matrix import DimensionError

        A = self.matrix
        if A.n_rows != A.n_cols:
            raise DimensionError("CG requires a square matrix")
        n = A.n_rows
        b = [float(v) for v in b]
        if len(b) != n:
            raise DimensionError(f"right-hand side has length {len(b)}, expected {n}")
        if x0 is None:
            x = [0.0] * n
        else:
            x = [float(v) for v in x0]
            if len(x) != n:
                raise DimensionError(f"initial guess has length {len(x)}, expected {n}")
        if max_iter is None:
            max_iter = 10 * n

        if self.check_symmetry:
            report = A.symmetry_evidence()
            if not report.square or report.max_abs_asymmetry > self.symmetry_tol:
                return CGResult(STATUS_ASYMMETRIC, x, 0, None, None, None, None,
                                [], evidence=report.to_dict())

        r = self._true_residual(b, x)
        b_norm = _norm(b)
        tol = max(self.atol, self.rtol * b_norm)
        r0 = _norm(r)
        history = [(0, r0, r0)]
        true_checks = [(0, r0)]
        if r0 <= tol:
            self._state = self._fresh_state(x, r, None, None, 0, b, b_norm,
                                            history, true_checks)
            return CGResult(STATUS_CONVERGED, list(x), 0, r0, r0, tol, r0, history)
        z = self.preconditioner.apply(r)
        rz = _dot(r, z)
        if rz <= 0.0:
            return CGResult(STATUS_INDEFINITE_PRECONDITIONER, x, 0, r0, r0, tol,
                            r0, history, evidence={"iteration": 0, "rz": rz})
        p = list(z)
        self._state = self._fresh_state(x, r, p, rz, 0, b, b_norm,
                                        history, true_checks)
        return self._run(self._state, max_iter, tol, iteration_callback)

    def resume(self, max_iter, iteration_callback=None):
        """Continue from the last committed state (e.g. after cancellation)."""
        if self._state is None:
            raise RuntimeError("no Krylov state to resume; call solve() first")
        st = self._state
        tol = max(self.atol, self.rtol * st["b_norm"])
        return self._run(st, max_iter, tol, iteration_callback)

    def request_cancel(self):
        """Ask the running solve to stop at the next iteration boundary."""
        self._cancel_requested = True

    @property
    def current_x(self):
        """Current iterate (copy), or None if no solve has started."""
        if self._state is None:
            return None
        return list(self._state["x"])

    def checkpoint(self):
        """JSON-serialisable snapshot of the O(n) Krylov state."""
        if self._state is None:
            raise RuntimeError("no state to checkpoint; call solve() first")
        st = self._state
        return {
            "matrix_version": self.matrix.version,
            "config": {
                "rtol": self.rtol,
                "atol": self.atol,
                "true_residual_period": self.true_residual_period,
                "stagnation_window": self.stagnation_window,
                "stagnation_factor": self.stagnation_factor,
            },
            "iteration": st["iteration"],
            "x": list(st["x"]),
            "r": list(st["r"]),
            "p": list(st["p"]),
            "rz": st["rz"],
            "b": list(st["b"]),
            "b_norm": st["b_norm"],
            "history": [list(h) for h in st["history"]],
            "true_checkpoints": [list(c) for c in st["true_checkpoints"]],
        }

    @classmethod
    def restore(cls, checkpoint, matrix, preconditioner=None):
        """Rebuild a solver from a checkpoint.

        Raises :class:`MatrixVersionError` if the checkpoint's matrix
        version differs from the supplied matrix, so stale search
        directions are never applied to changed data.
        """
        saved = checkpoint["matrix_version"]
        if saved != matrix.version:
            raise MatrixVersionError(expected=saved, found=matrix.version)
        solver = cls(matrix, preconditioner=preconditioner, **checkpoint["config"])
        solver._state = {
            "x": list(checkpoint["x"]),
            "r": list(checkpoint["r"]),
            "p": list(checkpoint["p"]),
            "rz": float(checkpoint["rz"]),
            "iteration": int(checkpoint["iteration"]),
            "b": list(checkpoint["b"]),
            "b_norm": float(checkpoint["b_norm"]),
            "history": [tuple(h) for h in checkpoint["history"]],
            "true_checkpoints": [tuple(c) for c in checkpoint["true_checkpoints"]],
        }
        return solver

    # ------------------------------------------------------------- internal

    @staticmethod
    def _fresh_state(x, r, p, rz, iteration, b, b_norm, history, true_checks):
        return {
            "x": x, "r": r, "p": p, "rz": rz, "iteration": iteration,
            "b": b, "b_norm": b_norm, "history": history,
            "true_checkpoints": true_checks,
        }

    @staticmethod
    def _commit(st, x, r, p, rz, k):
        st["x"] = x
        st["r"] = r
        st["p"] = p
        st["rz"] = rz
        st["iteration"] = k

    def _true_residual(self, b, x):
        ax = self.matrix.matvec(x)
        return [bi - ai for bi, ai in zip(b, ax)]

    def _run(self, st, max_iter, tol, iteration_callback):
        A = self.matrix
        apply_pre = self.preconditioner.apply
        b = st["b"]
        x, r, p, rz = st["x"], st["r"], st["p"], st["rz"]
        k = st["iteration"]
        history = st["history"]
        true_checks = st["true_checkpoints"]
        r0 = history[0][1]
        period = self.true_residual_period
        window = self.stagnation_window
        factor = self.stagnation_factor

        while k < max_iter:
            # Cancellation is only honoured at iteration boundaries, where
            # (x, r, p, rz) form a consistent, resumable Krylov state.
            if self._cancel_requested or (
                iteration_callback is not None
                and iteration_callback(k, _norm(r)) is False
            ):
                self._cancel_requested = False
                true_norm = _norm(self._true_residual(b, x))
                self._commit(st, x, r, p, rz, k)
                return CGResult(STATUS_CANCELLED, list(x), k, true_norm,
                                _norm(r), tol, r0, history,
                                evidence={"iteration": k})

            Ap = A.matvec(p)  # may raise; committed state stays consistent
            pAp = _dot(p, Ap)
            if pAp <= 0.0:
                self._commit(st, x, r, p, rz, k)
                return CGResult(
                    STATUS_INDEFINITE, list(x), k, None, None, tol, r0, history,
                    evidence={"iteration": k + 1, "pAp": pAp, "p_norm": _norm(p)},
                )
            alpha = rz / pAp
            x = [xi + alpha * pi for xi, pi in zip(x, p)]
            r = [ri - alpha * api for ri, api in zip(r, Ap)]
            k += 1
            rec_norm = _norm(r)

            true_norm = None
            if k % period == 0 or rec_norm <= tol:
                true_norm = _norm(self._true_residual(b, x))
                true_checks.append((k, true_norm))
            history.append((k, true_norm, rec_norm))

            if true_norm is not None and true_norm <= tol:
                self._commit(st, x, r, p, rz, k)
                return CGResult(STATUS_CONVERGED, list(x), k, true_norm,
                                rec_norm, tol, r0, history)

            if len(true_checks) > window:
                old_norm = true_checks[-1 - window][1]
                if true_checks[-1][1] >= factor * old_norm:
                    self._commit(st, x, r, p, rz, k)
                    return CGResult(
                        STATUS_STAGNATED, list(x), k, true_checks[-1][1],
                        rec_norm, tol, r0, history,
                        evidence={
                            "iteration": k,
                            "true_residual_checkpoints": list(true_checks),
                            "stagnation_window": window,
                            "stagnation_factor": factor,
                        },
                    )

            z = apply_pre(r)
            rz_new = _dot(r, z)
            if rz_new <= 0.0:
                self._commit(st, x, r, p, rz, k)
                return CGResult(
                    STATUS_INDEFINITE_PRECONDITIONER, list(x), k,
                    true_checks[-1][1], rec_norm, tol, r0, history,
                    evidence={"iteration": k, "rz": rz_new},
                )
            beta = rz_new / rz
            p = [zi + beta * pi for zi, pi in zip(z, p)]
            rz = rz_new
            # Commit after every completed iteration so that current_x and
            # checkpoint() always expose a consistent, resumable state even
            # if a later matvec raises.
            self._commit(st, x, r, p, rz, k)

        true_norm = _norm(self._true_residual(b, x))
        self._commit(st, x, r, p, rz, k)
        return CGResult(STATUS_MAX_ITER, list(x), k, true_norm, _norm(r), tol,
                        r0, history, evidence={"max_iter": max_iter})
