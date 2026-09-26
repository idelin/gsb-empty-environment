"""Exhaustive verification on a small rational grid.

Every demand rate is enumerated over {0, step, 2*step, ..., cap}; capacity
constraints prune the search.  The candidate allocation is compared against
every feasible grid point by the lexicographic (lex-max) order of the sorted
normalized-rate vector -- the formal definition of (weighted) max-min
optimality.

Outcomes are kept strictly separate:

* INFEASIBLE        -- the candidate violates caps or link capacities;
* OPTIMAL           -- feasible, and no feasible grid point is lex-larger;
* NOT_OPTIMAL       -- feasible, but a strictly better grid point exists;
* BUDGET_UNDECIDED  -- the enumeration budget ran out before the grid was
                       covered, so optimality remains undecided (feasibility
                       was still established).

A plain feasibility check is available on its own via verify_feasible.
"""

from __future__ import annotations

from dataclasses import dataclass
from enum import Enum
from fractions import Fraction
from math import lcm
from typing import Mapping

from .certificate import check_feasible
from .model import Network


class Status(Enum):
    INFEASIBLE = "infeasible"
    OPTIMAL = "optimal"
    NOT_OPTIMAL = "not_optimal"
    BUDGET_UNDECIDED = "budget_undecided"


@dataclass
class Verification:
    status: Status
    nodes_visited: int
    candidate_key: tuple[Fraction, ...] | None
    best_grid_key: tuple[Fraction, ...] | None
    witness: dict[str, Fraction] | None  # a strictly better grid point, if any


def lex_key(network: Network, rates: Mapping[str, Fraction]) -> tuple[Fraction, ...]:
    """Sorted normalized-rate vector; lex-max of this is max-min fairness."""
    return tuple(sorted(network.normalized_rates(rates)[d] for d in network.demands))


def suggest_step(network: Network, rates: Mapping[str, Fraction]) -> Fraction:
    """Grid step 1/L where L covers all denominators of caps and candidate
    rates, so the candidate itself lies on the grid."""
    denominators = [1]
    for demand in network.demands.values():
        denominators.append(demand.cap.denominator)
    for rate in rates.values():
        denominators.append(Fraction(rate).denominator)
    return Fraction(1, lcm(*denominators))


def verify_feasible(network: Network, rates: Mapping[str, Fraction]) -> bool:
    return not check_feasible(network, rates)


def verify_optimal(
    network: Network,
    rates: Mapping[str, Fraction],
    step: Fraction,
    budget: int,
) -> Verification:
    errors = check_feasible(network, rates)
    if errors:
        return Verification(Status.INFEASIBLE, 0, None, None, None)

    step = Fraction(step)
    if step <= 0:
        raise ValueError("grid step must be positive")
    candidate_key = lex_key(network, rates)

    order = sorted(network.demands)
    grids: list[list[Fraction]] = []
    for demand_id in order:
        cap = network.demands[demand_id].cap
        values: list[Fraction] = []
        value = Fraction(0)
        while value <= cap:
            values.append(value)
            value += step
        grids.append(values)

    loads = {link: Fraction(0) for link in network.capacities}
    current: dict[str, Fraction] = {}
    nodes = 0
    undecided = False
    best_key: tuple[Fraction, ...] | None = None
    best_point: dict[str, Fraction] | None = None

    def recurse(index: int) -> None:
        nonlocal nodes, undecided, best_key, best_point
        if undecided:
            return
        nodes += 1
        if nodes > budget:
            undecided = True
            return
        if index == len(order):
            key = tuple(sorted(current[d] / network.demands[d].weight for d in order))
            if best_key is None or key > best_key:
                best_key = key
                best_point = dict(current)
            return
        demand_id = order[index]
        demand = network.demands[demand_id]
        for value in grids[index]:
            if any(loads[link] + value > network.capacities[link] for link in demand.links):
                break  # values ascend; larger ones violate too
            for link in demand.links:
                loads[link] += value
            current[demand_id] = value
            recurse(index + 1)
            del current[demand_id]
            for link in demand.links:
                loads[link] -= value

    recurse(0)

    if undecided:
        return Verification(Status.BUDGET_UNDECIDED, nodes, candidate_key, best_key, None)
    if candidate_key >= best_key:
        return Verification(Status.OPTIMAL, nodes, candidate_key, best_key, None)
    return Verification(Status.NOT_OPTIMAL, nodes, candidate_key, best_key, best_point)
