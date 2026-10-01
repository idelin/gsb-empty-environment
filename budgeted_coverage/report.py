"""Heuristic-vs-exact gap reporting.

The gap is reported descriptively only.  No approximation guarantee is
claimed or implied for the greedy heuristic on this extended problem
(dependencies, conflicts and minimum-coverage requirements break the
classic submodular analysis).
"""
from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction
from typing import Optional

NO_GUARANTEE_NOTE = (
    "Gap reported descriptively only; no approximation guarantee is claimed "
    "or implied for the heuristic on this problem variant."
)


@dataclass
class GapReport:
    heuristic_value: Fraction
    reference_value: Fraction
    absolute_gap: Fraction
    relative_gap: Optional[Fraction]  # None when the reference value is zero
    note: str

    def __str__(self) -> str:
        rel = "n/a (reference is 0)" if self.relative_gap is None else str(self.relative_gap)
        return (
            f"heuristic={self.heuristic_value} reference={self.reference_value} "
            f"abs_gap={self.absolute_gap} rel_gap={rel}\nnote: {self.note}"
        )


def gap_report(heuristic_value, reference_value) -> GapReport:
    hv = Fraction(heuristic_value)
    rv = Fraction(reference_value)
    absolute = rv - hv
    relative = (absolute / rv) if rv != 0 else None
    return GapReport(hv, rv, absolute, relative, NO_GUARANTEE_NOTE)
