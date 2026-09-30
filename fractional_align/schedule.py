"""Delay schedules: piecewise-linear delay trajectories over output positions.

A schedule maps an output position n to a delay d(n) in samples. The engine
reads the input at p(n) = n - d(n). Every segment slope is kept strictly
below 1 sample/sample, so p(n) is strictly increasing: the trajectory can
never repeat or skip input samples beyond what is declared.
"""
from __future__ import annotations

import bisect
from dataclasses import dataclass


@dataclass(frozen=True)
class ScheduleSnapshot:
    """Checkpointable schedule state, including the delay version."""

    knots: tuple
    version: int
    committed_position: int


class DelaySchedule:
    """Piecewise-linear delay d(n) in samples, n = output position."""

    def __init__(self, initial_delay: float, max_delay: float):
        self._max_delay = float(max_delay)
        self._check_delay(initial_delay)
        self._positions = [0]
        self._delays = [float(initial_delay)]
        self.version = 0
        self.committed_position = 0

    def _check_delay(self, delay: float) -> None:
        if not 0.0 <= float(delay) <= self._max_delay:
            raise ValueError(f"delay {delay} outside [0, {self._max_delay}]")

    @staticmethod
    def _check_slope(n0: int, d0: float, n1: int, d1: float) -> None:
        slope = (d1 - d0) / (n1 - n0)
        if not -1.0 < slope < 1.0:
            raise ValueError(
                f"delay slope {slope} samples/sample would repeat or skip input"
            )

    def delay_at(self, n: int) -> float:
        positions = self._positions
        i = bisect.bisect_right(positions, n) - 1
        if i < 0:
            return self._delays[0]
        if i >= len(positions) - 1:
            return self._delays[-1]
        n0, n1 = positions[i], positions[i + 1]
        d0, d1 = self._delays[i], self._delays[i + 1]
        return d0 + (d1 - d0) * (n - n0) / (n1 - n0)

    def declare(self, position: int, delay: float) -> None:
        """Add (or replace the latest) knot; positions must be non-decreasing."""
        position = int(position)
        delay = float(delay)
        self._check_delay(delay)
        if position < self.committed_position:
            raise ValueError("cannot modify committed delay history")
        if position < self._positions[-1]:
            raise ValueError("positions must be non-decreasing; retract first")
        if position == self._positions[-1]:
            if len(self._positions) > 1:
                self._check_slope(
                    self._positions[-2], self._delays[-2], position, delay
                )
            self._delays[-1] = delay
        else:
            self._check_slope(self._positions[-1], self._delays[-1], position, delay)
            self._positions.append(position)
            self._delays.append(delay)
        self.version += 1

    def freeze_at(self, n: int) -> None:
        """Pin the trajectory at output position ``n`` with its current value."""
        n = int(n)
        i = bisect.bisect_left(self._positions, n)
        if i < len(self._positions) and self._positions[i] == n:
            return
        value = self.delay_at(n)
        self._positions.insert(i, n)
        self._delays.insert(i, value)

    def retract_after(self, position: int) -> None:
        """Drop all knots after ``position`` (must be uncommitted)."""
        position = int(position)
        if position < self.committed_position:
            raise ValueError("cannot retract committed delay history")
        keep = bisect.bisect_right(self._positions, position)
        if keep < len(self._positions):
            del self._positions[keep:]
            del self._delays[keep:]
            self.version += 1

    def commit_up_to(self, position: int) -> None:
        """Lock the trajectory up to ``position`` (pins a knot there)."""
        position = int(position)
        if position > self.committed_position:
            self.freeze_at(position)
            self.committed_position = position

    def snapshot(self) -> ScheduleSnapshot:
        return ScheduleSnapshot(
            tuple(zip(self._positions, self._delays)),
            self.version,
            self.committed_position,
        )

    def restore(self, snap: ScheduleSnapshot) -> None:
        self._positions = [p for p, _ in snap.knots]
        self._delays = [d for _, d in snap.knots]
        self.version = snap.version
        self.committed_position = snap.committed_position
