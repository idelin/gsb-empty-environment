"""Domain splitting at singularities, with an analysis budget.

When an interval crosses a singularity (e.g. 0 for x -> 1/x), the domain
is split so each piece stays on one side of the singularity.  The
analysis budget caps how many pieces are evaluated; pieces beyond the
budget are *kept* in ``unanalyzed`` -- never silently dropped.
"""

from __future__ import annotations

from dataclasses import dataclass, field

from .core import Interval


def split_domain(domain: Interval, singularities=()) -> list:
    """Split ``domain`` at the given singularity points.

    The singularity points themselves are excluded from the domain of
    definition of the underlying function; each returned closed piece is a
    valid enclosure of the open piece it covers.
    """
    cuts = sorted(s for s in singularities if domain.lo < s < domain.hi)
    bounds = [domain.lo, *cuts, domain.hi]
    return [Interval(a, b) for a, b in zip(bounds, bounds[1:])]


@dataclass
class AnalysisResult:
    analyzed: list = field(default_factory=list)    # list of (piece, result)
    unanalyzed: list = field(default_factory=list)  # budget exhausted: kept


def analyze(eval_fn, domain: Interval, singularities=(), budget=None) -> AnalysisResult:
    """Evaluate ``eval_fn`` piecewise over the split domain.

    ``budget`` = maximum number of pieces to analyze; the rest are kept
    in ``result.unanalyzed`` so no part of the domain is lost.
    """
    pieces = split_domain(domain, singularities)
    if budget is None:
        budget = len(pieces)
    result = AnalysisResult()
    for i, piece in enumerate(pieces):
        if i < budget:
            result.analyzed.append((piece, eval_fn(piece)))
        else:
            result.unanalyzed.append(piece)
    return result
