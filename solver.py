"""Incremental weighted least squares with rank-1 factor maintenance.

Maintains the regularized normal equations

    M x = v,   M = A^T W A + lam*I,   v = A^T W y

row by row. The Cholesky factor of M is kept up to date with rank-1
updates / downdates (never a full resolve per observation), and is
periodically verified against the raw observations.

Dimensions and sample indices are explicit inputs. No NumPy/SciPy.
"""

import math
from collections import deque

from dense import (
    DowndateError,
    NotPositiveDefiniteError,
    chol_rank1_downdate,
    chol_rank1_update,
    chol_solve,
    cholesky,
    matvec,
    rref_solve,
)

__all__ = ["IncrementalLeastSquares", "SlidingWindowLeastSquares", "Solution"]


class Solution:
    """Result of a solve. If ``unique`` is False, ``x`` is a particular
    solution and the full solution space is x + span(null_basis)."""

    def __init__(self, x, unique, null_basis, rank, n_features):
        self.x = x
        self.unique = unique
        self.null_basis = null_basis
        self.rank = rank
        self.n_features = n_features

    def __repr__(self):
        kind = "unique" if self.unique else "degenerate(rank=%d/%d)" % (
            self.rank, self.n_features)
        return "Solution(x=%r, %s)" % (self.x, kind)


class IncrementalLeastSquares:
    """Row-by-row weighted least squares with optional ridge regularization.

    Parameters
    ----------
    n_features : explicit column count of the design matrix (required).
    regularization : lam >= 0; M = A^T W A + lam*I. lam > 0 keeps M
        positive definite so the solution is always unique.
    check_every : run a residual/drift verification against the raw
        observations after this many updates (0 disables).
    drift_tol : max allowed |M, v| drift before an automatic rebuild.
    strict_downdate : if True, a failing rank-1 downdate rolls the whole
        retraction back atomically (old factor kept) and raises. If False
        (default), a retraction that drops rank is accepted and solve()
        returns the solution space.
    """

    def __init__(self, n_features, regularization=0.0, check_every=50,
                 drift_tol=1e-8, strict_downdate=False):
        if not isinstance(n_features, int) or n_features < 1:
            raise ValueError("n_features must be a positive integer")
        if not (regularization >= 0.0) or not math.isfinite(regularization):
            raise ValueError("regularization must be a finite value >= 0")
        self.n_features = n_features
        self.regularization = float(regularization)
        self.check_every = int(check_every)
        self.drift_tol = float(drift_tol)
        self.strict_downdate = bool(strict_downdate)

        n = n_features
        self._M = [[0.0] * n for _ in range(n)]
        self._v = [0.0] * n
        for i in range(n):
            self._M[i][i] = self.regularization
        self._L = None
        if self.regularization > 0.0:
            r = math.sqrt(self.regularization)
            self._L = [[r if i == j else 0.0 for j in range(n)]
                       for i in range(n)]
        self._samples = {}          # id -> (row tuple, weight, y)
        self._updates_since_check = 0
        self.rebuild_count = 0      # factor rebuilds from raw observations
        self.last_report = None     # last verification report

    # ------------------------------------------------------------------ API

    @property
    def sample_ids(self):
        return tuple(self._samples.keys())

    def __len__(self):
        return len(self._samples)

    def add(self, sample_id, row, y, weight=1.0):
        """Add one observation. Atomic: invalid input changes nothing."""
        row = self._validate(sample_id, row, y, weight, expect_new=True)
        snapshot = self._snapshot()
        try:
            self._accumulate(row, weight, y, sign=+1.0)
            if weight > 0.0 and self._L is not None:
                self._L = chol_rank1_update(self._L,
                                            self._scaled_row(row, weight))
            elif weight > 0.0 and self._L is None:
                self._try_refactor()
            self._samples[sample_id] = (row, weight, y)
        except Exception:
            self._restore(snapshot)
            raise
        self._tick()

    def retract(self, sample_id):
        """Remove one observation. Unknown ids raise KeyError and the
        state is left untouched (atomic rollback on any failure)."""
        if sample_id not in self._samples:
            raise KeyError("cannot retract unknown sample id %r" % (sample_id,))
        row, weight, y = self._samples[sample_id]
        snapshot = self._snapshot()
        try:
            self._accumulate(row, weight, y, sign=-1.0)
            if weight > 0.0:
                self._downdate(row, weight)
            del self._samples[sample_id]
        except Exception:
            self._restore(snapshot)
            raise
        self._tick()

    def solve(self):
        """Return the current Solution. When M is singular (only possible
        with regularization == 0), returns the affine solution space."""
        if self._L is not None:
            return Solution(chol_solve(self._L, self._v), True, [],
                            self.n_features, self.n_features)
        particular, null_basis, rank = rref_solve(self._M, self._v)
        return Solution(particular, False, null_basis, rank, self.n_features)

    def verify(self):
        """Recompute M, v and residuals from the raw observations.

        Returns a report dict; rebuilds the factor if drift exceeds
        ``drift_tol`` (counted in ``rebuild_count``).
        """
        n = self.n_features
        M = [[0.0] * n for _ in range(n)]
        v = [0.0] * n
        for i in range(n):
            M[i][i] = self.regularization
        for row, w, y in self._samples.values():
            if w == 0.0:
                continue
            for i in range(n):
                wi = w * row[i]
                for j in range(i, n):
                    M[i][j] += wi * row[j]
                v[i] += wi * y
        for i in range(n):
            for j in range(i):
                M[i][j] = M[j][i]
        drift = max(
            [abs(M[i][j] - self._M[i][j]) for i in range(n)
             for j in range(n)] +
            [abs(v[i] - self._v[i]) for i in range(n)] + [0.0])
        rebuilt = False
        if drift > self.drift_tol:
            self._M, self._v = M, v
            self._try_refactor()
            self.rebuild_count += 1
            rebuilt = True
        sol = self.solve()
        r = matvec(self._M, sol.x)
        normal_res = max((abs(r[i] - self._v[i]) for i in range(n)),
                         default=0.0)
        obs_ss = 0.0
        for row, w, y in self._samples.values():
            ri = sum(row[j] * sol.x[j] for j in range(n)) - y
            obs_ss += w * ri * ri
        self.last_report = {
            "n_samples": len(self._samples),
            "drift": drift,
            "rebuilt": rebuilt,
            "rebuild_count": self.rebuild_count,
            "normal_residual": normal_res,
            "obs_residual": math.sqrt(obs_ss),
            "unique": sol.unique,
            "rank": sol.rank,
        }
        return self.last_report

    # ------------------------------------------------------------- internals

    def _validate(self, sample_id, row, y, weight, expect_new):
        if expect_new and sample_id in self._samples:
            raise KeyError("duplicate sample id %r" % (sample_id,))
        if len(row) != self.n_features:
            raise ValueError("row has %d entries, expected %d"
                             % (len(row), self.n_features))
        if not math.isfinite(weight) or weight < 0.0:
            raise ValueError("weight must be finite and >= 0 (got %r); "
                             "negative weights break positive definiteness"
                             % (weight,))
        if not math.isfinite(y):
            raise ValueError("y must be finite")
        row = tuple(float(a) for a in row)
        if not all(math.isfinite(a) for a in row):
            raise ValueError("row entries must be finite")
        return row

    @staticmethod
    def _scaled_row(row, weight):
        s = math.sqrt(weight)
        return [s * a for a in row]

    def _accumulate(self, row, weight, y, sign):
        n = self.n_features
        M, v = self._M, self._v
        for i in range(n):
            wi = sign * weight * row[i]
            Mi = M[i]
            for j in range(n):
                Mi[j] += wi * row[j]
            v[i] += wi * y

    def _downdate(self, row, weight):
        scaled = self._scaled_row(row, weight)
        if self._L is None:
            self._try_refactor()
            return
        try:
            self._L = chol_rank1_downdate(self._L, scaled)
            return
        except DowndateError:
            pass
        # Downdate failed: either the matrix genuinely lost rank, or
        # roundoff made a still-PD matrix look singular. Try a fresh
        # factorization of the already-updated M to tell them apart.
        try:
            self._L = cholesky(self._M)
            self.rebuild_count += 1
            return
        except NotPositiveDefiniteError:
            pass
        if self.strict_downdate:
            raise DowndateError(
                "downdate rejected: would drop rank (strict mode)")
        # Rank genuinely dropped: accept the retraction, keep no factor.
        self._L = None

    def _try_refactor(self):
        try:
            self._L = cholesky(self._M)
        except NotPositiveDefiniteError:
            self._L = None

    def _snapshot(self):
        return ([row[:] for row in self._M], list(self._v),
                None if self._L is None else [r[:] for r in self._L],
                dict(self._samples))

    def _restore(self, snapshot):
        self._M, self._v, self._L, self._samples = snapshot

    def _tick(self):
        self._updates_since_check += 1
        if self.check_every and self._updates_since_check >= self.check_every:
            self._updates_since_check = 0
            self.verify()


class SlidingWindowLeastSquares:
    """Weighted sliding window over an IncrementalLeastSquares core.

    ``add`` appends an observation and evicts (retracts) the oldest one
    once the window exceeds ``maxlen``. All factor maintenance is done
    through rank-1 updates/downdates in the core solver.
    """

    def __init__(self, n_features, maxlen, regularization=0.0, **kw):
        if maxlen < 1:
            raise ValueError("maxlen must be >= 1")
        self.maxlen = int(maxlen)
        self.core = IncrementalLeastSquares(n_features, regularization, **kw)
        self._order = deque()
   # ids currently inside the window
        self._next_id = 0

    def add(self, row, y, weight=1.0):
        """Add a sample; returns (new_id, evicted_id_or_None)."""
        sid = self._next_id
        self._next_id += 1
        self.core.add(sid, row, y, weight)
        self._order.append(sid)
        evicted = None
        if len(self._order) > self.maxlen:
            evicted = self._order.popleft()
            self.core.retract(evicted)
        return sid, evicted

    def solve(self):
        return self.core.solve()

    def verify(self):
        return self.core.verify()

    @property
    def window_ids(self):
        return tuple(self._order)

    def __len__(self):
        return len(self._order)
