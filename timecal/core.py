"""Distinguishes wall-clock times, generic time points and durations.

All values are exact rationals (Fraction); nothing is ever converted to
float, so huge epoch timestamps keep full precision under arithmetic.
"""

from __future__ import annotations

from enum import Enum
from fractions import Fraction

from .rounding import RoundingPolicy, round_fraction
from .units import SECOND, TimeUnit


class ClockKind(Enum):
    WALL = "wall"              # civil/epoch time (e.g. UTC)
    MONOTONIC = "monotonic"    # steady local clock
    SAMPLE = "sample"          # free-running sampling/tick clock


class ClockDomain:
    __slots__ = ("name", "kind")

    def __init__(self, name: str, kind: ClockKind):
        self.name = name
        self.kind = kind

    def __repr__(self):
        return f"ClockDomain({self.name!r}, {self.kind.value})"

    def __eq__(self, other):
        return (
            isinstance(other, ClockDomain)
            and (self.name, self.kind) == (other.name, other.kind)
        )

    def __hash__(self):
        return hash((self.name, self.kind))


UTC = ClockDomain("UTC", ClockKind.WALL)


class Duration:
    """A signed length of time; exact rational seconds."""

    __slots__ = ("_seconds",)

    def __init__(self, seconds=0):
        self._seconds = Fraction(seconds)

    @classmethod
    def of(cls, value, unit: TimeUnit) -> "Duration":
        return cls(Fraction(value) * unit.seconds_per_unit)

    @property
    def seconds(self) -> Fraction:
        return self._seconds

    def to(self, unit: TimeUnit) -> Fraction:
        return self._seconds / unit.seconds_per_unit

    def to_integer(self, unit: TimeUnit, policy=RoundingPolicy.HALF_EVEN) -> int:
        return round_fraction(self.to(unit), policy)

    def __add__(self, other):
        if isinstance(other, Duration):
            return Duration(self._seconds + other._seconds)
        return NotImplemented

    __radd__ = __add__

    def __sub__(self, other):
        if isinstance(other, Duration):
            return Duration(self._seconds - other._seconds)
        return NotImplemented

    def __neg__(self):
        return Duration(-self._seconds)

    def __abs__(self):
        return Duration(abs(self._seconds))

    def __mul__(self, factor):
        return Duration(self._seconds * Fraction(factor))

    __rmul__ = __mul__

    def __truediv__(self, other):
        if isinstance(other, Duration):
            return self._seconds / other._seconds
        return Duration(self._seconds / Fraction(other))

    def _cmp(self, other, op):
        if not isinstance(other, Duration):
            return NotImplemented
        return op(self._seconds, other._seconds)

    def __eq__(self, other):
        return self._cmp(other, lambda a, b: a == b)

    def __lt__(self, other):
        return self._cmp(other, lambda a, b: a < b)

    def __le__(self, other):
        return self._cmp(other, lambda a, b: a <= b)

    def __gt__(self, other):
        return self._cmp(other, lambda a, b: a > b)

    def __ge__(self, other):
        return self._cmp(other, lambda a, b: a >= b)

    def __hash__(self):
        return hash(self._seconds)

    def __repr__(self):
        return f"Duration({self._seconds}s)"


class TimePoint:
    """An instant on a specific clock domain; exact seconds since its epoch."""

    __slots__ = ("_domain", "_since_epoch")

    def __init__(self, domain: ClockDomain, since_epoch=0):
        self._domain = domain
        self._since_epoch = Fraction(since_epoch)

    @classmethod
    def at(cls, domain, value, unit: TimeUnit = SECOND) -> "TimePoint":
        return cls(domain, Fraction(value) * unit.seconds_per_unit)

    @property
    def domain(self) -> ClockDomain:
        return self._domain

    def since_epoch(self, unit: TimeUnit = SECOND) -> Fraction:
        return self._since_epoch / unit.seconds_per_unit

    def __add__(self, duration):
        if isinstance(duration, Duration):
            return type(self)(self._domain, self._since_epoch + duration.seconds)
        return NotImplemented

    __radd__ = __add__

    def __sub__(self, other):
        if isinstance(other, Duration):
            return type(self)(self._domain, self._since_epoch - other.seconds)
        if isinstance(other, TimePoint):
            self._require_same_domain(other)
            return Duration(self._since_epoch - other._since_epoch)
        return NotImplemented

    def _require_same_domain(self, other):
        if self._domain != other._domain:
            raise ValueError(
                f"clock domain mismatch: {self._domain.name} vs {other._domain.name}"
            )

    def _cmp(self, other, op):
        if not isinstance(other, TimePoint):
            return NotImplemented
        self._require_same_domain(other)
        return op(self._since_epoch, other._since_epoch)

    def __eq__(self, other):
        return self._cmp(other, lambda a, b: a == b)

    def __lt__(self, other):
        return self._cmp(other, lambda a, b: a < b)

    def __le__(self, other):
        return self._cmp(other, lambda a, b: a <= b)

    def __gt__(self, other):
        return self._cmp(other, lambda a, b: a > b)

    def __ge__(self, other):
        return self._cmp(other, lambda a, b: a >= b)

    def __hash__(self):
        return hash((self._domain, self._since_epoch))

    def __repr__(self):
        return f"{type(self).__name__}({self._domain.name}+{self._since_epoch}s)"


class WallClockTime(TimePoint):
    """A TimePoint on a wall-clock domain (civil epoch time)."""

    def __init__(self, domain: ClockDomain, since_epoch=0):
        if domain.kind is not ClockKind.WALL:
            raise ValueError("WallClockTime requires a WALL clock domain")
        super().__init__(domain, since_epoch)

    @classmethod
    def at(cls, domain, value, unit: TimeUnit = SECOND) -> "WallClockTime":
        return cls(domain, Fraction(value) * unit.seconds_per_unit)
