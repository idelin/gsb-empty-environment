"""Domain splitting at singularities, with an analysis budget.

When an interval crosses a singularity (e.g. 0 for 1/x) the domain is
split into pieces that exclude the singular point (open endpoints), and
each piece is analysed separately.  The budget bounds how many pieces may
be analysed; when it is exhausted the remaining pieces are kept and
reported as *unanalyzed* -- they are never silently dropped.
"""
from __future__ import annotations

from dataclasses import dataclass, field
from fractions import Fraction

from .interval import Interval


@dataclass
class Budget:
    remaining: int

    def consume(self) -> bool:
        if self.remaining <= 0:
            return False
        self.remaining -= 1
        return True


@dataclass
class AnalysisResult:
    pieces: list[tuple[Interval, Interval]] = field(default_factory=list)
    unanalyzed: list[Interval] = field(default_factory=list)

    def hull(self) -> Interval | None:
        """Hull of the analysed pieces (unanalysed parts NOT included --
        check .unanalyzed before trusting this)."""
        result = None
        for _, image in self.pieces:
            result = image if result is None else result.hull(image)
        return result


def split_at(domain: Interval, points) -> list[Interval]:
    """Split ``domain`` at the given singular points.  Pieces adjacent to a
    singularity get an open endpoint there, so the singular value itself is
    excluded from every piece."""
    cuts = sorted(
        Fraction(p) for p in points
        if domain.lo is not None and domain.hi is not None and domain.lo < p < domain.hi
    )
    edges = [domain.lo, *cuts, domain.hi]
    pieces = []
    for a, b in zip(edges, edges[1:]):
        pieces.append(
            Interval(
                a, b,
                lo_open=(a != domain.lo) or domain.lo_open,
                hi_open=(b != domain.hi) or domain.hi_open,
            )
        )
    return pieces


def analyze(f_interval, domain: Interval, singularities, budget: Budget,
            cert=None) -> AnalysisResult:
    """Evaluate ``f_interval`` piece by piece over the split domain."""
    result = AnalysisResult()
    for piece in split_at(domain, singularities):
        if budget.consume():
            image = f_interval(piece)
            result.pieces.append((piece, image))
            if cert is not None:
                cert.add(f"analysed piece {piece}: image {image}", domain=str(piece))
        else:
            result.unanalyzed.append(piece)
            if cert is not None:
                cert.add(
                    f"budget exhausted: piece {piece} kept UNANALYZED",
                    domain=str(piece),
                )
    return result
