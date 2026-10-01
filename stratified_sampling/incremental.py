"""Incremental (add / retract) univariate statistics.

Uses the Welford recurrence so that when the identity of a sample changes
(one observation swapped for another) the contribution of the removed
observation can be retracted incrementally instead of recomputing from
scratch.
"""

from __future__ import annotations


class IncrementalStatistics:
    """Running count / mean / sum of squared deviations with retraction."""

    __slots__ = ("_count", "_mean", "_m2")

    def __init__(self, values=()):
        self._count = 0
        self._mean = 0.0
        self._m2 = 0.0
        for value in values:
            self.add(value)

    def add(self, value: float) -> None:
        """Incorporate one observation."""
        self._count += 1
        delta = value - self._mean
        self._mean += delta / self._count
        self._m2 += delta * (value - self._mean)

    def retract(self, value: float) -> None:
        """Remove the contribution of one previously added observation."""
        if self._count == 0:
            raise ValueError("cannot retract from an empty statistic")
        if self._count == 1:
            self._count = 0
            self._mean = 0.0
            self._m2 = 0.0
            return
        new_count = self._count - 1
        delta = value - self._mean
        new_mean = self._mean - delta / new_count
        self._m2 -= delta * (value - new_mean)
        # Guard against tiny negative round-off when retracting down to
        # a constant remainder.
        if self._m2 < 0.0 and self._m2 > -1e-9 * max(1.0, abs(new_mean)):
            self._m2 = 0.0
        self._mean = new_mean
        self._count = new_count

    @property
    def count(self) -> int:
        return self._count

    @property
    def mean(self) -> float:
        if self._count == 0:
            raise ValueError("mean of an empty statistic is undefined")
        return self._mean

    @property
    def sum_of_squared_deviations(self) -> float:
        return self._m2

    @property
    def sample_variance(self) -> float | None:
        """Unbiased (ddof=1) variance; None when fewer than 2 observations."""
        if self._count < 2:
            return None
        return self._m2 / (self._count - 1)

    @property
    def population_variance(self) -> float | None:
        """ddof=0 variance; None when empty."""
        if self._count == 0:
            return None
        return self._m2 / self._count
