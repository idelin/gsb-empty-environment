"""Data model for multi-objective plan combination enumeration.

All objective values are exact rationals (:class:`fractions.Fraction`).
Constraint data is synthetic integers or rationals, so floats are rejected
on purpose to keep every comparison exact.
"""

from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction
from typing import Optional, Tuple, Union

NumberLike = Union[int, str, Fraction]


def to_fraction(value: NumberLike, *, field_name: str = "value") -> Fraction:
    """Convert an int / Fraction / numeric string to an exact Fraction."""
    if isinstance(value, bool):
        raise TypeError(f"{field_name}: bool is not accepted as a number")
    if isinstance(value, Fraction):
        return value
    if isinstance(value, int):
        return Fraction(value)
    if isinstance(value, str):
        try:
            return Fraction(value)
        except (ValueError, ZeroDivisionError) as exc:
            raise ValueError(f"{field_name}: {value!r} is not a valid rational") from exc
    raise TypeError(
        f"{field_name}: expected int, Fraction or numeric string, "
        f"got {type(value).__name__}"
    )


@dataclass(frozen=True)
class Plan:
    """One candidate implementation of a task (all objectives minimised)."""

    plan_id: str
    task_id: str
    cost: Fraction
    latency: Fraction
    energy: Fraction

    @classmethod
    def of(cls, plan_id, task_id, cost, latency, energy) -> "Plan":
        return cls(
            plan_id=str(plan_id),
            task_id=str(task_id),
            cost=to_fraction(cost, field_name="cost"),
            latency=to_fraction(latency, field_name="latency"),
            energy=to_fraction(energy, field_name="energy"),
        )


@dataclass(frozen=True)
class Constraints:
    """Resource limits.

    ``max_latency`` / ``max_energy`` are hard limits: any combination that
    violates them is infeasible and never returned. ``max_cost`` is the
    budget: over-budget (but otherwise feasible) combinations are reported
    separately as budget-undecided instead of being dropped.
    """

    max_cost: Optional[Fraction] = None
    max_latency: Optional[Fraction] = None
    max_energy: Optional[Fraction] = None

    @classmethod
    def of(cls, max_cost=None, max_latency=None, max_energy=None) -> "Constraints":
        def conv(value, name):
            return None if value is None else to_fraction(value, field_name=name)

        return cls(
            max_cost=conv(max_cost, "max_cost"),
            max_latency=conv(max_latency, "max_latency"),
            max_energy=conv(max_energy, "max_energy"),
        )


@dataclass(frozen=True)
class Combination:
    """One plan per task; objectives are the sums over the chosen plans."""

    plans: Tuple[Plan, ...]
    cost: Fraction
    latency: Fraction
    energy: Fraction

    @classmethod
    def from_plans(cls, plans) -> "Combination":
        plans = tuple(plans)
        zero = Fraction(0)
        return cls(
            plans=plans,
            cost=sum((p.cost for p in plans), zero),
            latency=sum((p.latency for p in plans), zero),
            energy=sum((p.energy for p in plans), zero),
        )

    @property
    def combo_id(self) -> Tuple[str, ...]:
        """Deterministic identity: plan ids in (sorted) task order."""
        return tuple(p.plan_id for p in self.plans)

    @property
    def objectives(self) -> Tuple[Fraction, Fraction, Fraction]:
        return (self.cost, self.latency, self.energy)

    def sort_key(self):
        return (self.cost, self.latency, self.energy, self.combo_id)
