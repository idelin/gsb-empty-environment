"""Intervals with correlated error sources.

An :class:`ErrorBudget` is an exact center value plus a symbolic sum of
signed error contributions, one per *source* (e.g. one per calibration
edge).  Composing two budgets merges contributions by source id, so errors
that share a source stay correlated (their signed coefficients add and may
cancel), while independent sources accumulate worst-case widths.  No floats
are involved anywhere.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from fractions import Fraction


@dataclass(frozen=True)
class ErrorBudget:
    """center +/- sum(|coeff|) where coeffs are keyed by error source id."""

    center: Fraction
    terms: dict = field(default_factory=dict)  # source id -> signed Fraction

    def __post_init__(self):
        object.__setattr__(self, "center", Fraction(self.center))
        cleaned = {
            key: Fraction(coeff)
            for key, coeff in self.terms.items()
            if Fraction(coeff) != 0
        }
        object.__setattr__(self, "terms", cleaned)

    @classmethod
    def exact(cls, value) -> "ErrorBudget":
        return cls(Fraction(value), {})

    @property
    def half_width(self) -> Fraction:
        return sum((abs(c) for c in self.terms.values()), Fraction(0))

    @property
    def lo(self) -> Fraction:
        return self.center - self.half_width

    @property
    def hi(self) -> Fraction:
        return self.center + self.half_width

    def interval(self) -> tuple:
        return (self.lo, self.hi)

    def scale(self, factor) -> "ErrorBudget":
        factor = Fraction(factor)
        return ErrorBudget(
            self.center * factor,
            {key: coeff * factor for key, coeff in self.terms.items()},
        )

    def shift(self, delta) -> "ErrorBudget":
        return ErrorBudget(self.center + Fraction(delta), dict(self.terms))

    def add_term(self, source: str, coeff) -> "ErrorBudget":
        terms = dict(self.terms)
        terms[source] = terms.get(source, Fraction(0)) + Fraction(coeff)
        return ErrorBudget(self.center, terms)

    def combine(self, other: "ErrorBudget") -> "ErrorBudget":
        """Add two budgets, keeping same-source errors correlated."""
        terms = dict(self.terms)
        for key, coeff in other.terms.items():
            terms[key] = terms.get(key, Fraction(0)) + coeff
        return ErrorBudget(self.center + other.center, terms)

    def overlaps(self, other: "ErrorBudget") -> bool:
        return self.lo <= other.hi and other.lo <= self.hi

    def __repr__(self) -> str:
        return (
            f"ErrorBudget(center={self.center}, "
            f"half_width={self.half_width}, sources={sorted(self.terms)})"
        )
