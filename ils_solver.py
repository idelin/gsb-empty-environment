"""Incremental weighted least squares over a sliding observation set.

Maintains the normal equations

    M x = z,   M = A^T W A + lambda I,   z = A^T W b

through a Cholesky factor of M that is modified with rank-one
updates/downdates as observations are added or retracted -- the system
is *not* re-solved from scratch on every change.  A full O(n^3)
refactorization only happens on rank transitions or when the periodic
verification detects drift.

Guarantees:
  * sample ids, dimension and weights are validated before any mutation;
  * a failed update rolls the factor and right-hand side back atomically;
  * zero-weight samples register their id but contribute nothing;
  * negative weights are rejected (they would break the positive
    definiteness the Cholesky factor relies on);
  * if a retraction drops the rank, solve() returns the affine solution
    space (particular solution + null-space basis), or the unique
    regularized solution when lambda > 0 keeps M positive definite.
"""

import math
from dataclasses import dataclass

from ils_linalg import (
    RankDropError,
    cholesky_factor,
    cholesky_rank1,
    cholesky_solve,
    dense_rank,
    llt,
    mat_vec,
    solution_space,
)


class IlsError(Exception):
    """Base class for incremental least-squares errors."""


class DuplicateSampleError(IlsError):
    """A sample with the same id is already registered."""


class UnknownSampleError(IlsError):
    """Retraction of a sample id that was never added (or already removed)."""


class InvalidWeightError(IlsError):
    """Negative or otherwise invalid weight."""


class DimensionError(IlsError):
    """Observation row does not match the declared problem dimension."""


class NonFiniteError(IlsError):
    """Input contains NaN or infinite values."""


class UpdateError(IlsError):
    """A factor update failed; the previous state was restored atomically."""


@dataclass
class Solution:
    """Result of solve().

    status is "unique" (factor-based solve) or "deficient" (dense
    fallback).  In the deficient case ``x`` is a particular solution and
    the full solution set is ``x + span(null_space)``.
    """

    status: str
    x: list
    null_space: list
    rank: int


@dataclass
class VerificationReport:
    """Result of a periodic cross-check against the raw observations."""

    ok: bool
    factor_deviation: float   # max |L L^T - M| / scale (nan if deficient)
    rhs_deviation: float      # max |z_incremental - z_raw| / scale
    solution_residual: float  # max |M x - z| / scale from raw observations
    rank: int
    refactorized: bool        # True if the check triggered a self-heal


class IncrementalLeastSquares:
    """Weighted least squares with explicit ids and rank-one factor updates."""

    def __init__(self, n, regularization=0.0, *, verify_interval=64,
                 rank_tol=1e-12, verify_tol=1e-8):
        if not isinstance(n, int) or n < 1:
            raise DimensionError("n must be a positive integer")
        regularization = float(regularization)
        if not math.isfinite(regularization) or regularization < 0.0:
            raise InvalidWeightError(
                "regularization must be a finite non-negative number")
        self.n = n
        self.regularization = regularization
        self.verify_interval = int(verify_interval)
        self.rank_tol = float(rank_tol)
        self.verify_tol = float(verify_tol)
        self._obs = {}            # id -> (row tuple, weight, target)
        self._z = [0.0] * n       # incremental A^T W b
        self._L = None            # lower Cholesky factor of M
        self._factor_valid = False
        self._updates_since_verify = 0
        self.stats = {"updates": 0, "verifications": 0,
                      "refactorizations": 0, "rollbacks": 0}
        if regularization > 0.0:
            root = math.sqrt(regularization)
            self._L = [[root if i == j else 0.0 for j in range(n)]
                       for i in range(n)]
            self._factor_valid = True

    # ------------------------------------------------------------------ #
    # introspection

    def __contains__(self, sample_id):
        return sample_id in self._obs

    def __len__(self):
        return len(self._obs)

    @property
    def observations(self):
        """Copy of the id -> (row, weight, target) mapping."""
        return dict(self._obs)

    @property
    def is_positive_definite(self):
        """True while a valid Cholesky factor of M is maintained."""
        return self._factor_valid

    def rhs(self):
        """Current incremental right-hand side z = A^T W b."""
        return list(self._z)

    def normal_matrix(self):
        """Current M = A^T W A + lambda I (from the factor when valid)."""
        if self._factor_valid:
            return llt(self._L)
        M, _ = self._raw_normal_equations()
        return M

    # ------------------------------------------------------------------ #
    # validation (all checks run before any state is touched)

    def _validate_row(self, row):
        try:
            values = tuple(float(v) for v in row)
        except (TypeError, ValueError):
            raise DimensionError("row must be a sequence of numbers") from None
        if len(values) != self.n:
            raise DimensionError(
                "row has length %d, expected %d" % (len(values), self.n))
        if not all(math.isfinite(v) for v in values):
            raise NonFiniteError("row contains non-finite values")
        return values

    @staticmethod
    def _validate_weight(weight):
        weight = float(weight)
        if not math.isfinite(weight):
            raise NonFiniteError("weight must be finite")
        if weight < 0.0:
            raise InvalidWeightError(
                "negative weights are rejected: they would break the "
                "positive-definiteness assumption")
        return weight

    # ------------------------------------------------------------------ #
    # raw normal equations rebuilt from the stored observations

    def _raw_normal_equations(self):
        n = self.n
        M = [[0.0] * n for _ in range(n)]
        z = [0.0] * n
        for row, weight, target in self._obs.values():
            if weight == 0.0:
                continue
            for i in range(n):
                wi = weight * row[i]
                z[i] += wi * target
                Mi = M[i]
                for j in range(n):
                    Mi[j] += wi * row[j]
        if self.regularization:
            for i in range(n):
                M[i][i] += self.regularization
        return M, z

    def _raw_rank_without(self, excluded_id):
        n = self.n
        M = [[0.0] * n for _ in range(n)]
        for sid, (row, weight, _) in self._obs.items():
            if sid == excluded_id or weight == 0.0:
                continue
            for i in range(n):
                wi = weight * row[i]
                for j in range(n):
                    M[i][j] += wi * row[j]
        if self.regularization:
            for i in range(n):
                M[i][i] += self.regularization
        return dense_rank(M)

    def _refactorize(self):
        """Full O(n^3) rebuild of the factor from raw observations."""
        M, z = self._raw_normal_equations()
        self._z = z
        try:
            self._L = cholesky_factor(M)
        except RankDropError:
            self._L = None
            self._factor_valid = False
            return False
        self._factor_valid = True
        self.stats["refactorizations"] += 1
        return True

    # ------------------------------------------------------------------ #
    # updates

    def add(self, sample_id, row, target, weight=1.0):
        """Add one observation row; atomic on failure."""
        if sample_id in self._obs:
            raise DuplicateSampleError(
                "sample id %r already exists" % (sample_id,))
        row = self._validate_row(row)
        weight = self._validate_weight(weight)
        target = float(target)
        if not math.isfinite(target):
            raise NonFiniteError("target must be finite")

        if weight == 0.0:
            # id is registered but the sample contributes nothing.
            self._obs[sample_id] = (row, weight, target)
            self._after_update()
            return

        if self._factor_valid:
            scale = math.sqrt(weight)
            L_new = [list(r) for r in self._L]
            cholesky_rank1(L_new, [scale * v for v in row], +1, self.rank_tol)
            z_new = [z + weight * a * target
                     for z, a in zip(self._z, row)]
            if not all(math.isfinite(v) for r in L_new for v in r):
                self.stats["rollbacks"] += 1
                raise UpdateError(
                    "factor update overflowed; state rolled back")
            self._L = L_new
            self._z = z_new
            self._obs[sample_id] = (row, weight, target)
        else:
            # currently rank-deficient: try a full rebuild including the
            # new row; if it is still deficient we stay in dense mode.
            self._obs[sample_id] = (row, weight, target)
            self._refactorize()
        self._after_update()

    def retract(self, sample_id):
        """Remove one observation; atomic on failure.

        If the removal drops the rank of M below n the removal is
        committed and the solver switches to the dense solution-space
        fallback (or keeps a unique solution when regularized).
        """
        if sample_id not in self._obs:
            raise UnknownSampleError(
                "sample id %r is not registered" % (sample_id,))
        row, weight, target = self._obs[sample_id]

        if weight == 0.0:
            del self._obs[sample_id]
            self._after_update()
            return

        z_new = [z - weight * a * target
                 for z, a in zip(self._z, row)]

        if self._factor_valid:
            scale = math.sqrt(weight)
            L_new = [list(r) for r in self._L]
            try:
                cholesky_rank1(L_new, [scale * v for v in row], -1,
                               self.rank_tol)
            except RankDropError:
                if self._raw_rank_without(sample_id) < self.n:
                    # legitimate rank drop: commit and go dense
                    del self._obs[sample_id]
                    self._z = z_new
                    self._L = None
                    self._factor_valid = False
                    self._after_update()
                    return
                # numerical failure although M stays full rank: roll back
                self.stats["rollbacks"] += 1
                raise UpdateError(
                    "downdate failed although the system stays full rank; "
                    "state rolled back") from None
            if not all(math.isfinite(v) for r in L_new for v in r):
                self.stats["rollbacks"] += 1
                raise UpdateError(
                    "downdate overflowed; state rolled back")
            self._L = L_new
            self._z = z_new
            del self._obs[sample_id]
        else:
            del self._obs[sample_id]
            self._z = z_new
        self._after_update()

    def _after_update(self):
        self.stats["updates"] += 1
        self._updates_since_verify += 1
        if self._updates_since_verify >= self.verify_interval:
            self.verify()

    # ------------------------------------------------------------------ #
    # solving

    def solve(self):
        """Return the current Solution (unique or solution space)."""
        if self._factor_valid:
            x = cholesky_solve(self._L, self._z)
            return Solution(status="unique", x=x, null_space=[], rank=self.n)
        M, z = self._raw_normal_equations()
        particular, basis, rank = solution_space(M, z)
        return Solution(status="deficient", x=particular,
                        null_space=basis, rank=rank)

    def residual(self, x=None):
        """max |M x - z| recomputed from the raw observations."""
        if x is None:
            x = self.solve().x
        M, z = self._raw_normal_equations()
        r = mat_vec(M, x)
        return max((abs(a - b) for a, b in zip(r, z)), default=0.0)

    # ------------------------------------------------------------------ #
    # periodic verification against the raw observations

    def verify(self):
        """Cross-check the incremental factor/rhs against raw data.

        Rebuilds M and z from the stored observations, measures the
        drift of the maintained factor and right-hand side, evaluates
        the residual of the current solution against the raw normal
        equations, and self-heals (refactorizes) if the drift exceeds
        ``verify_tol``.
        """
        M, z_raw = self._raw_normal_equations()
        m_scale = max(1.0, max((abs(v) for r in M for v in r), default=0.0))
        z_scale = max(1.0, max((abs(v) for v in z_raw), default=0.0))

        rhs_dev = max((abs(a - b) for a, b in zip(self._z, z_raw)),
                      default=0.0) / z_scale

        if self._factor_valid:
            prod = llt(self._L)
            factor_dev = max(abs(prod[i][j] - M[i][j])
                             for i in range(self.n)
                             for j in range(self.n)) / m_scale
            rank = self.n
            factor_ok = factor_dev <= self.verify_tol
        else:
            factor_dev = float("nan")
            rank = dense_rank(M)
            factor_ok = True  # nothing to drift while in dense mode

        sol = self.solve()
        r = mat_vec(M, sol.x)
        sol_resid = max((abs(a - b) for a, b in zip(r, z_raw)),
                        default=0.0) / m_scale

        ok = (factor_ok
              and rhs_dev <= self.verify_tol
              and sol_resid <= self.verify_tol)

        refactorized = False
        if not ok:
            self._refactorize()
            refactorized = True

        self._updates_since_verify = 0
        self.stats["verifications"] += 1
        return VerificationReport(
            ok=ok,
            factor_deviation=factor_dev,
            rhs_deviation=rhs_dev,
            solution_residual=sol_resid,
            rank=rank,
            refactorized=refactorized,
        )
