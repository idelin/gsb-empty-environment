"""Restricted piecewise functions: continuous piecewise-linear (PL) maps.

A PiecewiseLinear is given explicitly by sorted breakpoints and the
values at those breakpoints; it is linear between consecutive
breakpoints and undefined outside [breakpoints[0], breakpoints[-1]].

Interval/affine evaluation rule:
  * If the input interval contains no interior breakpoint (kink), the
    map is exactly linear there and is applied exactly (correlations
    preserved).
  * Otherwise the interval straddles kinks: the exact range of the PL
    map over the interval is computed by scanning the endpoints AND all
    interior kinks (never just the endpoints), and a conservative
    envelope (center + fresh noise symbol) is returned.
"""

from __future__ import annotations

from bisect import bisect_right


class PiecewiseDomainError(ValueError):
    """Raised when an input falls outside the PL function's domain."""


class PiecewiseLinear:
    """Continuous piecewise-linear function with explicit breakpoints."""

    def __init__(self, breakpoints, values):
        breakpoints = [float(b) for b in breakpoints]
        values = [float(v) for v in values]
        if len(breakpoints) != len(values):
            raise ValueError(
                f"size mismatch: {len(breakpoints)} breakpoints vs {len(values)} values"
            )
        if len(breakpoints) < 2:
            raise ValueError("need at least 2 breakpoints")
        for i in range(len(breakpoints) - 1):
            if not breakpoints[i + 1] > breakpoints[i]:
                raise ValueError("breakpoints must be strictly increasing")
        self.breakpoints = breakpoints
        self.values = values

    @property
    def domain(self) -> tuple[float, float]:
        return (self.breakpoints[0], self.breakpoints[-1])

    def _segment_index(self, x: float) -> int:
        i = bisect_right(self.breakpoints, x) - 1
        return max(0, min(i, len(self.breakpoints) - 2))

    def slope_intercept(self, i: int) -> tuple[float, float]:
        x0, x1 = self.breakpoints[i], self.breakpoints[i + 1]
        y0, y1 = self.values[i], self.values[i + 1]
        slope = (y1 - y0) / (x1 - x0)
        return slope, y0 - slope * x0

    def eval(self, x: float) -> float:
        lo, hi = self.domain
        if x < lo or x > hi:
            raise PiecewiseDomainError(
                f"point {x!r} outside piecewise domain [{lo!r}, {hi!r}]"
            )
        slope, intercept = self.slope_intercept(self._segment_index(x))
        return slope * x + intercept

    def linear_on(self, lo: float, hi: float) -> tuple[float, float] | None:
        """(slope, intercept) if [lo, hi] crosses no interior kink, else None."""
        dlo, dhi = self.domain
        if lo < dlo or hi > dhi:
            raise PiecewiseDomainError(
                f"interval [{lo!r}, {hi!r}] outside piecewise domain [{dlo!r}, {dhi!r}]"
            )
        for b in self.breakpoints[1:-1]:
            if lo < b < hi:
                return None
        return self.slope_intercept(self._segment_index(lo))

    def range_on(self, lo: float, hi: float) -> tuple[float, float]:
        """Exact range over [lo, hi]; scans endpoints AND interior kinks."""
        dlo, dhi = self.domain
        if lo < dlo or hi > dhi:
            raise PiecewiseDomainError(
                f"interval [{lo!r}, {hi!r}] outside piecewise domain [{dlo!r}, {dhi!r}]"
            )
        points = [lo, hi]
        points.extend(b for b in self.breakpoints if lo < b < hi)
        vals = [self.eval(p) for p in points]
        return min(vals), max(vals)


def abs_pl() -> PiecewiseLinear:
    return PiecewiseLinear([-1.0e30, 0.0, 1.0e30], [1.0e30, 0.0, 1.0e30])


def relu_pl() -> PiecewiseLinear:
    return PiecewiseLinear([-1.0e30, 0.0, 1.0e30], [0.0, 0.0, 1.0e30])
