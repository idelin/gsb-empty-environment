"""Exact dynamic-programming solver for the scheduling problem.

No external solver is used.  The method:

1. Work in *storage-side* units: charging c_t from the grid adds
   c'_t = eta_c * c_t to the battery, discharging removes s_t directly.
   The energy balance becomes  e_{t+1} = e_t + c'_t - s_t  with
   0 <= c'_t <= eta_c * P_c and 0 <= s_t <= P_d.
2. Scale every bound (capacity, both power bounds, initial/final energy)
   by the LCM of their denominators so all bounds are integers.  For any
   fixed charge/discharge/idle pattern per period the feasible set is a
   min-cost-flow polytope (totally unimodular) with integer bounds, hence
   an optimal solution exists on the integer grid.  Dynamic programming
   over that grid is therefore *exact*, not an approximation.
3. Each period allows exactly one of {idle, charge, discharge}, which
   structurally forbids simultaneous bidirectional cycling.  This matters
   with negative prices: cycling would otherwise convert the round-trip
   loss into unbounded-per-period arbitrage ("paid to waste energy").
4. Per-period transitions are min-plus convolutions with a linear cost,
   evaluated in O(states) with monotone queues.

All arithmetic uses fractions.Fraction, so results are exact rationals.
"""
from __future__ import annotations

from collections import deque
from fractions import Fraction
from math import lcm

from .model import (
    BudgetStatus,
    PeriodDecision,
    Problem,
    Result,
    Status,
)

DEFAULT_MAX_STATES = 2_000_000


def _common_denominator(values) -> int:
    den = 1
    for v in values:
        den = lcm(den, v.denominator)
    return den


def solve(problem: Problem, max_states: int = DEFAULT_MAX_STATES) -> Result:
    """Solve the instance exactly; never raises for infeasible inputs."""
    p = problem
    n_periods = len(p.prices)
    eta_c = p.charge_efficiency
    eta_d = p.discharge_efficiency

    # Storage-side per-period movement bounds.
    charge_bound = eta_c * p.max_charge_power   # max stored energy gained
    discharge_bound = p.max_discharge_power     # max stored energy released

    scale = _common_denominator(
        [p.capacity, charge_bound, discharge_bound,
         p.initial_energy, p.final_energy]
    )
    n_states = int(p.capacity * scale) + 1
    if n_states > max_states:
        raise ValueError(
            f"scaled state space too large ({n_states} states > {max_states}); "
            "use coarser rational data or raise max_states"
        )

    cap = n_states - 1
    e_start = int(p.initial_energy * scale)
    e_target = int(p.final_energy * scale)
    cb = int(charge_bound * scale)
    db = int(discharge_bound * scale)

    # dp[e] = min variable cost (charging cost minus discharge revenue,
    # excluding the unavoidable sum(price * demand)) to reach scaled
    # energy e after the periods processed so far.  None = unreachable.
    dp: list[Fraction | None] = [None] * n_states
    dp[e_start] = Fraction(0)
    parents: list[list[tuple[int, int] | None]] = []
    constant = Fraction(0)  # sum of price * demand, added back at the end

    for t in range(n_periods):
        price = p.prices[t]
        constant += price * p.demands[t]
        # Per *scaled* unit costs (states are 1/scale of a physical unit).
        charge_cost = price / (eta_c * scale)    # cost per unit charged
        discharge_value = price * eta_d / scale  # revenue per unit released

        # Mode "idle": carry the state over.
        new: list[Fraction | None] = list(dp)
        par: list[tuple[int, int] | None] = [
            (e, 0) if dp[e] is not None else None for e in range(n_states)
        ]

        # Mode "charge": new[ep] = min_{e in [ep-cb, ep]} dp[e] + (ep-e)*k
        # Sliding minimum of dp[e] - e*k over a growing-right window.
        if cb > 0:
            window: deque[int] = deque()
            for ep in range(n_states):
                if dp[ep] is not None:
                    value = dp[ep] - ep * charge_cost
                    while window and dp[window[-1]] - window[-1] * charge_cost >= value:
                        window.pop()
                    window.append(ep)
                while window and window[0] < ep - cb:
                    window.popleft()
                if window:
                    best = window[0]
                    cost = dp[best] + (ep - best) * charge_cost
                    if new[ep] is None or cost < new[ep]:
                        new[ep] = cost
                        par[ep] = (best, 1)

        # Mode "discharge": new[ep] = min_{e in [ep, ep+db]} dp[e] - (e-ep)*v
        # Sliding minimum of dp[e] - e*v over a window, scanned right-to-left.
        if db > 0:
            window = deque()
            for ep in range(cap, -1, -1):
                if dp[ep] is not None:
                    value = dp[ep] - ep * discharge_value
                    while window and dp[window[-1]] - window[-1] * discharge_value >= value:
                        window.pop()
                    window.append(ep)
                while window and window[0] > ep + db:
                    window.popleft()
                if window:
                    best = window[0]
                    cost = dp[best] - (best - ep) * discharge_value
                    if new[ep] is None or cost < new[ep]:
                        new[ep] = cost
                        par[ep] = (best, -1)

        dp = new
        parents.append(par)

    if dp[e_target] is None:
        budget_status = (
            BudgetStatus.UNDECIDED if p.budget is not None else None
        )
        return Result(
            status=Status.INFEASIBLE,
            optimal_cost=None,
            schedule=(),
            budget=p.budget,
            budget_status=budget_status,
            message=(
                "no schedule reaches the required final energy under the "
                "capacity, power and efficiency constraints"
            ),
        )

    # Reconstruct the per-period decisions from the parent pointers.
    moves: list[tuple[int, int, int]] = []
    e = e_target
    for t in range(n_periods - 1, -1, -1):
        prev, mode = parents[t][e]
        moves.append((prev, e, mode))
        e = prev
    moves.reverse()

    schedule = []
    for t, (prev, cur, mode) in enumerate(moves):
        price = p.prices[t]
        stored = Fraction(cur - prev, scale) if mode == 1 else Fraction(0)
        released = Fraction(prev - cur, scale) if mode == -1 else Fraction(0)
        charge = stored / eta_c            # grid-side energy bought
        delivered = released * eta_d       # energy delivered to load/grid
        grid_import = p.demands[t] + charge - delivered
        schedule.append(
            PeriodDecision(
                period=t + 1,
                price=price,
                demand=p.demands[t],
                charge=charge,
                discharge=released,
                delivered=delivered,
                grid_import=grid_import,
                energy_after=Fraction(cur, scale),
                cost=price * grid_import,
            )
        )

    optimal_cost = dp[e_target] + constant
    if p.budget is None:
        budget_status = None
    elif optimal_cost <= p.budget:
        budget_status = BudgetStatus.WITHIN_BUDGET
    else:
        budget_status = BudgetStatus.OVER_BUDGET

    return Result(
        status=Status.OPTIMAL,
        optimal_cost=optimal_cost,
        schedule=tuple(schedule),
        budget=p.budget,
        budget_status=budget_status,
        message="exact optimum found by dynamic programming",
    )
