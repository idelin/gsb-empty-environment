"""Brute-force reference verifier over a small rational domain.

This module is intentionally naive and independent from
:mod:`clockmap.mapper`: it enumerates small-denominator rational times for
every anchor, keeps the assignments that satisfy *all* pairwise slope
bounds, and reads off the achievable time range at a query index.  It
exists to cross-check the analytical band computation on small cases --
including cases with dropped samples and clock rollback.
"""

from __future__ import annotations

from fractions import Fraction
from typing import Dict, List, Optional, Tuple

from .model import Anchor, SlopeBounds


def rational_grid(lo: Fraction, hi: Fraction, quantum: Fraction) -> List[Fraction]:
    """Grid ``lo, lo+quantum, ..., <= hi`` of small rationals.

    The domain is deliberately small: every point is a rational with a
    denominator dividing ``quantum.denominator``, so enumeration stays
    tractable and exact.
    """
    lo = Fraction(lo)
    hi = Fraction(hi)
    quantum = Fraction(quantum)
    if quantum <= 0:
        raise ValueError("quantum must be positive")
    out = []
    value = lo
    while value <= hi:
        out.append(value)
        value += quantum
    return out


def _pairwise_ok(times: Dict[int, Fraction], slope_bounds: SlopeBounds) -> bool:
    """Check slope bounds between every pair of assigned sample times."""
    items = sorted(times.items())
    for pos, (n1, t1) in enumerate(items):
        for n2, t2 in items[pos + 1:]:
            gap = n2 - n1
            if t2 - t1 < slope_bounds.min_slope * gap:
                return False
            if t2 - t1 > slope_bounds.max_slope * gap:
                return False
    return True


def _assignments(
    anchors: List[Anchor],
    grids: List[List[Fraction]],
    slope_bounds: SlopeBounds,
    fixed: Optional[Dict[int, Fraction]] = None,
):
    """Yield consistent assignments {sample_index: time} via backtracking."""
    fixed = dict(fixed or {})
    order = sorted(range(len(anchors)), key=lambda k: len(grids[k]))

    def rec(slot: int, current: Dict[int, Fraction]):
        if slot == len(order):
            yield dict(current)
            return
        anchor = anchors[order[slot]]
        for t in grids[order[slot]]:
            current[anchor.index] = t
            if _pairwise_ok({**fixed, **current}, slope_bounds):
                yield from rec(slot + 1, current)
        del current[anchor.index]

    if fixed and not _pairwise_ok(fixed, slope_bounds):
        return
    yield from rec(0, fixed)


def reference_feasible(
    anchors: List[Anchor],
    slope_bounds: SlopeBounds,
    quantum: Fraction = Fraction(1, 100),
) -> bool:
    """Brute-force feasibility: some small-rational assignment satisfies all
    anchor intervals and pairwise slope bounds."""
    grids = [
        rational_grid(a.earliest.seconds, a.latest.seconds, quantum)
        for a in anchors
    ]
    if any(not grid for grid in grids):
        return False
    for _ in _assignments(anchors, grids, slope_bounds):
        return True
    return False


def reference_band(
    anchors: List[Anchor],
    slope_bounds: SlopeBounds,
    index: int,
    quantum: Fraction = Fraction(1, 100),
) -> Optional[Tuple[Fraction, Fraction]]:
    """Achievable ``(min, max)`` wall-clock time at ``index``, by enumeration.

    For every consistent anchor assignment, the query time can range over
    the interval implied by the pairwise slope bounds; the union of those
    intervals gives the band.  ``None`` means infeasible in the searched
    domain.
    """
    grids = [
        rational_grid(a.earliest.seconds, a.latest.seconds, quantum)
        for a in anchors
    ]
    if any(not grid for grid in grids):
        return None
    s_min = slope_bounds.min_slope
    s_max = slope_bounds.max_slope
    best_lo = None
    best_hi = None
    for assignment in _assignments(anchors, grids, slope_bounds):
        lo = None
        hi = None
        for n, t in assignment.items():
            if index >= n:
                cand_lo = t + s_min * (index - n)
                cand_hi = t + s_max * (index - n)
            else:
                cand_lo = t + s_max * (index - n)
                cand_hi = t + s_min * (index - n)
            lo = cand_lo if lo is None else max(lo, cand_lo)
            hi = cand_hi if hi is None else min(hi, cand_hi)
        if lo is None or lo > hi:
            continue
        best_lo = lo if best_lo is None else min(best_lo, lo)
        best_hi = hi if best_hi is None else max(best_hi, hi)
    if best_lo is None:
        return None
    return best_lo, best_hi


def check_epochs_independent(
    epoch_anchors: Dict[int, List[Anchor]],
    slope_bounds: SlopeBounds,
    quantum: Fraction = Fraction(1, 100),
) -> bool:
    """Every epoch feasible on its own -- there are no cross-epoch
    constraints to check, because epochs cannot be bridged (a reset clock
    may even run behind the previous epoch, i.e. rollback)."""
    return all(
        reference_feasible(anchors, slope_bounds, quantum)
        for anchors in epoch_anchors.values()
    )
