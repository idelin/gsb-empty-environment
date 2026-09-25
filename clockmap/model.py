"""Slope model and calibration anchors.

The sample clock is never assumed to run at exactly the nominal rate: its
period (wall seconds per sample) is bounded by ``[min_period, max_period]``
around the nominal period, which models drift. A calibration *anchor* pins a
sequence number to a wall-clock *error interval* rather than an exact point.
"""
from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction

from .timeprims import exact


@dataclass(frozen=True)
class SlopeModel:
    """Bounds on the sample-clock period, in wall seconds per sample."""

    nominal_period: Fraction
    min_period: Fraction
    max_period: Fraction

    def __post_init__(self):
        object.__setattr__(self, "nominal_period", exact(self.nominal_period))
        object.__setattr__(self, "min_period", exact(self.min_period))
        object.__setattr__(self, "max_period", exact(self.max_period))
        if not (0 < self.min_period <= self.nominal_period <= self.max_period):
            raise ValueError("need 0 < min_period <= nominal_period <= max_period")

    @classmethod
    def from_ppm(cls, sample_rate_hz: int, ppm: int) -> "SlopeModel":
        """Nominal rate plus/minus ``ppm`` parts per million of drift."""
        nominal = Fraction(1, sample_rate_hz)
        tol = Fraction(ppm, 1_000_000)
        return cls(nominal, nominal * (1 - tol), nominal * (1 + tol))


@dataclass(frozen=True)
class Anchor:
    """A calibration point: ``seq`` maps to wall time in ``[wall_lo, wall_hi]``.

    ``epoch`` identifies the clock epoch; anchors from different epochs are
    never combined (a clock reset cannot be bridged).
    """

    epoch: int
    seq: int
    wall_lo: Fraction
    wall_hi: Fraction
    label: str = ""

    def __post_init__(self):
        object.__setattr__(self, "wall_lo", exact(self.wall_lo))
        object.__setattr__(self, "wall_hi", exact(self.wall_hi))

    @property
    def self_consistent(self) -> bool:
        return self.wall_lo <= self.wall_hi

    def implies(self, seq: int, slope: SlopeModel) -> tuple[Fraction, Fraction]:
        """Feasible wall interval at ``seq`` implied by this anchor alone."""
        delta = seq - self.seq
        if delta >= 0:
            return (
                self.wall_lo + delta * slope.min_period,
                self.wall_hi + delta * slope.max_period,
            )
        return (
            self.wall_lo + delta * slope.max_period,
            self.wall_hi + delta * slope.min_period,
        )

    def name(self) -> str:
        return self.label or f"anchor(epoch={self.epoch}, seq={self.seq})"
