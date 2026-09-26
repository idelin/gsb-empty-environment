"""Storage charge/discharge scheduling as an exact min-cost circulation.

Model (all quantities per period t, stored-energy units unless noted):
  - charge arc   S -> t:  capacity eta_c * max_charge_power, cost  p_t / eta_c
  - discharge arc t -> K: lower bound demand_t / eta_d (must be delivered),
                          capacity max_discharge_power / eta_d, cost -p_t * eta_d
  - storage arc  t -> t+1: capacity C, cost 0 (its flow is the state of charge)
  - initial energy: S -> 1, capacity initial_energy, cost 0
  - final energy:   T -> K, lower bound final_energy, cost 0
  - return arc K -> S closes the circulation.

No-simultaneous-charge/discharge rule (charge_t * discharge_t == 0):
  - periods with demand > 0 must discharge, so their charge arc is removed;
  - for non-negative prices simultaneous operation is never strictly
    beneficial (reducing both by the same stored amount never increases cost
    because eta_c * eta_d <= 1), and the flow construction never creates it;
  - for negative-price, zero-demand periods the choice is genuinely discrete,
    so we enumerate charge-only / discharge-only modes (2^k configurations)
    and keep the best. This is exactly what bounds the profit from negative
    prices: energy cannot be bought and resold in the same period, and all
    flows are bounded by capacity and power limits.

Result statuses are kept separate:
  - "optimal":    provably minimum-cost schedule found;
  - "feasible":   a feasible schedule is known, but the computational budget
                  ran out before optimality (or full enumeration) was proven;
  - "infeasible": proven that no schedule satisfies all constraints
                  (including the final-energy target);
  - "undecided":  the budget did not suffice to decide even feasibility.
A budget query ("is there a schedule with cost <= budget?") is answered
separately as "yes" / "no" / "undecided".
"""

from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction

from .mincostflow import Circulation
from .model import Instance

STATUS_OPTIMAL = "optimal"
STATUS_FEASIBLE = "feasible"
STATUS_INFEASIBLE = "infeasible"
STATUS_UNDECIDED = "undecided"


@dataclass(frozen=True)
class PeriodSchedule:
    price: Fraction
    demand: Fraction
    charge_grid: Fraction          # grid energy bought this period
    discharge_delivered: Fraction  # energy delivered (demand + surplus sold)
    soc_after: Fraction            # stored energy after this period


@dataclass
class SolveResult:
    status: str
    objective: Fraction | None            # net cost (negative means profit)
    schedule: list[PeriodSchedule] | None
    budget_decision: str | None           # "yes" / "no" / "undecided" / None
    iterations: int                       # cycle-canceling iterations used
    configs_evaluated: int
    configs_total: int


def _build_circulation(inst: Instance, charge_only: frozenset,
                       discharge_only: frozenset):
    """Build the circulation network for one mode assignment.

    charge_only / discharge_only restrict the given (negative-price,
    zero-demand) periods to a single direction.
    """
    periods = inst.periods
    source, sink = 0, 1
    circ = Circulation(2 + periods)
    eta_c = inst.charge_efficiency
    eta_d = inst.discharge_efficiency
    charge_idx = [None] * periods
    discharge_idx = [None] * periods
    soc_idx = [None] * periods
    total_upper = Fraction(0)

    def add(u, v, lower, upper, cost):
        nonlocal total_upper
        total_upper += upper
        return circ.add_arc(u, v, lower, upper, cost)

    for t in range(periods):
        node = 2 + t
        price = inst.prices[t]
        if inst.demands[t] == 0 and t not in discharge_only:
            charge_idx[t] = add(source, node, Fraction(0),
                                eta_c * inst.max_charge_power, price / eta_c)
        if t not in charge_only:
            required = inst.demands[t] / eta_d
            discharge_idx[t] = add(node, sink, required,
                                   inst.max_discharge_power / eta_d,
                                   -price * eta_d)
        if t + 1 < periods:
            soc_idx[t] = add(node, node + 1, Fraction(0), inst.capacity,
                             Fraction(0))
    add(source, 2, Fraction(0), inst.initial_energy, Fraction(0))
    last = 2 + periods - 1
    soc_idx[periods - 1] = add(last, sink, inst.final_energy, inst.capacity,
                               Fraction(0))
    add(sink, source, Fraction(0), total_upper, Fraction(0))
    circ.build()
    return circ, charge_idx, discharge_idx, soc_idx


def _extract_schedule(inst: Instance, circ: Circulation,
                      charge_idx, discharge_idx, soc_idx) -> list[PeriodSchedule]:
    eta_c = inst.charge_efficiency
    eta_d = inst.discharge_efficiency
    zero = Fraction(0)
    schedule = []
    for t in range(inst.periods):
        charge_stored = (circ.flow_of(charge_idx[t])
                         if charge_idx[t] is not None else zero)
        discharge_stored = (circ.flow_of(discharge_idx[t])
                            if discharge_idx[t] is not None else zero)
        schedule.append(PeriodSchedule(
            price=inst.prices[t],
            demand=inst.demands[t],
            charge_grid=charge_stored / eta_c,
            discharge_delivered=discharge_stored * eta_d,
            soc_after=circ.flow_of(soc_idx[t]),
        ))
    return schedule


def objective_of(schedule: list[PeriodSchedule]) -> Fraction:
    """Net cost: sum of price * (energy bought - energy delivered)."""
    return sum(p.price * (p.charge_grid - p.discharge_delivered)
               for p in schedule)


def validate_schedule(inst: Instance, schedule: list[PeriodSchedule]) -> None:
    """Check every constraint exactly; raises AssertionError on violation."""
    assert len(schedule) == inst.periods, "schedule length mismatch"
    soc = inst.initial_energy
    for t, ps in enumerate(schedule):
        assert ps.charge_grid >= 0, f"period {t}: negative charge"
        assert ps.discharge_delivered >= 0, f"period {t}: negative discharge"
        assert not (ps.charge_grid > 0 and ps.discharge_delivered > 0), \
            f"period {t}: simultaneous charge and discharge"
        assert ps.charge_grid <= inst.max_charge_power, \
            f"period {t}: charge power exceeded"
        assert ps.discharge_delivered <= inst.max_discharge_power, \
            f"period {t}: discharge power exceeded"
        assert ps.discharge_delivered >= inst.demands[t], \
            f"period {t}: demand not met"
        soc = (soc + inst.charge_efficiency * ps.charge_grid
               - ps.discharge_delivered / inst.discharge_efficiency)
        assert 0 <= soc <= inst.capacity, f"period {t}: SoC out of bounds"
        assert soc == ps.soc_after, f"period {t}: SoC bookkeeping mismatch"
    assert soc >= inst.final_energy, "final energy target not met"


def solve(inst: Instance, budget: Fraction | None = None,
          iteration_budget: int = 100_000,
          config_budget: int = 4096) -> SolveResult:
    """Solve the scheduling instance exactly (subject to compute budgets)."""
    inst.validate()

    # Demand exceeding the discharge power limit is impossible in any config.
    if any(d > inst.max_discharge_power for d in inst.demands):
        decision = "no" if budget is not None else None
        return SolveResult(STATUS_INFEASIBLE, None, None, decision, 0, 0, 1)

    # Negative-price, zero-demand periods need a discrete direction choice.
    branch = [t for t in range(inst.periods)
              if inst.prices[t] < 0 and inst.demands[t] == 0]
    configs_total = 1 << len(branch)
    configs_to_run = min(configs_total, config_budget)
    complete_enumeration = configs_to_run == configs_total

    best_objective = None
    best_schedule = None
    any_feasible = False
    fully_proven = complete_enumeration
    iterations_used = 0
    iterations_left = max(0, iteration_budget)

    for mask in range(configs_to_run):
        charge_only = frozenset(
            branch[i] for i in range(len(branch)) if (mask >> i) & 1)
        discharge_only = frozenset(branch) - charge_only
        circ, charge_idx, discharge_idx, soc_idx = _build_circulation(
            inst, charge_only, discharge_only)
        if not circ.feasible():
            continue
        any_feasible = True
        proven, used = circ.minimize(iterations_left)
        iterations_used += used
        iterations_left -= used
        if not proven:
            fully_proven = False
        schedule = _extract_schedule(inst, circ, charge_idx, discharge_idx,
                                     soc_idx)
        validate_schedule(inst, schedule)
        objective = objective_of(schedule)
        if best_objective is None or objective < best_objective:
            best_objective = objective
            best_schedule = schedule

    if not any_feasible:
        status = STATUS_INFEASIBLE if complete_enumeration else STATUS_UNDECIDED
    elif fully_proven:
        status = STATUS_OPTIMAL
    else:
        status = STATUS_FEASIBLE

    decision = None
    if budget is not None:
        if status == STATUS_INFEASIBLE:
            decision = "no"
        elif status == STATUS_UNDECIDED:
            decision = "undecided"
        elif best_objective <= budget:
            decision = "yes"
        elif status == STATUS_OPTIMAL:
            decision = "no"
        else:
            decision = "undecided"

    return SolveResult(status, best_objective, best_schedule, decision,
                       iterations_used, configs_to_run, configs_total)
