"""Two-level bucketing: uniform fine buckets + versioned business rules.

The fine layer is a fixed uniform grid over the synthetic tick axis.
Bucket indices use floor division, so negative times behave correctly:
with origin 0 and width 10, tick -1 falls in bucket -1 == [-10, 0),
not in bucket 0 == [0, 10).

A ``BucketRule`` groups contiguous fine buckets into non-uniform business
buckets.  Rules are immutable and versioned: registering a new rule never
touches the fine-level index, so old query snapshots keep working and the
aggregation tree is never rebuilt on a rule change.
"""

from __future__ import annotations

from bisect import bisect_right
from dataclasses import dataclass
from typing import Dict, List, Tuple


@dataclass(frozen=True)
class FineBuckets:
    """Uniform fine bucket grid over the synthetic tick axis."""

    origin: int  # ticks, start of bucket 0
    width: int  # ticks per bucket, > 0

    def __post_init__(self):
        if self.width <= 0:
            raise ValueError("width must be positive")

    def index(self, ticks: int) -> int:
        """Fine bucket index for a tick position (floor semantics)."""
        return (ticks - self.origin) // self.width

    def bounds(self, index: int) -> Tuple[int, int]:
        """Half-open tick range [lo, hi) covered by a fine bucket."""
        lo = self.origin + index * self.width
        return lo, lo + self.width


@dataclass(frozen=True)
class BucketRule:
    """A versioned mapping of non-uniform business buckets to fine buckets.

    ``boundaries`` are tick positions on the fine grid; business bucket i
    covers ``[boundaries[i], boundaries[i+1])``.  The range is open-ended on
    both sides: everything before the first boundary forms bucket 0 and
    everything from the last boundary on forms the last bucket.
    """

    version: int
    fine: FineBuckets
    boundaries: Tuple[int, ...]

    def __post_init__(self):
        b = tuple(self.boundaries)
        object.__setattr__(self, "boundaries", b)
        for prev, cur in zip(b, b[1:]):
            if cur <= prev:
                raise ValueError("boundaries must be strictly increasing")
        for x in b:
            if (x - self.fine.origin) % self.fine.width != 0:
                raise ValueError("boundaries must align with the fine grid")

    @property
    def bucket_count(self) -> int:
        return len(self.boundaries) + 1

    def locate(self, ticks: int) -> int:
        """Business bucket index for a tick position."""
        return bisect_right(self.boundaries, ticks)

    def fine_ranges(self) -> List[Tuple[int, int]]:
        """Fine-bucket index ranges [lo, hi) for each business bucket."""
        idx = [self.fine.index(x) for x in self.boundaries]
        edges = [-(1 << 62)] + idx + [1 << 62]
        return list(zip(edges[:-1], edges[1:]))


class RuleRegistry:
    """Keeps every version of the business-bucket rule for one fine grid."""

    def __init__(self, fine: FineBuckets):
        self.fine = fine
        self._rules: Dict[int, BucketRule] = {}
        self.register(())

    def register(self, boundaries) -> BucketRule:
        """Register a new rule version; old versions stay valid."""
        rule = BucketRule(len(self._rules), self.fine, tuple(boundaries))
        self._rules[rule.version] = rule
        return rule

    def get(self, version: int) -> BucketRule:
        return self._rules[version]

    @property
    def current(self) -> BucketRule:
        return self._rules[len(self._rules) - 1]
