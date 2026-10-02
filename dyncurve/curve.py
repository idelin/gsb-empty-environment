"""Static dynamics curve compilation in the dB domain.

A curve maps input level (dB) to output level (dB) as a pure, stateless
function.  It is built from segments:

- compress: above ``threshold_db`` the slope is multiplied by ``1/ratio``
- expand:   below ``threshold_db`` the slope is multiplied by ``ratio``

``knee_db`` > 0 rounds the breakpoint with a quadratic soft knee that is
continuous in value *and* first derivative at both knee edges.

Declared numerical tolerances (module constants):
    VALUE_TOL   value-continuity tolerance in dB
    SLOPE_TOL   derivative-continuity tolerance in dB/dB
    INVERSE_TOL inverse-lookup tolerance in dB
    DB_FLOOR    silence floor; 0 amplitude maps here instead of -inf
"""
from __future__ import annotations

import math
from dataclasses import dataclass

DB_FLOOR = -120.0
VALUE_TOL = 1e-9
SLOPE_TOL = 1e-7
INVERSE_TOL = 1e-9
INVERSE_X_MIN = -240.0
INVERSE_X_MAX = 120.0

COMPRESS = "compress"
EXPAND = "expand"


def soft_hinge_hi(x: float, threshold: float, knee: float) -> float:
    """C1-smoothed max(0, x - threshold)."""
    if knee <= 0.0:
        return max(0.0, x - threshold)
    lo = threshold - 0.5 * knee
    hi = threshold + 0.5 * knee
    if x <= lo:
        return 0.0
    if x >= hi:
        return x - threshold
    d = x - lo
    return d * d / (2.0 * knee)


def soft_hinge_hi_deriv(x: float, threshold: float, knee: float) -> float:
    if knee <= 0.0:
        return 0.0 if x < threshold else 1.0
    lo = threshold - 0.5 * knee
    hi = threshold + 0.5 * knee
    if x <= lo:
        return 0.0
    if x >= hi:
        return 1.0
    return (x - lo) / knee


def soft_hinge_lo(x: float, threshold: float, knee: float) -> float:
    """C1-smoothed min(0, x - threshold)."""
    return -soft_hinge_hi(-x, -threshold, knee)


def soft_hinge_lo_deriv(x: float, threshold: float, knee: float) -> float:
    return soft_hinge_hi_deriv(-x, -threshold, knee)


@dataclass(frozen=True)
class Segment:
    threshold_db: float
    ratio: float            # > 0; math.inf allowed (limiter / gate plateau)
    knee_db: float = 0.0
    kind: str = COMPRESS


@dataclass(frozen=True)
class InverseResult:
    x_lo: float             # smallest input dB mapping to the requested output
    x_hi: float             # largest  input dB mapping to the requested output
    is_plateau: bool        # True when the pre-image is a non-trivial interval


@dataclass(frozen=True)
class _Term:
    coeff: float
    threshold: float
    knee: float
    direction: int          # +1 => hinge above threshold, -1 => hinge below


class CompiledCurve:
    """Immutable, stateless level->level map.  Safe to share across processors."""

    def __init__(self, terms, segments):
        self._terms = tuple(terms)
        self.segments = tuple(segments)
        self.knots = self._collect_knots()

    def _collect_knots(self):
        pts = []
        for t in self._terms:
            if t.knee > 0.0:
                pts.append(t.threshold - 0.5 * t.knee)
                pts.append(t.threshold + 0.5 * t.knee)
            else:
                pts.append(t.threshold)
        return tuple(sorted(set(pts)))

    def output_db(self, x_db: float) -> float:
        y = x_db
        for t in self._terms:
            if t.direction > 0:
                y += t.coeff * soft_hinge_hi(x_db, t.threshold, t.knee)
            else:
                y += t.coeff * soft_hinge_lo(x_db, t.threshold, t.knee)
        return y

    def slope(self, x_db: float) -> float:
        s = 1.0
        for t in self._terms:
            if t.direction > 0:
                s += t.coeff * soft_hinge_hi_deriv(x_db, t.threshold, t.knee)
            else:
                s += t.coeff * soft_hinge_lo_deriv(x_db, t.threshold, t.knee)
        return s

    def gain_db(self, x_db: float) -> float:
        return self.output_db(x_db) - x_db

    # -- inverse lookup ----------------------------------------------------
    def invert(self, y_db: float) -> InverseResult:
        """Pre-image of ``y_db``.  A plateau yields x_lo < x_hi."""
        lo, hi = INVERSE_X_MIN, INVERSE_X_MAX
        if y_db < self.output_db(lo):
            return InverseResult(lo, lo, False)
        if y_db > self.output_db(hi):
            return InverseResult(hi, hi, False)
        # x_lo: first x with f(x) >= y
        a, b = lo, hi
        for _ in range(200):
            m = 0.5 * (a + b)
            if self.output_db(m) < y_db:
                a = m
            else:
                b = m
            if b - a < INVERSE_TOL:
                break
        x_lo = b
        # x_hi: last x with f(x) <= y
        a, b = lo, hi
        for _ in range(200):
            m = 0.5 * (a + b)
            if self.output_db(m) <= y_db:
                a = m
            else:
                b = m
            if b - a < INVERSE_TOL:
                break
        x_hi = a
        return InverseResult(x_lo, x_hi, x_hi - x_lo > 1e-6)

    def plateau_regions(self):
        """Output-plateau intervals [(x_lo, x_hi, y_db)] where slope == 0.

        These are exactly the non-invertible regions of the curve.
        """
        bounds = [INVERSE_X_MIN] + list(self.knots) + [INVERSE_X_MAX]
        regions = []
        for a, b in zip(bounds, bounds[1:]):
            mid = 0.5 * (a + b)
            if abs(self.slope(mid)) <= SLOPE_TOL:
                x_lo, x_hi = a, b
                if regions and regions[-1][1] == x_lo:
                    prev = regions.pop()
                    x_lo = prev[0]
                regions.append((x_lo, x_hi, self.output_db(mid)))
        return regions


def compile_curve(segments) -> CompiledCurve:
    """Compile segments into a verified :class:`CompiledCurve`.

    Raises ValueError on invalid parameters, overlapping same-direction
    knees, broken value/derivative continuity, or non-monotonic results.
    """
    segs = list(segments)
    for s in segs:
        if not (s.ratio > 0.0):
            raise ValueError(f"ratio must be > 0, got {s.ratio}")
        if s.knee_db < 0.0:
            raise ValueError(f"knee_db must be >= 0, got {s.knee_db}")
        if s.kind not in (COMPRESS, EXPAND):
            raise ValueError(f"unknown segment kind {s.kind!r}")

    # Same-direction knees of adjacent segments must not overlap, so that
    # segment boundaries keep their analytic meaning.
    for kind in (COMPRESS, EXPAND):
        grp = sorted((s for s in segs if s.kind == kind),
                     key=lambda s: s.threshold_db)
        for a, b in zip(grp, grp[1:]):
            if a.threshold_db + 0.5 * a.knee_db > b.threshold_db - 0.5 * b.knee_db:
                raise ValueError(
                    f"overlapping knees at {a.threshold_db} and {b.threshold_db} dB")

    terms = []
    # Compress segments: cumulative slope above threshold k is prod(1/R_i).
    comp = sorted((s for s in segs if s.kind == COMPRESS),
                  key=lambda s: s.threshold_db)
    slope_so_far = 1.0
    for s in comp:
        new_slope = slope_so_far / s.ratio
        terms.append(_Term(new_slope - slope_so_far, s.threshold_db,
                           s.knee_db, +1))
        slope_so_far = new_slope
    # Expand segments: cumulative slope below threshold k is prod(R_i).
    expn = sorted((s for s in segs if s.kind == EXPAND),
                  key=lambda s: s.threshold_db, reverse=True)
    slope_so_far = 1.0
    for s in expn:
        new_slope = slope_so_far * s.ratio
        terms.append(_Term(new_slope - slope_so_far, s.threshold_db,
                           s.knee_db, -1))
        slope_so_far = new_slope

    curve = CompiledCurve(terms, segs)
    _verify(curve)
    return curve


def _verify(curve: CompiledCurve) -> None:
    eps = 1e-6
    # A jump would show up as |dy| >> slope_bound * 2*eps.
    slope_bound = 1.0 + sum(abs(t.coeff) for t in curve._terms)
    value_jump_tol = VALUE_TOL + 2.0 * eps * slope_bound
    for k in curve.knots:
        # value continuity
        dv = abs(curve.output_db(k + eps) - curve.output_db(k - eps))
        if dv > value_jump_tol:
            raise ValueError(f"value discontinuity at {k} dB: {dv}")
        # derivative condition: must be continuous where every knee is soft
        soft = all(t.knee > 0.0 or abs(k - t.threshold) > 1e-12
                   for t in curve._terms)
        if soft:
            # allow for the finite probe step: |ds| <= 2*eps*curvature
            curvature = sum(abs(t.coeff) / t.knee for t in curve._terms
                            if t.knee > 0.0)
            ds = abs(curve.slope(k + eps) - curve.slope(k - eps))
            if ds > SLOPE_TOL + 2.0 * eps * curvature:
                raise ValueError(f"slope discontinuity at soft knot {k} dB: {ds}")
    # monotonicity: slope is piecewise linear with knots at knee edges,
    # so checking knots (and far ends) is exact.
    probe = [INVERSE_X_MIN] + list(curve.knots) + [INVERSE_X_MAX]
    for p in probe:
        if curve.slope(p) < -SLOPE_TOL:
            raise ValueError(f"non-monotone curve: slope {curve.slope(p)} at {p} dB")


def amp_to_db(a: float) -> float:
    """Linear amplitude to dB.  Silence maps to DB_FLOOR, never -inf."""
    if a <= 0.0:
        return DB_FLOOR
    return max(20.0 * math.log10(a), DB_FLOOR)


def db_to_amp(db: float) -> float:
    return 10.0 ** (db / 20.0)
