"""Immutable edit plans built on a piecewise-affine time map.

A plan is a value object: revising control points produces a *new* plan, so
readers holding the old plan keep the old mapping (no in-place mutation).
``affected_output_range`` localises the output interval in which two plans
differ, so a renderer only has to recompute that part of the output.
"""
from __future__ import annotations

from dataclasses import dataclass
from typing import Optional, Sequence, Tuple

from timemap import PiecewiseTimeMap


@dataclass(frozen=True)
class EditPlan:
    time_map: PiecewiseTimeMap

    @classmethod
    def from_control_points(
        cls, points: Sequence[Tuple[float, float]]
    ) -> "EditPlan":
        return cls(PiecewiseTimeMap(points))

    @property
    def out_start(self) -> float:
        return self.time_map.out_start

    @property
    def out_end(self) -> float:
        return self.time_map.out_end

    def revise(self, points: Sequence[Tuple[float, float]]) -> "EditPlan":
        """Return a revised plan; ``self`` is left untouched for old readers."""
        return EditPlan.from_control_points(points)

    def affected_output_range(
        self, other: "EditPlan"
    ) -> Optional[Tuple[float, float]]:
        """Smallest output interval covering every difference with ``other``.

        Both maps are piecewise affine, so on each atomic interval between
        consecutive union breakpoints their difference is affine; probing the
        endpoints and the midpoint decides whether the interval differs.
        Returns ``None`` when the plans are equivalent (nothing to recompute).
        """
        a, b = self.time_map, other.time_map
        lo = min(a.out_start, b.out_start)
        hi = max(a.out_end, b.out_end)
        cuts = {lo, hi}
        cuts.update(t for t, _ in a.control_points if lo < t < hi)
        cuts.update(t for t, _ in b.control_points if lo < t < hi)
        ordered = sorted(cuts)
        first: Optional[float] = None
        last: Optional[float] = None
        for x, y in zip(ordered, ordered[1:]):
            if y <= x:
                continue
            mid = (x + y) / 2.0
            if (
                a.source_at(x) != b.source_at(x)
                or a.source_at(mid) != b.source_at(mid)
                or a.source_at(y) != b.source_at(y)
            ):
                if first is None:
                    first = x
                last = y
        if first is None:
            return None
        return (first, last)
