"""Independent rational-point enumeration to check composition relations.

Sample points are derived from segment breakpoints (each boundary that lies
in the domain, plus the midpoint of every adjacent pair), so the check is
exact, finite and independent of the interval lengths -- huge domains are
verified without ever expanding ticks.
"""
from __future__ import annotations

from dataclasses import dataclass, field
from fractions import Fraction

from .mapping import PiecewiseMap


def rational_samples(m: PiecewiseMap) -> list[Fraction]:
    """Rational check points: in-domain boundaries plus interval midpoints."""
    bounds = m.boundaries()
    pts = []
    for b in bounds:
        try:
            m.segment_at(b)
        except KeyError:
            continue
        pts.append(b)
    for a, b in zip(bounds, bounds[1:]):
        pts.append((a + b) / 2)
    return sorted(set(pts))


@dataclass
class CompositionCheck:
    samples: int
    mismatches: list[tuple[Fraction, Fraction, Fraction]] = field(default_factory=list)

    @property
    def ok(self) -> bool:
        return not self.mismatches

    def __str__(self) -> str:
        if self.ok:
            return f"OK ({self.samples} rational sample points)"
        return f"FAILED ({len(self.mismatches)}/{self.samples} mismatches): {self.mismatches}"


def check_composition(f: PiecewiseMap, g: PiecewiseMap, h: PiecewiseMap) -> CompositionCheck:
    """Verify h(t) == g(f(t)) at independent rational sample points."""
    pts = set(rational_samples(h))
    for b in f.boundaries():
        try:
            f.segment_at(b)
        except KeyError:
            continue
        pts.add(b)
    mismatches = []
    checked = 0
    for t in sorted(pts):
        try:
            direct = h.eval(t)
        except KeyError:
            mismatches.append((t, Fraction(0), Fraction(0)))
            continue
        checked += 1
        via = g.eval(f.eval(t))
        if direct != via:
            mismatches.append((t, via, direct))
    return CompositionCheck(checked, mismatches)
