"""Canonical sampling of shared edges (seams).

Every shared edge is identified by its canonical key: the two endpoints
ordered by exact tuple comparison of their Fraction coordinates (no
epsilon).  Interior samples are computed once per (edge, spacing) and
cached, so any number of polylines traversing the same edge - in either
direction - obtain bit-identical points instead of independently rounded
ones.
"""
from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction

from .model import Point, segment_length, to_point


@dataclass(frozen=True)
class CanonicalSample:
    """One interior sample of a shared edge, in canonical orientation."""

    mileage: Fraction  # arc length from the canonical start endpoint
    point: Point
    t: Fraction  # exact parameter from canonical start to canonical end


class SeamRegistry:
    def __init__(self):
        self._edge_ids: dict = {}
        self._cache: dict = {}
        self.computations = 0  # how many edges were actually sampled

    @staticmethod
    def canonical_key(a, b) -> tuple:
        a = to_point(a)
        b = to_point(b)
        return (a, b) if a <= b else (b, a)

    def edge_id(self, a, b) -> int:
        """Stable integer id per canonical edge within this registry."""
        key = self.canonical_key(a, b)
        seq = self._edge_ids.get(key)
        if seq is None:
            seq = len(self._edge_ids)
            self._edge_ids[key] = seq
        return seq

    def interior_samples(self, a, b, spacing) -> tuple:
        """Canonical interior samples (endpoints excluded) for an edge."""
        key = self.canonical_key(a, b)
        spacing = Fraction(spacing)
        cache_key = (key, spacing)
        cached = self._cache.get(cache_key)
        if cached is None:
            p0, p1 = key
            length = segment_length(p0, p1)
            out = []
            m = spacing
            while m < length:
                t = m / length
                out.append(
                    CanonicalSample(
                        mileage=m,
                        point=tuple(p0[k] + t * (p1[k] - p0[k]) for k in range(len(p0))),
                        t=t,
                    )
                )
                m += spacing
            cached = tuple(out)
            self._cache[cache_key] = cached
            self.computations += 1
        return cached
