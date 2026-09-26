"""Data model for the synthetic maintenance-window selection problem.

All constraint data is synthetic: durations, limits, budgets and startup
costs are integers; risk scores may be arbitrary non-negative rationals
(`fractions.Fraction`).  Risk scores serve only as in-problem weights for
the objective and are NOT interpreted as real-world failure probabilities.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from fractions import Fraction


@dataclass(frozen=True)
class Device:
    """A synthetic device that may receive maintenance inside the window."""

    name: str
    group: str
    duration: int        # maintenance work, in integer time slots
    score: Fraction      # in-problem weight only (NOT a real probability)
    startup_cost: int    # charged per segment; an interrupted+resumed job pays again


@dataclass(frozen=True)
class Instance:
    """One maintenance-window instance built from synthetic data."""

    horizon: int                 # window length; slots are 0 .. horizon-1
    group_limits: dict[str, int] # max simultaneous jobs per group, per slot
    budget: int                  # total startup-cost budget
    devices: tuple[Device, ...]
    blocked_slots: frozenset[int] = frozenset()  # slots closed to all groups
