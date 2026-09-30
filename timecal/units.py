"""Time units (s, ms, us, ns, sampling-clock ticks) with exact rational math."""

from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction

from .rounding import RoundingPolicy, round_fraction


@dataclass(frozen=True)
class TimeUnit:
    """A time unit defined by its exact size in seconds (a Fraction)."""

    name: str
    seconds_per_unit: Fraction

    def __post_init__(self):
        object.__setattr__(self, "seconds_per_unit", Fraction(self.seconds_per_unit))
        if self.seconds_per_unit <= 0:
            raise ValueError("unit size must be positive")

    @staticmethod
    def sampling(rate_num, rate_den=1, name=None) -> "TimeUnit":
        """Unit of one tick of a sampling clock with a rational rate in Hz."""
        rate = Fraction(rate_num, rate_den)
        if rate <= 0:
            raise ValueError("sampling rate must be positive")
        return TimeUnit(name or f"tick@{rate}Hz", 1 / rate)


SECOND = TimeUnit("s", Fraction(1))
MILLISECOND = TimeUnit("ms", Fraction(1, 1_000))
MICROSECOND = TimeUnit("us", Fraction(1, 1_000_000))
NANOSECOND = TimeUnit("ns", Fraction(1, 1_000_000_000))


def to_seconds(value, unit: TimeUnit) -> Fraction:
    return Fraction(value) * unit.seconds_per_unit


def from_seconds(seconds, unit: TimeUnit) -> Fraction:
    """Exact value in `unit`; may be fractional - no rounding is applied."""
    return Fraction(seconds) / unit.seconds_per_unit


def convert_exact(value, from_unit: TimeUnit, to_unit: TimeUnit) -> Fraction:
    return from_seconds(to_seconds(value, from_unit), to_unit)


def convert_rounded(value, from_unit, to_unit, policy=RoundingPolicy.HALF_EVEN) -> int:
    return round_fraction(convert_exact(value, from_unit, to_unit), policy)


def compare(value_a, unit_a, value_b, unit_b) -> int:
    """-1/0/1 comparison of two quantities in different units, no floats."""
    a = to_seconds(value_a, unit_a)
    b = to_seconds(value_b, unit_b)
    return (a > b) - (a < b)
