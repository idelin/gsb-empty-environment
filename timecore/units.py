"""Time units as exact rational scales relative to the second.

A unit is defined by ``scale`` = number of seconds per single tick of the
unit.  Sample-clock ticks are units too (e.g. 1/48000 s at 48 kHz), so
milliseconds, microseconds and hardware sample counts are unified in one
exact rational framework.
"""

from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction


@dataclass(frozen=True)
class Unit:
    """A time unit: ``scale`` seconds per tick."""

    name: str
    scale: Fraction  # seconds per tick, exact

    def ticks_to_seconds(self, ticks) -> Fraction:
        return Fraction(ticks) * self.scale

    def seconds_to_ticks(self, seconds) -> Fraction:
        return Fraction(seconds) / self.scale


SECOND = Unit("s", Fraction(1))
MILLISECOND = Unit("ms", Fraction(1, 1_000))
MICROSECOND = Unit("us", Fraction(1, 1_000_000))
NANOSECOND = Unit("ns", Fraction(1, 1_000_000_000))


def sample_unit(rate: int, name: str | None = None) -> Unit:
    """Unit of one sample period of a clock running at ``rate`` Hz."""
    if rate <= 0:
        raise ValueError("sample rate must be positive")
    return Unit(name or f"tick@{rate}Hz", Fraction(1, rate))


SAMPLE_48K = sample_unit(48_000, "tick@48kHz")
SAMPLE_44K1 = sample_unit(44_100, "tick@44.1kHz")
