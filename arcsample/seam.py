"""Shared-seam sampling.

When several polylines share an edge (a seam), every consumer must see
bit-identical sample points.  Samples are computed exactly once per
canonical (undirected) edge and cached; a request in the opposite
direction reuses the cached computation and re-expresses it, so the
points are exactly the reversed sequence -- never independently rounded.
"""

from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction
from typing import Dict, List, Optional, Tuple

from .rational import Number, Point, as_fraction, as_point, distance


@dataclass(frozen=True)
class EdgeSample:
    mileage: Fraction            # arc length from the requested start point
    point: Point
    t: Fraction                  # exact parameter from requested start (0..1)
    direction: Optional[Point]   # exact unit direction of travel


def _canonical(a: Point, b: Point) -> Tuple[Point, Point]:
    return (a, b) if a <= b else (b, a)


class SeamSampler:
    """Caches exact per-edge sampling shared by any number of polylines."""

    def __init__(self) -> None:
        self._cache: Dict[Tuple[Point, Point, Fraction], Tuple[EdgeSample, ...]] = {}

    @property
    def cache_size(self) -> int:
        return len(self._cache)

    def samples(self, a, b, spacing: Number) -> Tuple[EdgeSample, ...]:
        """Equal-arc-length samples along the edge from ``a`` to ``b``.

        Endpoints are always included.  Traversing the same edge in the
        opposite direction yields exactly the reversed point sequence.
        """
        a = as_point(a)
        b = as_point(b)
        spacing = as_fraction(spacing)
        if spacing <= 0:
            raise ValueError("spacing must be positive")
        c0, c1 = _canonical(a, b)
        key = (c0, c1, spacing)
        canonical = self._cache.get(key)
        if canonical is None:
            canonical = self._compute(c0, c1, spacing)
            self._cache[key] = canonical
        if (a, b) == (c0, c1):
            return canonical
        return self._reverse(canonical)

    @staticmethod
    def _compute(c0: Point, c1: Point, spacing: Fraction) -> Tuple[EdgeSample, ...]:
        length = distance(c0, c1)
        if length == 0:
            return (EdgeSample(Fraction(0), c0, Fraction(0), None),)
        unit = tuple((y - x) / length for x, y in zip(c0, c1))
        mileages: List[Fraction] = []
        k = 0
        while k * spacing < length:
            mileages.append(k * spacing)
            k += 1
        if mileages[-1] != length:
            mileages.append(length)
        return tuple(
            EdgeSample(
                mileage=m,
                point=tuple(x + (m / length) * (y - x) for x, y in zip(c0, c1)),
                t=m / length,
                direction=unit,
            )
            for m in mileages
        )

    @staticmethod
    def _reverse(samples: Tuple[EdgeSample, ...]) -> Tuple[EdgeSample, ...]:
        total = samples[-1].mileage
        out: List[EdgeSample] = []
        for sample in reversed(samples):
            direction = (
                tuple(-d for d in sample.direction)
                if sample.direction is not None
                else None
            )
            out.append(
                EdgeSample(
                    mileage=total - sample.mileage,
                    point=sample.point,
                    t=1 - sample.t,
                    direction=direction,
                )
            )
        return tuple(out)
