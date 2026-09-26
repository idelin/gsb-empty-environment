"""Data model for the battery charge/discharge scheduling problem.

All quantities are exact rationals (fractions.Fraction).  Inputs may be
given as int, float, str (e.g. "3/4", "1.5", "-2") or Fraction.
"""
from __future__ import annotations

from dataclasses import dataclass
from enum import Enum
from fractions import Fraction


def to_fraction(value, name: str = "value") -> Fraction:
    """Convert an int / float / str / Fraction to an exact Fraction."""
    if isinstance(value, Fraction):
        return value
    if isinstance(value, bool):
        raise TypeError(f"{name}: boolean is not a valid number")
    if isinstance(value, int):
        return Fraction(value)
    if isinstance(value, float):
        # Decimal interpretation of the literal, not the binary expansion.
        return Fraction(str(value))
    if isinstance(value, str):
        try:
            return Fraction(value.strip())
        except (ValueError, ZeroDivisionError) as exc:
            raise ValueError(f"{name}: cannot parse {value!r} as a rational") from exc
    raise TypeError(
        f"{name}: unsupported type {type(value).__name__}; use int, float, str or Fraction"
    )


class Status(Enum):
    """Outcome of an optimisation run."""

    OPTIMAL = "optimal"        # a provably optimal schedule was found
    INFEASIBLE = "infeasible"  # no schedule satisfies every constraint


class BudgetStatus(Enum):
    """Answer to the budget decision question "is optimal cost <= budget?"."""

    WITHIN_BUDGET = "within_budget"
    OVER_BUDGET = "over_budget"
    UNDECIDED = "undecided"  # budget was given but the problem is infeasible


@dataclass(frozen=True)
class Problem:
    """A fully validated scheduling instance.

    Conventions (per period t, all energies in the same unit, period = 1h):
      * charge c_t is bought from the grid; the battery stores eta_c * c_t.
      * discharge s_t is taken from the battery; eta_d * s_t is delivered.
      * grid_import g_t = demand_t + c_t - eta_d * s_t (negative = export).
      * cost_t = price_t * g_t (negative price => you are paid to import).
      * energy_{t+1} = energy_t + eta_c * c_t - s_t, 0 <= energy <= capacity.
      * c_t > 0 and s_t > 0 never hold in the same period (no simultaneous
        bidirectional cycling); this also keeps negative prices from being
        exploited by burning energy through the round-trip loss.
    """

    prices: tuple[Fraction, ...]
    demands: tuple[Fraction, ...]
    capacity: Fraction
    max_charge_power: Fraction
    max_discharge_power: Fraction
    charge_efficiency: Fraction
    discharge_efficiency: Fraction
    initial_energy: Fraction
    final_energy: Fraction
    budget: Fraction | None = None

    @property
    def periods(self) -> int:
        return len(self.prices)


def build_problem(
    prices,
    demands,
    capacity,
    max_charge_power,
    max_discharge_power,
    charge_efficiency=1,
    discharge_efficiency=1,
    initial_energy=0,
    final_energy=0,
    budget=None,
) -> Problem:
    """Convert inputs to exact rationals and validate every constraint."""
    prices = tuple(to_fraction(p, f"prices[{i}]") for i, p in enumerate(prices))
    demands = tuple(to_fraction(d, f"demands[{i}]") for i, d in enumerate(demands))
    if not prices:
        raise ValueError("at least one period is required")
    if len(prices) != len(demands):
        raise ValueError(
            f"prices and demands must have equal length "
            f"({len(prices)} != {len(demands)})"
        )
    capacity = to_fraction(capacity, "capacity")
    max_charge_power = to_fraction(max_charge_power, "max_charge_power")
    max_discharge_power = to_fraction(max_discharge_power, "max_discharge_power")
    charge_efficiency = to_fraction(charge_efficiency, "charge_efficiency")
    discharge_efficiency = to_fraction(discharge_efficiency, "discharge_efficiency")
    initial_energy = to_fraction(initial_energy, "initial_energy")
    final_energy = to_fraction(final_energy, "final_energy")
    budget = None if budget is None else to_fraction(budget, "budget")

    if capacity <= 0:
        raise ValueError("capacity must be positive")
    if max_charge_power < 0 or max_discharge_power < 0:
        raise ValueError("power limits must be non-negative")
    if not (0 < charge_efficiency <= 1):
        raise ValueError("charge_efficiency must be in (0, 1]")
    if not (0 < discharge_efficiency <= 1):
        raise ValueError("discharge_efficiency must be in (0, 1]")
    for i, d in enumerate(demands):
        if d < 0:
            raise ValueError(f"demands[{i}] must be non-negative")
    if not (0 <= initial_energy <= capacity):
        raise ValueError("initial_energy must lie in [0, capacity]")
    if not (0 <= final_energy <= capacity):
        raise ValueError("final_energy must lie in [0, capacity]")

    return Problem(
        prices=prices,
        demands=demands,
        capacity=capacity,
        max_charge_power=max_charge_power,
        max_discharge_power=max_discharge_power,
        charge_efficiency=charge_efficiency,
        discharge_efficiency=discharge_efficiency,
        initial_energy=initial_energy,
        final_energy=final_energy,
        budget=budget,
    )


@dataclass(frozen=True)
class PeriodDecision:
    """What happens in one period of an optimal schedule."""

    period: int               # 1-based period index
    price: Fraction           # price in this period
    demand: Fraction          # load that must be served
    charge: Fraction          # energy bought from the grid for charging
    discharge: Fraction       # energy released from the battery (storage side)
    delivered: Fraction       # eta_d * discharge, serves demand / is exported
    grid_import: Fraction     # demand + charge - delivered (negative = export)
    energy_after: Fraction    # battery energy at the end of the period
    cost: Fraction            # price * grid_import


@dataclass(frozen=True)
class Result:
    """Solver output: feasibility, optimality and the budget decision."""

    status: Status
    optimal_cost: Fraction | None      # None when infeasible
    schedule: tuple[PeriodDecision, ...]
    budget: Fraction | None            # None when no budget was requested
    budget_status: BudgetStatus | None  # None when no budget was requested
    message: str = ""

    @property
    def is_optimal(self) -> bool:
        return self.status is Status.OPTIMAL
