from __future__ import annotations

from functools import lru_cache

from .model import (
    Belief,
    Machine,
    apply_input,
    apply_reset,
    initial_belief,
    is_goal,
    legal_inputs,
)


def _can_finish(machine: Machine, budget: int) -> tuple[bool, int]:
    @lru_cache(maxsize=None)
    def rec(belief: Belief, remaining: int) -> bool:
        if is_goal(machine, belief):
            return True
        if remaining <= 0:
            return False

        for symbol in legal_inputs(machine, belief):
            cost = machine.input_costs[symbol]
            if cost > remaining:
                continue
            branches = apply_input(machine, belief, symbol)
            if all(rec(child, remaining - cost) for child in branches.values()):
                return True

        reset_belief = apply_reset(machine, belief)
        cost = machine.reset_cost
        if reset_belief is not None and reset_belief != belief and cost <= remaining:
            if rec(reset_belief, remaining - cost):
                return True
        return False

    start = initial_belief(machine)
    answer = rec(start, budget)
    return answer, rec.cache_info().currsize


def exact_optimal_check(machine: Machine, claimed: int) -> dict[str, object]:
    if claimed < 0:
        raise ValueError("claimed cost must be non-negative")
    at_claimed, at_states = _can_finish(machine, claimed)
    below_states = 0
    below_claimed = False
    if claimed > 0:
        below_claimed, below_states = _can_finish(machine, claimed - 1)
    return {
        "claimed_worst_case_cost": claimed,
        "feasible_with_cost_at_most_claimed": at_claimed,
        "feasible_with_cost_at_most_claimed_minus_one": below_claimed,
        "optimal": at_claimed and not below_claimed,
        "memoized_belief_budget_states": max(at_states, below_states),
    }
