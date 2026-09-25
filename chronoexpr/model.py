"""Exact time model.

Three distinct kinds of values are modelled:

- ``WallClock``: a naive local date-time (no offset attached).
- ``Instant``:   an exact point on the UTC timeline, stored as a
                 ``Fraction`` of seconds since 1970-01-01T00:00:00Z.
- ``Duration``:  a length of time with three orthogonal components:
                 calendar months, calendar days and fixed seconds.

A synthetic :class:`OffsetTable` maps between wall clocks and instants.
"""

from __future__ import annotations

import bisect
from dataclasses import dataclass
from fractions import Fraction

from .civil import civil_from_days, days_from_civil, days_in_month

SECONDS_PER_DAY = 86_400
MAX_COMPONENT = 1 << 62  # overflow limit for duration components and instants


def _fmt_rational(value: Fraction) -> str:
    """Canonical text for a non-negative rational: decimal if it terminates,
    otherwise ``p/q``."""
    if value.denominator == 1:
        return str(value.numerator)
    den = value.denominator
    rest = den
    twos = 0
    while rest % 2 == 0:
        rest //= 2
        twos += 1
    fives = 0
    while rest % 5 == 0:
        rest //= 5
        fives += 1
    if rest == 1:
        digits = max(twos, fives)
        scaled = value.numerator * (10 ** digits) // den
        text = str(abs(scaled)).rjust(digits + 1, "0")
        sign = "-" if scaled < 0 else ""
        return f"{sign}{text[:-digits]}.{text[-digits:].rstrip('0')}"
    return f"{value.numerator}/{value.denominator}"


@dataclass(frozen=True)
class WallClock:
    """A naive local date-time; exact to a rational fraction of a second."""

    year: int
    month: int
    day: int
    hour: int
    minute: int
    second: Fraction = Fraction(0)

    def __post_init__(self) -> None:
        if not 1 <= self.month <= 12:
            raise ValueError(f"month {self.month} out of range 1..12")
        if not 1 <= self.day <= days_in_month(self.year, self.month):
            raise ValueError(f"day {self.day} out of range for {self.year}-{self.month:02d}")
        if not 0 <= self.hour <= 23:
            raise ValueError(f"hour {self.hour} out of range 0..23")
        if not 0 <= self.minute <= 59:
            raise ValueError(f"minute {self.minute} out of range 0..59")
        if not 0 <= self.second < 60:
            raise ValueError(f"second {self.second} out of range [0, 60)")

    def add_months(self, count: int) -> "WallClock":
        """Calendar month addition with month-end clamping."""
        total = self.year * 12 + (self.month - 1) + count
        year, month0 = divmod(total, 12)
        month = month0 + 1
        day = min(self.day, days_in_month(year, month))
        return WallClock(year, month, day, self.hour, self.minute, self.second)

    def add_days(self, count: int) -> "WallClock":
        """Calendar day addition (date arithmetic, time-of-day preserved)."""
        serial = days_from_civil(self.year, self.month, self.day) + count
        year, month, day = civil_from_days(serial)
        return WallClock(year, month, day, self.hour, self.minute, self.second)

    def to_epoch_seconds(self) -> Fraction:
        """Seconds since the epoch *as if* this wall clock were UTC."""
        days = days_from_civil(self.year, self.month, self.day)
        return Fraction(days * SECONDS_PER_DAY + self.hour * 3600 + self.minute * 60) + self.second

    @staticmethod
    def from_epoch_seconds(seconds: Fraction) -> "WallClock":
        days = int(seconds // SECONDS_PER_DAY)
        rem = seconds - days * SECONDS_PER_DAY
        year, month, day = civil_from_days(days)
        hour = int(rem // 3600)
        rem -= hour * 3600
        minute = int(rem // 60)
        return WallClock(year, month, day, hour, minute, rem - minute * 60)

    def iso(self) -> str:
        whole = int(self.second)
        text = f"{whole:02d}"
        frac = self.second - whole
        if frac:
            rendered = _fmt_rational(frac)
            text += rendered[1:] if rendered.startswith("0") else f"[+{rendered}]"
        return (
            f"{self.year:04d}-{self.month:02d}-{self.day:02d}"
            f"T{self.hour:02d}:{self.minute:02d}:{text}"
        )


@dataclass(frozen=True)
class Instant:
    """An exact point on the UTC timeline (Fraction seconds since the epoch)."""

    seconds: Fraction

    def iso(self) -> str:
        return WallClock.from_epoch_seconds(self.seconds).iso() + "Z"


@dataclass(frozen=True)
class Duration:
    """Calendar months + calendar days + fixed seconds.

    A negative duration negates *all* components (never a single field),
    so ``-1mo2d`` is unambiguous.  Mixed-sign results of subtraction are
    printed as an explicit subtraction, e.g. ``1mo - 2h``.
    """

    months: int = 0
    days: int = 0
    seconds: Fraction = Fraction(0)

    def __add__(self, other: "Duration") -> "Duration":
        return Duration(self.months + other.months, self.days + other.days,
                        self.seconds + other.seconds)

    def __sub__(self, other: "Duration") -> "Duration":
        return self + (-other)

    def __neg__(self) -> "Duration":
        return Duration(-self.months, -self.days, -self.seconds)

    def is_zero(self) -> bool:
        return not self.months and not self.days and not self.seconds

    def canonical(self) -> str:
        if self.is_zero():
            return "0s"
        signs = set()
        for comp in (self.months, self.days, self.seconds):
            if comp > 0:
                signs.add(1)
            elif comp < 0:
                signs.add(-1)
        if signs == {-1}:
            return "-" + (-self)._unsigned()
        if signs == {1}:
            return self._unsigned()
        pos = Duration(max(self.months, 0), max(self.days, 0),
                       max(self.seconds, Fraction(0)))
        neg = Duration(min(self.months, 0), min(self.days, 0),
                       min(self.seconds, Fraction(0)))
        return f"{pos._unsigned()} - {(-neg)._unsigned()}"

    def _unsigned(self) -> str:
        parts: list[str] = []
        if self.months:
            parts.append(f"{self.months}mo")
        if self.days:
            parts.append(f"{self.days}d")
        if self.seconds:
            rest = self.seconds
            days = int(rest // SECONDS_PER_DAY)
            rest -= days * SECONDS_PER_DAY
            hours = int(rest // 3600)
            rest -= hours * 3600
            minutes = int(rest // 60)
            rest -= minutes * 60
            if days:
                parts.append(f"{days}sd")
            if hours:
                parts.append(f"{hours}h")
            if minutes:
                parts.append(f"{minutes}m")
            if rest:
                parts.append(f"{_fmt_rational(rest)}s")
        return "".join(parts) or "0s"


@dataclass(frozen=True)
class OffsetTransition:
    utc_start: Fraction
    offset_seconds: int


@dataclass(frozen=True)
class WallClassification:
    kind: str  # "ok" | "ambiguous" | "gap"
    instants: tuple = ()
    gap_seconds: Fraction | None = None


class OffsetTable:
    """A synthetic UTC-offset transition table."""

    def __init__(self, transitions):
        ordered = sorted(transitions, key=lambda t: t.utc_start)
        if not ordered:
            raise ValueError("offset table needs at least one transition")
        self._transitions = tuple(ordered)
        self._starts = [t.utc_start for t in ordered]

    @property
    def transitions(self) -> tuple:
        return self._transitions

    def offset_at(self, utc_seconds: Fraction) -> int:
        index = bisect.bisect_right(self._starts, utc_seconds) - 1
        return self._transitions[max(0, index)].offset_seconds

    def instant_to_wall(self, instant: Instant) -> WallClock:
        return WallClock.from_epoch_seconds(
            instant.seconds + self.offset_at(instant.seconds))

    def classify_wall(self, wall: WallClock) -> WallClassification:
        local = wall.to_epoch_seconds()
        offsets = {t.offset_seconds for t in self._transitions}
        candidates = sorted(
            {local - off for off in offsets if self.offset_at(local - off) == off})
        if len(candidates) == 1:
            return WallClassification("ok", (Instant(candidates[0]),))
        if len(candidates) == 2:
            return WallClassification(
                "ambiguous", (Instant(candidates[0]), Instant(candidates[1])))
        for prev, cur in zip(self._transitions, self._transitions[1:]):
            if cur.offset_seconds > prev.offset_seconds:
                start = cur.utc_start + prev.offset_seconds
                end = cur.utc_start + cur.offset_seconds
                if start <= local < end:
                    return WallClassification(
                        "gap", (), Fraction(cur.offset_seconds - prev.offset_seconds))
        raise AssertionError(f"cannot classify wall clock {wall.iso()}")


def _utc(year: int, month: int, day: int, hour: int = 0) -> Fraction:
    return WallClock(year, month, day, hour, 0).to_epoch_seconds()


def make_synthetic_table() -> OffsetTable:
    """Zone ``SYN``: UTC+8, springs forward to UTC+9 on 2024-03-10
    (local gap 02:00-03:00) and falls back on 2024-10-27
    (ambiguous local hour 01:00-02:00)."""
    return OffsetTable([
        OffsetTransition(Fraction(-(1 << 40)), 8 * 3600),
        OffsetTransition(_utc(2024, 3, 9, 18), 9 * 3600),
        OffsetTransition(_utc(2024, 10, 26, 17), 8 * 3600),
    ])


SYN = make_synthetic_table()
ZONES = {"SYN": SYN}
