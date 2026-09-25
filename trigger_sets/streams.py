"""Periodic trigger streams with versioned pauses, exceptions, phase switches.

Rule semantics (all times exact ``Fraction``):

- Base schedule: triggers at ``phase + n * period`` for integers ``n >= 0``.
- ``PhaseSwitch(at, phase)``: for ``t >= at`` the schedule re-anchors to
  ``at + phase + n * period`` (``n >= 0``). Switches apply in time order.
- ``PauseInterval(start, end)``: periodic triggers inside ``[start, end)``
  are suppressed. Pauses never suppress exception points.
- Exception points: explicit extra trigger times; they always fire, even
  inside a pause.

Every mutation bumps ``stream.version`` so a :class:`Checkpoint` can bind a
query to the exact rule versions it ran against.
"""

from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction
from math import ceil


@dataclass(frozen=True, order=True)
class PauseInterval:
    """Half-open ``[start, end)`` interval suppressing periodic triggers."""

    start: Fraction
    end: Fraction

    def __post_init__(self) -> None:
        object.__setattr__(self, "start", Fraction(self.start))
        object.__setattr__(self, "end", Fraction(self.end))
        if not self.start < self.end:
            raise ValueError("pause interval must be non-empty")

    def contains(self, t) -> bool:
        return self.start <= Fraction(t) < self.end


@dataclass(frozen=True, order=True)
class PhaseSwitch:
    """At time ``at`` the schedule re-anchors to ``at + phase + n * period``."""

    at: Fraction
    phase: Fraction

    def __post_init__(self) -> None:
        object.__setattr__(self, "at", Fraction(self.at))
        object.__setattr__(self, "phase", Fraction(self.phase))
        if self.phase < 0:
            raise ValueError("phase must be non-negative")


class PeriodicStream:
    """A named periodic trigger stream with versioned rules."""

    def __init__(self, name, period, phase=0):
        period, phase = Fraction(period), Fraction(phase)
        if period <= 0:
            raise ValueError("period must be positive")
        if phase < 0:
            raise ValueError("phase must be non-negative")
        self.name = str(name)
        self.period = period
        self.phase = phase
        self.pauses: list[PauseInterval] = []
        self.exceptions: set[Fraction] = set()
        self.switches: list[PhaseSwitch] = []
        self.version = 0

    # -- versioned rule mutation --------------------------------------
    def add_pause(self, start, end) -> PauseInterval:
        pause = PauseInterval(start, end)
        self.pauses.append(pause)
        self.version += 1
        return pause

    def add_exception(self, t) -> Fraction:
        point = Fraction(t)
        self.exceptions.add(point)
        self.version += 1
        return point

    def add_phase_switch(self, at, phase) -> PhaseSwitch:
        switch = PhaseSwitch(at, phase)
        if any(sw.at == switch.at for sw in self.switches):
            raise ValueError(f"duplicate phase switch at {switch.at}")
        self.switches.append(switch)
        self.switches.sort(key=lambda sw: sw.at)
        self.version += 1
        return switch

    # -- rule evaluation ------------------------------------------------
    def anchor_at(self, t) -> Fraction:
        """Anchor of the schedule in force at time ``t``."""
        t = Fraction(t)
        anchor = self.phase
        for switch in self.switches:
            if switch.at <= t:
                anchor = switch.at + switch.phase
            else:
                break
        return anchor

    def is_paused(self, t) -> bool:
        t = Fraction(t)
        return any(p.contains(t) for p in self.pauses)

    def is_trigger(self, t) -> bool:
        """Exact membership test: does this stream fire at ``t``?"""
        t = Fraction(t)
        if t in self.exceptions:
            return True
        if self.is_paused(t):
            return False
        anchor = self.anchor_at(t)
        if t < anchor:
            return False
        return (t - anchor) % self.period == 0

    def triggers_in(self, lo, hi) -> list[Fraction]:
        """Exact brute-force enumeration of this stream's triggers in [lo, hi)."""
        lo, hi = Fraction(lo), Fraction(hi)
        points = {e for e in self.exceptions if lo <= e < hi}
        bounds = [lo] + [sw.at for sw in self.switches if lo < sw.at < hi] + [hi]
        for a, b in zip(bounds, bounds[1:]):
            anchor = self.anchor_at(a)
            start = max(a, anchor)
            if start >= b:
                continue
            n = ceil((start - anchor) / self.period)
            t = anchor + n * self.period
            while t < b:
                if not self.is_paused(t):
                    points.add(t)
                n += 1
                t = anchor + n * self.period
        return sorted(points)
