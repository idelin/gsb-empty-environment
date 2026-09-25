"""Model objects: calibration anchors, slope bounds and piecewise segments."""

from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction
from typing import Optional, Tuple

from .timetypes import WallClock


@dataclass(frozen=True)
class SlopeBounds:
    """Bounds on the sample-clock slope, in wall-clock seconds per sample.

    ``min_slope <= actual_period <= max_slope`` must hold for the true
    (unknown) sample clock.  Both bounds are exact rationals.
    """

    min_slope: Fraction
    max_slope: Fraction

    def __post_init__(self):
        object.__setattr__(self, "min_slope", Fraction(self.min_slope))
        object.__setattr__(self, "max_slope", Fraction(self.max_slope))
        if self.min_slope <= 0:
            raise ValueError("min_slope must be positive")
        if self.min_slope > self.max_slope:
            raise ValueError("min_slope must not exceed max_slope")

    @classmethod
    def from_nominal_rate(cls, nominal_rate, drift=0) -> "SlopeBounds":
        """Build bounds from a nominal sample rate and a relative drift.

        ``nominal_rate`` is samples per second (exact rational),
        ``drift`` is a relative tolerance, e.g. ``Fraction(100, 1_000_000)``
        for 100 ppm.  The true period is assumed to lie in
        ``[period*(1-drift), period*(1+drift)]``.
        """
        rate = Fraction(nominal_rate)
        if rate <= 0:
            raise ValueError("nominal_rate must be positive")
        drift = Fraction(drift)
        if drift < 0 or drift >= 1:
            raise ValueError("drift must be in [0, 1)")
        period = Fraction(1, 1) / rate
        return cls(period * (1 - drift), period * (1 + drift))


@dataclass(frozen=True)
class Anchor:
    """A calibration point: sample ``index`` of ``epoch`` was observed at a
    wall-clock time known to lie in ``[earliest, latest]``.

    Calibration points carry an uncertainty *interval*, never an exact point.
    """

    epoch: int
    index: int
    earliest: WallClock
    latest: WallClock
    label: str = ""

    def __post_init__(self):
        if self.epoch < 0:
            raise ValueError("epoch must be non-negative")
        if self.index < 0:
            raise ValueError("index must be non-negative")
        if self.earliest > self.latest:
            raise ValueError(
                f"anchor {self.label!r} has earliest > latest; "
                "a single malformed anchor is a data error, not a conflict"
            )


@dataclass(frozen=True)
class Line:
    """An exact affine bound line ``t = slope * index + offset``."""

    slope: Fraction   # seconds per sample
    offset: Fraction  # seconds at index 0

    def at(self, index: int) -> Fraction:
        return self.slope * index + self.offset


@dataclass(frozen=True)
class Bound:
    """A piecewise-linear bound: the max (lower bound) or min (upper bound)
    of a small set of :class:`Line` objects."""

    kind: str  # "max" for lower bounds, "min" for upper bounds
    lines: Tuple[Line, ...]

    def __post_init__(self):
        if self.kind not in ("max", "min"):
            raise ValueError("kind must be 'max' or 'min'")
        if not self.lines:
            raise ValueError("a bound needs at least one line")

    def at(self, index: int) -> Fraction:
        values = [line.at(index) for line in self.lines]
        return max(values) if self.kind == "max" else min(values)


@dataclass(frozen=True)
class Segment:
    """One piece of the piecewise mapping of an epoch.

    Valid for ``first_index <= n <= last_index`` (``None`` means the range
    extends to infinity on that side).  ``lower``/``upper`` give the exact
    feasible wall-clock band for any sample index in the range.
    """

    epoch: int
    first_index: Optional[int]
    last_index: Optional[int]
    lower: Bound
    upper: Bound

    def contains(self, index: int) -> bool:
        if self.first_index is not None and index < self.first_index:
            return False
        if self.last_index is not None and index > self.last_index:
            return False
        return True

    def band_at(self, index: int):
        """Return ``(lo, hi)`` as exact Fractions of seconds."""
        return self.lower.at(index), self.upper.at(index)
