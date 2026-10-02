"""Static dynamics curve compilation (dB domain).

A curve maps input level (dBFS) to output level (dBFS). It is compiled from
an ordered list of Segment specs into a piecewise structure of linear and
quadratic (soft-knee) pieces. Compilation validates:

  * value continuity at every piece boundary,
  * derivative (slope) conditions: continuous across soft knees, and equal to
    the declared region slopes elsewhere,
  * monotonicity: the compiled curve is non-decreasing.

The curve is a pure function of level: envelope detection and gain smoothing
live in dyncurve.envelope and are deliberately separated from this module.

Numeric contract (see dyncurve.TOLERANCES):
  * levels at or below DB_FLOOR are treated as silence; gain_db() is clamped
    there so silence never produces inf/nan,
  * ratio == 1 yields the identity curve,
  * a compress segment with ratio == math.inf produces a zero-slope plateau
    (limiter); plateaus are non-invertible and inverse_db() reports them as
    intervals.
"""
from __future__ import annotations

import bisect
import math
from dataclasses import dataclass
from typing import List, Optional, Sequence, Tuple

DB_FLOOR = -160.0          # dBFS; at/below this a signal is treated as silence
CONTINUITY_TOL = 1e-9      # max |value| mismatch allowed at piece boundaries
SLOPE_TOL = 1e-9           # max |slope| mismatch allowed across a soft knee
KNEE_GUARD = 1e-12         # guards degenerate knee widths


@dataclass(frozen=True)
class Segment:
    """One breakpoint of a static curve.

    kind='compress': above threshold_db the slope becomes 1/ratio.
    kind='expand':   below threshold_db the slope becomes ratio (downward
                     expansion).
    knee_db == 0 gives a hard knee; knee_db > 0 inserts a quadratic blend of
    width knee_db centred on the threshold (value- and slope-continuous).
    ratio must be >= 1; math.inf is allowed for compress (plateau/limiter).
    """
    threshold_db: float
    ratio: float
    knee_db: float = 0.0
    kind: str = "compress"

    def __post_init__(self):
        if self.kind not in ("compress", "expand"):
            raise ValueError(f"unknown segment kind: {self.kind!r}")
        if not (self.ratio >= 1.0 or self.ratio == math.inf):
            raise ValueError(f"ratio must be >= 1, got {self.ratio}")
        if self.kind == "expand" and self.ratio == math.inf:
            raise ValueError("expand segment with infinite ratio is discontinuous")
        if self.knee_db < 0.0:
            raise ValueError("knee_db must be >= 0")


@dataclass(frozen=True)
class _Piece:
    """Compiled piece on [x0, x1].

    y(x) = y0 + s0*d + (s1-s0)*d^2/(2*w) with d = x-x0, w = x1-x0.
    The first/last piece may have an infinite bound; those are linear and
    evaluated from their finite anchor end.
    """
    x0: float
    x1: float
    y0: float
    y1: float
    s0: float
    s1: float

    @property
    def is_plateau(self) -> bool:
        return self.s0 == 0.0 and self.s1 == 0.0

    def eval(self, x: float) -> float:
        if self.is_plateau:
            return self.y0
        if self.x0 == -math.inf:            # leading linear piece
            return self.y1 + self.s1 * (x - self.x1)
        if self.x1 == math.inf:             # trailing linear piece
            return self.y0 + self.s0 * (x - self.x0)
        d = x - self.x0
        if self.s0 == self.s1:
            return self.y0 + self.s0 * d
        return self.y0 + self.s0 * d + (self.s1 - self.s0) * d * d / (2.0 * (self.x1 - self.x0))

    def slope(self, x: float) -> float:
        if self.s0 == self.s1 or self.x0 == -math.inf or self.x1 == math.inf:
            return self.s0
        return self.s0 + (self.s1 - self.s0) * (x - self.x0) / (self.x1 - self.x0)

    def inverse(self, y: float) -> Tuple[float, float]:
        """x-range mapping to y inside this piece (lo == hi when unique)."""
        if self.is_plateau:
            return (self.x0, self.x1)
        if self.x0 == -math.inf:
            x = self.x1 + (y - self.y1) / self.s1
            return (x, x)
        if self.x1 == math.inf:
            x = self.x0 + (y - self.y0) / self.s0
            return (x, x)
        if self.s0 == self.s1:
            x = self.x0 + (y - self.y0) / self.s0
            return (x, x)
        # quadratic: y0 + s0*d + a*d^2 = y, a = (s1-s0)/(2w), d in [0, w]
        w = self.x1 - self.x0
        a = (self.s1 - self.s0) / (2.0 * w)
        disc = self.s0 * self.s0 + 4.0 * a * (y - self.y0)
        d = (-self.s0 + math.sqrt(max(disc, 0.0))) / (2.0 * a)
        d = min(max(d, 0.0), w)
        x = self.x0 + d
        return (x, x)


@dataclass(frozen=True)
class CheckResult:
    name: str
    ok: bool
    detail: str


class CompiledCurve:
    """Immutable compiled static curve. Build via compile_curve()."""

    def __init__(self, pieces: List[_Piece], checks: List[CheckResult]):
        self._pieces = pieces
        self._x0s = [p.x0 for p in pieces]
        self.checks = checks

    # ---- evaluation -----------------------------------------------------
    def _piece_index(self, x: float) -> int:
        i = bisect.bisect_right(self._x0s, x) - 1
        return min(max(i, 0), len(self._pieces) - 1)

    def output_db(self, x_db: float) -> float:
        """Output level (dBFS) for input level x_db."""
        if x_db <= DB_FLOOR:
            x_db = DB_FLOOR
        return self._pieces[self._piece_index(x_db)].eval(x_db)

    def gain_db(self, x_db: float) -> float:
        """Static gain (dB) for input level x_db. Always finite."""
        if x_db <= DB_FLOOR:
            x_db = DB_FLOOR
        return self.output_db(x_db) - x_db

    def slope_db(self, x_db: float) -> float:
        if x_db <= DB_FLOOR:
            x_db = DB_FLOOR
        return self._pieces[self._piece_index(x_db)].slope(x_db)

    # ---- inverse --------------------------------------------------------
    def inverse_db(self, y_db: float) -> Optional[Tuple[float, float]]:
        """Inverse lookup: input-level interval producing output y_db.

        Returns (lo, hi); lo == hi when the inverse is unique. A zero-slope
        plateau yields lo < hi (non-invertible region). Returns None when
        y_db is outside the curve's output range.
        """
        lo = hi = None
        for p in self._pieces:
            if p.is_plateau:
                # plateau output is the single level y0 (y1 may be +inf)
                if abs(y_db - p.y0) <= CONTINUITY_TOL:
                    a, b = p.x0, p.x1
                    lo = a if lo is None else min(lo, a)
                    hi = b if hi is None else max(hi, b)
                continue
            ylo, yhi = (p.y0, p.y1) if p.y0 <= p.y1 else (p.y1, p.y0)
            if ylo - CONTINUITY_TOL <= y_db <= yhi + CONTINUITY_TOL:
                a, b = p.inverse(y_db)
                lo = a if lo is None else min(lo, a)
                hi = b if hi is None else max(hi, b)
        if lo is None:
            return None
        return (lo, hi)

    def plateaus(self) -> List[Tuple[float, float, float]]:
        """Non-invertible plateau regions as (x0, x1, y) triples."""
        return [(p.x0, p.x1, p.y0) for p in self._pieces if p.is_plateau]

    def is_invertible_at(self, y_db: float) -> bool:
        r = self.inverse_db(y_db)
        return r is not None and r[1] - r[0] <= CONTINUITY_TOL


def _region_slopes(segs: Sequence[Segment]) -> List[float]:
    """Slope of each open region between sorted thresholds.

    Region j spans (T_j, T_{j+1}) with T_0 = -inf, T_{n+1} = +inf. The slope
    of a region is 1/R of the compress segment at its left edge, else R of
    the expand segment at its right edge, else 1.0 (standard multiband
    compander semantics: each region is governed by its own segment).
    """
    slopes = []
    for j in range(len(segs) + 1):
        left_seg = segs[j - 1] if j > 0 else None
        right_seg = segs[j] if j < len(segs) else None
        if left_seg is not None and left_seg.kind == "compress":
            slopes.append(0.0 if left_seg.ratio == math.inf else 1.0 / left_seg.ratio)
        elif right_seg is not None and right_seg.kind == "expand":
            slopes.append(right_seg.ratio)
        else:
            slopes.append(1.0)
    return slopes


def compile_curve(segments: Sequence[Segment]) -> CompiledCurve:
    """Compile segment specs into a validated CompiledCurve."""
    if not segments:
        raise ValueError("at least one segment is required")
    segs = sorted(segments, key=lambda s: s.threshold_db)
    for a, b in zip(segs, segs[1:]):
        if b.threshold_db - a.threshold_db <= KNEE_GUARD:
            raise ValueError("duplicate/degenerate thresholds")
    for i, seg in enumerate(segs):
        half = seg.knee_db / 2.0
        if i > 0 and half >= seg.threshold_db - segs[i - 1].threshold_db:
            raise ValueError(f"knee at {seg.threshold_db} dB overlaps previous knot")
        if i < len(segs) - 1 and half >= segs[i + 1].threshold_db - seg.threshold_db:
            raise ValueError(f"knee at {seg.threshold_db} dB overlaps next knot")

    slopes = _region_slopes(segs)
    # anchor: zero gain at the lowest threshold, propagate across regions
    yknot = [segs[0].threshold_db]
    for i in range(1, len(segs)):
        yknot.append(yknot[-1] + slopes[i] * (segs[i].threshold_db - segs[i - 1].threshold_db))

    pieces: List[_Piece] = []
    for i, seg in enumerate(segs):
        half = seg.knee_db / 2.0
        x_edge = seg.threshold_db - half
        y_edge = yknot[i] - slopes[i] * half
        if not pieces:  # leading region (-inf, first knee edge)
            pieces.append(_Piece(-math.inf, x_edge, -math.inf, y_edge, slopes[0], slopes[0]))
        else:           # linear region between previous edge and this knee
            pieces.append(_Piece(pieces[-1].x1, x_edge, pieces[-1].y1, y_edge,
                                 slopes[i], slopes[i]))
        if seg.knee_db > 0.0:  # quadratic soft-knee blend around the knot
            pieces.append(_Piece(x_edge, seg.threshold_db + half,
                                 y_edge, yknot[i] + slopes[i + 1] * half,
                                 slopes[i], slopes[i + 1]))
    # trailing region (last knee edge, +inf)
    half_last = segs[-1].knee_db / 2.0
    pieces.append(_Piece(segs[-1].threshold_db + half_last, math.inf,
                         yknot[-1] + slopes[-1] * half_last, math.inf,
                         slopes[-1], slopes[-1]))

    checks = _validate(pieces)
    bad = [c for c in checks if not c.ok]
    if bad:
        raise ValueError("curve validation failed: " + "; ".join(c.detail for c in bad))
    return CompiledCurve(pieces, checks)


def _validate(pieces: List[_Piece]) -> List[CheckResult]:
    checks: List[CheckResult] = []
    # value continuity at every internal boundary
    worst_v = 0.0
    for a, b in zip(pieces, pieces[1:]):
        worst_v = max(worst_v, abs(a.eval(a.x1) - b.eval(b.x0)))
    checks.append(CheckResult(
        "value_continuity", worst_v <= CONTINUITY_TOL,
        f"max value jump at boundaries = {worst_v:.3e} (tol {CONTINUITY_TOL:.1e})"))
    # slope conditions: continuous across soft knees (quadratic left piece)
    worst_s = 0.0
    for a, b in zip(pieces, pieces[1:]):
        if a.s0 != a.s1:
            worst_s = max(worst_s, abs(a.slope(a.x1) - b.slope(b.x0)))
    checks.append(CheckResult(
        "slope_conditions", worst_s <= SLOPE_TOL,
        f"max slope mismatch across soft knees = {worst_s:.3e} (tol {SLOPE_TOL:.1e})"))
    # monotonicity: every piece non-decreasing
    min_slope = min(min(p.s0, p.s1) for p in pieces)
    checks.append(CheckResult(
        "monotonicity", min_slope >= 0.0,
        f"minimum piece slope = {min_slope:.6g} (must be >= 0)"))
    return checks
