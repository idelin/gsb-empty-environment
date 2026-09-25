"""Bucket schemas: uniform fine buckets plus non-uniform business buckets.

Fine buckets are uniform half-open intervals ``[i*w, (i+1)*w)``. The bucket
index of a tick is computed with Python floor division, which rounds toward
negative infinity, so e.g. with width 10 the tick -1 lands in bucket -1,
i.e. ``[-10, 0)`` -- never in ``[0, 10)``.

Business buckets are arbitrary non-uniform half-open intervals defined by
sorted boundaries. Boundaries must align with the fine grid so that any
fully-covered business bucket decomposes exactly into whole fine buckets.

Schemas are immutable and versioned: updating bucket rules creates a new
schema version while old versions keep working for old snapshots.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Tuple


@dataclass(frozen=True)
class BucketSchema:
    fine_width: int
    business_boundaries: Tuple[int, ...] = ()
    version: int = 0

    def __post_init__(self) -> None:
        if self.fine_width <= 0:
            raise ValueError("fine_width must be positive")
        boundaries = tuple(self.business_boundaries)
        if boundaries != tuple(sorted(boundaries)) or len(set(boundaries)) != len(boundaries):
            raise ValueError("business boundaries must be sorted and unique")
        for boundary in boundaries:
            if boundary % self.fine_width != 0:
                raise ValueError(
                    f"business boundary {boundary} is not aligned to "
                    f"fine width {self.fine_width}")
        object.__setattr__(self, "business_boundaries", boundaries)

    def leaf_bucket(self, ticks: int) -> int:
        """Index of the one and only fine bucket containing ``ticks``.

        Floor division: -1 // 10 == -1, so negative ticks round down into
        the bucket below, e.g. [-10, 0) for width 10.
        """
        return ticks // self.fine_width

    def bucket_span(self, index: int) -> Tuple[int, int]:
        """Half-open [lo, hi) span of fine bucket ``index``."""
        return index * self.fine_width, (index + 1) * self.fine_width

    def business_spans(self) -> Tuple[Tuple[int, int], ...]:
        bounds = self.business_boundaries
        return tuple((bounds[i], bounds[i + 1]) for i in range(len(bounds) - 1))
