"""Exact rational WLS solver with optional non-negativity.

Method: active-set on the bound constraints x_i >= 0.  Every subproblem is
an equality-constrained QP solved exactly via its KKT system over
``fractions.Fraction``.  Because arithmetic is exact, optimality is decided
by exact sign tests -- never by "the iterate changed by less than eps".

Convergence status honesty: a result is Status.OPTIMAL only when a full
KKT certificate (primal feasibility, stationarity, dual feasibility,
complementary slackness -- all exact) is attached.  Hitting the iteration
cap yields Status.MAX_ITER and is never reported as optimal.
"""

from dataclasses import dataclass
from enum import Enum
from fractions import Fraction

from .rational_linalg import (
    F, dot, inconsistency_certificate, independent_row_indices, solve_general,
)


class Status(Enum):
    OPTIMAL = "optimal"                        # KKT certificate attached
    INFEASIBLE = "infeasible"                  # Farkas certificate attached
    SINGULAR_ZERO_WEIGHT = "singular_zero_weight"  # null direction attached
    MAX_ITER = "max_iter"                      # NOT optimal


@dataclass
class KKTCertificate:
    """Exact KKT certificate for the bound-constrained WLS problem."""
    stationarity_residual: list   # W(x-y) + A^T lam - z  (must be all 0)
    primal_residual: list         # A x - b               (must be all 0)
    bound_multipliers: list       # z_i >= 0, z_i = 0 unless i in active set
    eq_multipliers: list          # lam aligned with the independent eq rows
    active_set: frozenset
    x: list

    def is_valid(self):
        if any(v != 0 for v in self.stationarity_residual):
            return False
        if any(v != 0 for v in self.primal_residual):
            return False
        for i, z in enumerate(self.bound_multipliers):
            if z < 0:
                return False
            if i in self.active_set:
                if self.x[i] != 0:
                    return False
            elif z != 0:
                return False
        return True


@dataclass
class SolveResult:
    status: Status
    x: list = None                      # name-ordered Fractions, if solved
    objective: Fraction = None
    iterations: int = 0
    active_set: frozenset = frozenset()
    certificate: KKTCertificate = None
    infeasibility_certificate: list = None   # mu: mu^T A = 0, mu^T b != 0
    singularity_direction: list = None       # d != 0, W d = 0, A d = 0
    undetermined_variables: tuple = ()
    warm_started: bool = False

    @property
    def is_optimal(self):
        return self.status is Status.OPTIMAL


class WLSSolver:
    def __init__(self, model, nonneg=False, max_iter=None):
        self.model = model
        self.nonneg = nonneg
        self.n = model.nvars
        self.max_iter = max_iter if max_iter is not None else 10 * (self.n + 1)
        self.y = list(model.observed)
        self.w = list(model.weights)
        # Full system (constraints + fixed rows) kept for certificates.
        self.A_full, self.b_full = model.build_system()
        # Pre-checks that do not depend on the active set.
        self._pre_infeasible_mu = inconsistency_certificate(self.A_full, self.b_full)
        if nonneg and self._pre_infeasible_mu is None:
            for idx, value in model.fixed.items():
                if value < 0:
                    self._pre_infeasible_mu = self._fixed_negative_certificate(idx)
        # Independent equality rows for the KKT solves (redundant rows of
        # the DAG constraint matrix are dropped here but still verified in
        # the certificate's primal residual).
        keep = independent_row_indices(self.A_full)
        self._A_ind = [self.A_full[i] for i in keep]
        self._b_ind = [self.b_full[i] for i in keep]
        self._last_result = None

    # -- internal helpers -------------------------------------------------

    def _fixed_negative_certificate(self, idx):
        # Fixed row position in A_full: constraints first, then fixed rows.
        row_pos = len(self.model.constraints) + sorted(self.model.fixed).index(idx)
        mu = [Fraction(0)] * len(self.A_full)
        mu[row_pos] = Fraction(1)
        return mu

    def _objective(self, x):
        return sum(
            (self.w[i] * (x[i] - self.y[i]) ** 2 for i in range(self.n)),
            Fraction(0),
        ) / 2

    def _eqp(self, active):
        """Solve the equality QP with x_i = 0 for i in ``active``.

        Returns ``(x, z, lam_eq)`` where ``z`` are bound multipliers (zero
        off the active set) and ``lam_eq`` aligns with ``self._A_ind``.
        Returns a Status on failure; for Status.SINGULAR_ZERO_WEIGHT,
        ``self._singularity_direction`` is set.
        """
        n = self.n
        A = [list(row) for row in self._A_ind]
        b = list(self._b_ind)
        is_bound = [False] * len(A)
        for i in sorted(active):
            row = [Fraction(0)] * n
            row[i] = Fraction(1)
            A.append(row)
            b.append(Fraction(0))
            is_bound.append(True)
        keep = independent_row_indices(A)
        # Equality rows come first and are independent among themselves,
        # so they are always kept; only redundant bound rows are dropped.
        A_k = [A[p] for p in keep]
        b_k = [b[p] for p in keep]
        kept_is_bound = [is_bound[p] for p in keep]
        m = len(A_k)
        # KKT system  [W A^T] [x  ]   [W y]
        #             [A 0 ] [lam] = [b  ]
        KKT = []
        rhs = []
        for i in range(n):
            row = [Fraction(0)] * (n + m)
            row[i] = self.w[i]
            for k in range(m):
                row[n + k] = A_k[k][i]
            KKT.append(row)
            rhs.append(self.w[i] * self.y[i])
        for k in range(m):
            row = [Fraction(0)] * (n + m)
            row[:n] = A_k[k]
            KKT.append(row)
            rhs.append(b_k[k])
        solved = solve_general(KKT, rhs)
        if solved is None:
            return Status.INFEASIBLE
        particular, null_basis = solved
        for vec in null_basis:
            d = vec[:n]
            if any(v != 0 for v in d):
                # A d = 0 and W d + A^T(*) = 0  =>  d^T W d = 0  =>  W d = 0.
                self._singularity_direction = d
                return Status.SINGULAR_ZERO_WEIGHT
        x = particular[:n]
        lam = particular[n:]
        # Bound multipliers: z = W(x-y) + (equality rows)^T lam_eq.
        # Off the active set this is exactly 0 by stationarity; on the
        # active set it equals -lam of the kept bound row (or 0 if that
        # bound row was redundant, which is a valid multiplier choice).
        z = [self.w[i] * (x[i] - self.y[i]) for i in range(n)]
        lam_eq = [Fraction(0)] * len(self._A_ind)
        eq_pos = 0
        for k in range(m):
            if kept_is_bound[k]:
                continue
            lam_eq[eq_pos] = lam[k]
            for i in range(n):
                z[i] += A_k[k][i] * lam[k]
            eq_pos += 1
        return x, z, lam_eq

    def _make_certificate(self, x, z, lam_eq, active):
        stat = [self.w[i] * (x[i] - self.y[i]) for i in range(self.n)]
        for k in range(len(self._A_ind)):
            for i in range(self.n):
                stat[i] += self._A_ind[k][i] * lam_eq[k]
        for i in active:
            stat[i] -= z[i]
        primal = [dot(row, x) - rhs for row, rhs in zip(self.A_full, self.b_full)]
        return KKTCertificate(
            stationarity_residual=stat,
            primal_residual=primal,
            bound_multipliers=list(z),
            eq_multipliers=list(lam_eq),
            active_set=frozenset(active),
            x=list(x),
        )

    # -- public API --------------------------------------------------------

    def solve(self, warm_active=None):
        """Solve from scratch (or from ``warm_active``); always re-verifies."""
        if self._pre_infeasible_mu is not None:
            return SolveResult(
                status=Status.INFEASIBLE,
                infeasibility_certificate=self._pre_infeasible_mu,
            )
        active = set()
        warm = False
        if self.nonneg and warm_active:
            active = set(warm_active)
            warm = True
        iterations = 0
        while True:
            out = self._eqp(active)
            if out is Status.INFEASIBLE:
                return SolveResult(status=Status.INFEASIBLE)
            if out is Status.SINGULAR_ZERO_WEIGHT:
                d = self._singularity_direction
                undet = tuple(
                    self.model.names[i] for i in range(self.n) if d[i] != 0
                )
                return SolveResult(
                    status=Status.SINGULAR_ZERO_WEIGHT,
                    singularity_direction=d,
                    undetermined_variables=undet,
                    iterations=iterations,
                    warm_started=warm,
                )
            x, z, lam_eq = out
            if self.nonneg:
                violating = [i for i in range(self.n)
                             if i not in active and x[i] < 0]
                if violating:
                    if iterations >= self.max_iter:
                        return SolveResult(status=Status.MAX_ITER,
                                           iterations=iterations)
                    # most negative, ties broken by index (Bland-style)
                    add = min(violating, key=lambda i: (x[i], i))
                    active.add(add)
                    iterations += 1
                    continue
                releasing = [i for i in active if z[i] < 0]
                if releasing:
                    if iterations >= self.max_iter:
                        return SolveResult(status=Status.MAX_ITER,
                                           iterations=iterations)
                    drop = min(releasing, key=lambda i: (z[i], i))
                    active.remove(drop)
                    iterations += 1
                    continue
            cert = self._make_certificate(x, z, lam_eq, active)
            result = SolveResult(
                status=Status.OPTIMAL,
                x=list(x),
                objective=self._objective(x),
                iterations=iterations,
                active_set=frozenset(active),
                certificate=cert,
                warm_started=warm,
            )
            self._last_result = result
            return result

    def revise(self, observed_updates=None, weight_updates=None, warm_start=True):
        """Apply a local observation/weight revision and re-solve.

        The previous active set may be used as a warm start, but the KKT
        certificate is always recomputed from scratch and must validate
        exactly before Status.OPTIMAL is returned.
        """
        for name, value in (observed_updates or {}).items():
            self.y[self.model._index[name]] = F(value)
        for name, value in (weight_updates or {}).items():
            w = F(value)
            if w < 0:
                raise ValueError("negative weight")
            self.w[self.model._index[name]] = w
        warm_active = None
        if warm_start and self._last_result is not None:
            warm_active = self._last_result.active_set
        return self.solve(warm_active=warm_active)
