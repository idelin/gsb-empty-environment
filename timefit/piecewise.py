"""Piecewise-affine local->reference clock mapping.

Model
-----
* Knots sit at the distinct local times of the kept anchors; the mapping is
  affine on each inter-knot segment.
* Continuity (C0) is *declared and enforced explicitly*: adjacent segments
  share the reference value at their common knot (one variable per knot).
* Each segment slope must lie in [1 - max_drift, 1 + max_drift].
* Anchor i constrains the reference value at its local time to
  [ref_i - err_i, ref_i + err_i]  (anchor error interval).

Consequences
------------
* Two anchors at the same local time constrain the same knot variable, so
  disjoint error intervals at one local time are an immediate conflict.
* Query bounds are the exact min/max over ALL feasible mappings, obtained by
  projecting the difference-constraint polytope onto the two knot variables
  of the query segment (exact for difference constraints, via shortest
  paths) and evaluating the objective at the vertices of that 2-D polygon.
* Outlier rejection minimises the NUMBER of removed anchors (exact search
  over removal cardinalities), not an arbitrary residual threshold.

All arithmetic is exact (fractions.Fraction).
"""

from __future__ import annotations

from dataclasses import dataclass
from enum import Enum
from fractions import Fraction
from itertools import combinations

from .diffconstraints import DifferenceConstraints
from .timebase import Duration, Instant


class ExtrapolationError(ValueError):
    """Query lies beyond the allowed extrapolation distance."""


class InfeasibleError(ValueError):
    """No feasible mapping exists for the kept anchors."""


@dataclass(frozen=True)
class Anchor:
    """One observation: at `local` the reference clock read `ref` +/- `error`."""

    local: Instant
    ref: Instant
    error: Duration = Duration(0)

    def __post_init__(self):
        if self.error.seconds < 0:
            raise ValueError("error interval half-width must be >= 0")

    @property
    def ref_lo(self):
        return self.ref.seconds - self.error.seconds

    @property
    def ref_hi(self):
        return self.ref.seconds + self.error.seconds


class Status(Enum):
    EXACT = "exact"                            # exact minimum-removal fit found
    BUDGET_UNDECIDED = "budget_undecided"      # infeasible within the removal budget


@dataclass(frozen=True)
class FitResult:
    status: Status
    removed: tuple          # one minimum-cardinality removal set (anchor indices)
    num_optimal: int        # number of optimal removal sets found (capped)
    min_removals: int | None
    budget: int | None
    cores: tuple            # minimal infeasible anchor subsets (conflict cores)
    witness: tuple | None   # ((local Instant, ref Instant) per kept knot) or None


@dataclass(frozen=True)
class QueryResult:
    local: Instant
    ref_lo: Instant
    ref_hi: Instant
    kind: str               # 'knot' | 'interpolation' | 'extrapolation'
    segment: tuple | None   # knot indices of the segment used

    @property
    def corrected(self):
        """Midpoint of the feasible reference range."""
        return Instant((self.ref_lo.seconds + self.ref_hi.seconds) / 2)

    @property
    def uncertainty(self):
        """Width of the feasible reference range."""
        return Duration(self.ref_hi.seconds - self.ref_lo.seconds)


_OPTIMA_CAP = 64


class PiecewiseAffineFitter:
    def __init__(self, anchors, max_drift, max_extrapolation=Duration(0)):
        anchors = list(anchors)
        if not anchors:
            raise ValueError("need at least one anchor")
        self.anchors = anchors
        self.max_drift = Fraction(max_drift)
        if not 0 <= self.max_drift < 1:
            raise ValueError("max_drift must be in [0, 1)")
        if not isinstance(max_extrapolation, Duration):
            raise TypeError("max_extrapolation must be a Duration")
        self.max_extrapolation = max_extrapolation

    # -- constraint system -------------------------------------------------
    def _build(self, keep):
        """Difference constraints for the sub-problem over `keep` indices."""
        times = sorted({self.anchors[i].local.seconds for i in keep})
        index = {t: k + 1 for k, t in enumerate(times)}
        dc = DifferenceConstraints(len(times) + 1)
        for i in keep:
            a = self.anchors[i]
            dc.add_bound(index[a.local.seconds], lo=a.ref_lo, hi=a.ref_hi)
        for k in range(len(times) - 1):
            dt = times[k + 1] - times[k]
            dc.add_difference(k + 1, k + 2, (1 + self.max_drift) * dt)
            dc.add_difference(k + 2, k + 1, -(1 - self.max_drift) * dt)
        return dc, times

    def feasible(self, removed=()):
        keep = set(range(len(self.anchors))) - set(removed)
        dc, _ = self._build(keep)
        return dc.feasible()

    # -- exact minimum-removal search --------------------------------------
    def solve(self, budget=None, max_core_size=3):
        """Find a minimum-cardinality set of anchors to remove.

        With `budget=None` the exact optimum is returned (Status.EXACT).
        With a budget, only removal sets up to that size are tried; if none
        fits, the result is Status.BUDGET_UNDECIDED (reported separately
        from the exact optimum, which remains unknown).
        """
        cores = self.conflict_cores(max_core_size)
        n = len(self.anchors)
        limit = n if budget is None else min(budget, n)
        for k in range(limit + 1):
            optima = []
            for combo in combinations(range(n), k):
                if self.feasible(removed=combo):
                    optima.append(combo)
                    if len(optima) >= _OPTIMA_CAP:
                        break
            if optima:
                keep = set(range(n)) - set(optima[0])
                dc, times = self._build(keep)
                values = dc.witness()
                # The system is shift-invariant; pin the reference node to 0
                # so knot values are absolute reference readings.
                values = [v - values[0] for v in values]
                witness = tuple(
                    (Instant(t), Instant(values[k + 1]))
                    for k, t in enumerate(times)
                )
                return FitResult(Status.EXACT, optima[0], len(optima), k,
                                 budget, cores, witness)
        return FitResult(Status.BUDGET_UNDECIDED, (), 0, None, budget, cores, None)

    def conflict_cores(self, max_size=3):
        """Minimal infeasible anchor subsets (anchors + drift constraints)."""
        cores = []
        n = len(self.anchors)
        for size in range(1, max_size + 1):
            for combo in combinations(range(n), size):
                if any(set(core) < set(combo) for core in cores):
                    continue
                dc, _ = self._build(set(combo))
                if dc.feasible():
                    continue
                if all(self._build(set(combo) - {j})[0].feasible() for j in combo):
                    cores.append(combo)
        return tuple(cores)

    # -- queries -------------------------------------------------------------
    def query(self, local, removed=()):
        """Exact [lo, hi] reference range at `local` over all feasible maps."""
        if not isinstance(local, Instant):
            raise TypeError("query expects an Instant")
        keep = set(range(len(self.anchors))) - set(removed)
        dc, times = self._build(keep)
        if not dc.feasible():
            raise InfeasibleError("kept anchors admit no feasible mapping")
        t = local.seconds
        if len(times) == 1:
            if t != times[0]:
                raise ExtrapolationError("single-knot mapping cannot extrapolate")
            lo, hi = dc.tight_bound(1)
            return QueryResult(local, Instant(lo), Instant(hi), "knot", (0, 0))
        if t <= times[0]:
            seg, overshoot = 0, times[0] - t
        elif t >= times[-1]:
            seg, overshoot = len(times) - 2, t - times[-1]
        else:
            seg = max(k for k in range(len(times) - 1) if times[k] <= t)
            overshoot = Fraction(0)
        if overshoot > self.max_extrapolation.seconds:
            raise ExtrapolationError(
                f"query is {overshoot}s beyond the mapped range, "
                f"allowed extrapolation is {self.max_extrapolation.seconds}s"
            )
        t0, t1 = times[seg], times[seg + 1]
        mu = (t - t0) / (t1 - t0)
        lo, hi = self._pair_objective_range(dc, seg + 1, seg + 2, 1 - mu, mu)
        if overshoot > 0:
            kind = "extrapolation"
        elif mu == 0 or mu == 1:
            kind = "knot"
        else:
            kind = "interpolation"
        return QueryResult(local, Instant(lo), Instant(hi), kind, (seg, seg + 1))

    @staticmethod
    def _pair_objective_range(dc, p, q, alpha, beta):
        """Exact min/max of alpha*x_p + beta*x_q over the projection.

        The projection of a difference-constraint polytope onto two variables
        is exactly the 2-D polygon cut out by the tightest implied bounds
        (shortest paths), so the optimum is attained at a polygon vertex.
        """
        ap = dc.all_pairs()
        lines = [  # a*x + b*y <= c  with (x, y) = (x_p, x_q)
            (Fraction(1), Fraction(0), ap[0][p]),
            (Fraction(-1), Fraction(0), ap[p][0]),
            (Fraction(0), Fraction(1), ap[0][q]),
            (Fraction(0), Fraction(-1), ap[q][0]),
            (Fraction(-1), Fraction(1), ap[p][q]),
            (Fraction(1), Fraction(-1), ap[q][p]),
        ]
        if any(c is None for _, _, c in lines):
            raise InfeasibleError("unbounded projection")
        lo = hi = None
        for (a1, b1, c1), (a2, b2, c2) in combinations(lines, 2):
            det = a1 * b2 - a2 * b1
            if det == 0:
                continue
            x = (c1 * b2 - c2 * b1) / det
            y = (a1 * c2 - a2 * c1) / det
            if all(a * x + b * y <= c for a, b, c in lines):
                value = alpha * x + beta * y
                lo = value if lo is None or value < lo else lo
                hi = value if hi is None or value > hi else hi
        if lo is None:
            raise InfeasibleError("empty projection")
        return lo, hi
