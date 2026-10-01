"""Piecewise-constant sampled streams (epochs of rational sample rate)."""
from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction

from .types import TimePoint


@dataclass(frozen=True)
class SamplingBlock:
    """One epoch of constant rational-rate sampling.

    Sample ``k`` is stamped ``origin + k / rate`` where ``rate`` is an exact
    rational number of samples per second.  The block covers the half-open
    span ``[start, end)`` (``end=None`` means unbounded).  Indices listed in
    ``missing`` were dropped by the source: they are reported as such and
    never back-filled with fabricated zeros.

    ``(epoch, version)`` identifies the block and drives the de-duplication
    rule when several blocks stamp the same instant.
    """

    epoch: int
    version: int
    origin: TimePoint
    rate: Fraction
    start: TimePoint
    end: TimePoint | None = None
    missing: frozenset = frozenset()

    def __post_init__(self):
        rate = self.rate if isinstance(self.rate, Fraction) else Fraction(self.rate)
        if rate <= 0:
            raise ValueError("sample rate must be positive")
        object.__setattr__(self, "rate", rate)
        object.__setattr__(self, "missing", frozenset(self.missing))
        if self.end is not None and self.end.value <= self.start.value:
            raise ValueError("block end must be after its start")

    @property
    def key(self) -> tuple:
        return (self.epoch, self.version)

    @property
    def period(self) -> Fraction:
        """Exact seconds per sample."""
        return Fraction(1, 1) / self.rate

    def timestamp(self, index: int) -> TimePoint:
        return TimePoint(self.origin.value + index * self.period)
