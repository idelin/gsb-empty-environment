"""Exact duration values and canonical (normalized) formatting.

A Duration is a triple of *non-interchangeable* fields:

- ``months``   -- calendar months (no fixed length)
- ``cal_days`` -- calendar days (not always 24 h across offset changes)
- ``fixed``    -- exact fixed seconds as a Fraction (rational seconds)

Normalization only performs transforms that are provably semantics
preserving: carrying inside the fixed domain (60 s = 1 min, 60 min = 1 h,
24 h = 1 fixed day, all exact by definition), reducing fractions, and
merging like terms. It never converts between calendar and fixed fields.
"""
from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction

MAX_CALENDAR_FIELD = 10 ** 9
MAX_FIXED_SECONDS = 10 ** 18
MAX_SCALAR = 10 ** 18
MAX_DENOMINATOR = 10 ** 12

UNIT_SECONDS = {"d": 86400, "h": 3600, "min": 60, "s": 1}
CALENDAR_UNITS = ("mo", "cd")
FIXED_UNITS = ("d", "h", "min", "s")


def floor_div(value: Fraction, n: int) -> int:
    """Exact floor division of a Fraction by an int."""
    return value.numerator // (value.denominator * n)


@dataclass(frozen=True)
class Duration:
    """An exact duration: calendar months, calendar days, fixed seconds."""

    months: int = 0
    cal_days: int = 0
    fixed: Fraction = Fraction(0)

    def __add__(self, other):
        if not isinstance(other, Duration):
            return NotImplemented
        return Duration(self.months + other.months,
                        self.cal_days + other.cal_days,
                        self.fixed + other.fixed)

    def __sub__(self, other):
        if not isinstance(other, Duration):
            return NotImplemented
        return self + (-other)

    def __neg__(self):
        return Duration(-self.months, -self.cal_days, -self.fixed)

    def scale(self, factor: Fraction) -> "Duration":
        months = self.months * factor
        days = self.cal_days * factor
        if months.denominator != 1 or days.denominator != 1:
            raise ValueError("scaling would produce a fractional calendar field")
        return Duration(int(months), int(days), self.fixed * factor)

    def __str__(self):
        return format_duration(self)


def format_duration(d: Duration) -> str:
    """Canonical text form; ``parse_duration(format_duration(d)) == d``.

    A wholly negative duration is printed as ``-(...)`` so the leading
    minus unambiguously negates the entire expression. Mixed-sign values
    are printed with explicit binary ``-`` operators (positive terms
    first); reordering is safe here because duration *values* add
    commutatively, unlike compiled programs.
    """
    terms = []  # (text, is_negative)
    if d.months:
        terms.append((f"{abs(d.months)}mo", d.months < 0))
    if d.cal_days:
        terms.append((f"{abs(d.cal_days)}cd", d.cal_days < 0))
    rem = abs(d.fixed)
    neg = d.fixed < 0
    days = floor_div(rem, 86400)
    rem -= days * 86400
    hours = floor_div(rem, 3600)
    rem -= hours * 3600
    minutes = floor_div(rem, 60)
    rem -= minutes * 60
    if days:
        terms.append((f"{days}d", neg))
    if hours:
        terms.append((f"{hours}h", neg))
    if minutes:
        terms.append((f"{minutes}min", neg))
    if rem:
        if rem.denominator == 1:
            terms.append((f"{rem.numerator}s", neg))
        else:
            terms.append((f"{rem.numerator}/{rem.denominator}s", neg))
    if not terms:
        return "0s"
    if all(n for _, n in terms):
        return "-(" + " ".join(t for t, _ in terms) + ")"
    positives = [t for t, n in terms if not n]
    negatives = [t for t, n in terms if n]
    out = " ".join(positives)
    for t in negatives:
        out += f" - {t}"
    return out
