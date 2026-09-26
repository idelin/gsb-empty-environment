"""unittest suite for the battery scheduling solver (Python 3.11, stdlib)."""
from __future__ import annotations

import random
import unittest
from fractions import Fraction
from itertools import product
from math import lcm

from battery import (
    BudgetStatus,
    Status,
    build_problem,
    solve,
    to_fraction,
)


def brute_force_optimum(problem):
    """Independent exact enumeration on the same rational grid.

    Returns the minimal total cost (Fraction) or None if infeasible.
    """
    eta_c = problem.charge_efficiency
    eta_d = problem.discharge_efficiency
    charge_bound = eta_c * problem.max_charge_power
    discharge_bound = problem.max_discharge_power
    scale = 1
    for v in (problem.capacity, charge_bound, discharge_bound,
              problem.initial_energy, problem.final_energy):
        scale = lcm(scale, v.denominator)
    cap = int(problem.capacity * scale)
    cb = int(charge_bound * scale)
    db = int(discharge_bound * scale)
    e0 = int(problem.initial_energy * scale)
    et = int(problem.final_energy * scale)

    # Per-period list of (delta_e, variable_cost) moves: idle, charge, discharge.
    per_period = []
    for price in problem.prices:
        moves = [(0, Fraction(0))]
        moves += [(k, Fraction(k, scale) * price / eta_c) for k in range(1, cb + 1)]
        moves += [(-k, -Fraction(k, scale) * price * eta_d) for k in range(1, db + 1)]
        per_period.append(moves)

    best = {}
    # Forward reachability with min cost, enumerating the full move product.
    frontier = {e0: Fraction(0)}
    for moves in per_period:
        nxt = {}
        for e, cost in frontier.items():
            for delta, move_cost in moves:
                ne = e + delta
                if 0 <= ne <= cap:
                    val = cost + move_cost
                    if ne not in nxt or val < nxt[ne]:
                        nxt[ne] = val
        frontier = nxt
    if et not in frontier:
        return None
    constant = sum((p * d for p, d in zip(problem.prices, problem.demands)),
                   Fraction(0))
    return frontier[et] + constant


class HandComputedCases(unittest.TestCase):
    def test_lossless_arbitrage(self):
        problem = build_problem(
            prices=[1, 3], demands=[0, 0], capacity=10,
            max_charge_power=5, max_discharge_power=5,
        )
        result = solve(problem)
        self.assertIs(result.status, Status.OPTIMAL)
        # Buy 5 at price 1, sell 5 at price 3.
        self.assertEqual(result.optimal_cost, Fraction(-10))
        self.assertEqual(result.schedule[0].charge, 5)
        self.assertEqual(result.schedule[1].discharge, 5)

    def test_lossy_round_trip_not_worth_it(self):
        problem = build_problem(
            prices=[1, 3], demands=[0, 0], capacity=10,
            max_charge_power=5, max_discharge_power=5,
            charge_efficiency=Fraction(1, 2),
            discharge_efficiency=Fraction(1, 2),
        )
        result = solve(problem)
        # Round trip: spend 5, get back 3 * 5/4 = 15/4 < 5, so idling wins.
        self.assertEqual(result.optimal_cost, Fraction(0))

    def test_efficiency_losses_counted_per_direction(self):
        problem = build_problem(
            prices=[1, 10], demands=[0, 0], capacity=10,
            max_charge_power=4, max_discharge_power=4,
            charge_efficiency=Fraction(1, 2),
            discharge_efficiency=Fraction(1, 4),
        )
        result = solve(problem)
        charge, discharge = result.schedule
        # Charge 4 from the grid, only eta_c * 4 = 2 stored.
        self.assertEqual(charge.charge, 4)
        self.assertEqual(charge.energy_after, 2)
        # Release 2 from storage, only eta_d * 2 = 1/2 delivered.
        self.assertEqual(discharge.discharge, 2)
        self.assertEqual(discharge.delivered, Fraction(1, 2))
        # Cost: 1*4 - 10*(1/2) = -1.
        self.assertEqual(result.optimal_cost, Fraction(-1))

    def test_demand_must_be_served(self):
        problem = build_problem(
            prices=[2], demands=[4], capacity=5,
            max_charge_power=0, max_discharge_power=0,
        )
        result = solve(problem)
        self.assertEqual(result.optimal_cost, Fraction(8))
        self.assertEqual(result.schedule[0].grid_import, 4)

    def test_final_energy_target_is_met_exactly(self):
        problem = build_problem(
            prices=[1, 1, 1], demands=[0, 0, 0], capacity=10,
            max_charge_power=4, max_discharge_power=4,
            initial_energy=1, final_energy=7,
        )
        result = solve(problem)
        self.assertIs(result.status, Status.OPTIMAL)
        self.assertEqual(result.schedule[-1].energy_after, 7)
        # Must buy exactly 6 units of energy, all at price 1.
        self.assertEqual(result.optimal_cost, Fraction(6))

    def test_infeasible_final_energy(self):
        problem = build_problem(
            prices=[1, 1], demands=[0, 0], capacity=10,
            max_charge_power=3, max_discharge_power=3,
            charge_efficiency=Fraction(1, 2),
            discharge_efficiency=Fraction(1, 2),
            initial_energy=0, final_energy=5,
        )
        result = solve(problem)
        self.assertIs(result.status, Status.INFEASIBLE)
        self.assertIsNone(result.optimal_cost)
        self.assertEqual(result.schedule, ())


class NegativePricesAndCycling(unittest.TestCase):
    def test_no_simultaneous_charge_and_discharge(self):
        # Negative price would pay for energy burned via round-trip loss;
        # the solver must never charge and discharge in the same period.
        problem = build_problem(
            prices=[-5], demands=[0], capacity=100,
            max_charge_power=5, max_discharge_power=5,
            charge_efficiency=Fraction(1, 2),
            discharge_efficiency=Fraction(1, 2),
            initial_energy=0, final_energy=0,
        )
        result = solve(problem)
        self.assertIs(result.status, Status.OPTIMAL)
        for d in result.schedule:
            self.assertFalse(d.charge > 0 and d.discharge > 0)
        # With cycling allowed one could earn 5*(5 - 5/4) = 75/4 from thin
        # air; the no-cycling rule keeps the only feasible action "idle".
        self.assertEqual(result.optimal_cost, Fraction(0))

    def test_negative_price_profit_is_bounded(self):
        problem = build_problem(
            prices=[-2, -2], demands=[0, 0], capacity=100,
            max_charge_power=5, max_discharge_power=5,
            charge_efficiency=Fraction(1, 2),
            discharge_efficiency=Fraction(1, 2),
            initial_energy=0, final_energy=0,
        )
        result = solve(problem)
        # Charge 5 in period 1 (paid 10), discharge the stored 5/2 in
        # period 2 (export 5/4 at a negative price costs 5/2): net -15/2.
        self.assertEqual(result.optimal_cost, Fraction(-15, 2))
        # Bounded: |profit| <= sum_t |price_t| * (demand_t + max_charge).
        bound = sum(2 * 5 for _ in range(2))
        self.assertGreaterEqual(result.optimal_cost, Fraction(-bound))

    def test_negative_price_never_triggers_export(self):
        problem = build_problem(
            prices=[-3, 5], demands=[1, 1], capacity=10,
            max_charge_power=4, max_discharge_power=4,
            initial_energy=0, final_energy=0,
        )
        result = solve(problem)
        # Exporting at a negative price would lose money; grid import in
        # the negative-price period must stay non-negative.
        self.assertGreaterEqual(result.schedule[0].grid_import, 0)


class BudgetDecision(unittest.TestCase):
    def make_problem(self, budget):
        return build_problem(
            prices=[1, 3], demands=[0, 0], capacity=10,
            max_charge_power=5, max_discharge_power=5, budget=budget,
        )

    def test_within_budget(self):
        result = solve(self.make_problem(budget=-5))
        self.assertIs(result.budget_status, BudgetStatus.WITHIN_BUDGET)

    def test_over_budget(self):
        result = solve(self.make_problem(budget=-20))
        self.assertIs(result.budget_status, BudgetStatus.OVER_BUDGET)

    def test_budget_undecided_when_infeasible(self):
        problem = build_problem(
            prices=[1], demands=[0], capacity=1,
            max_charge_power=0, max_discharge_power=0,
            initial_energy=1, final_energy=0, budget=0,
        )
        result = solve(problem)
        self.assertIs(result.status, Status.INFEASIBLE)
        self.assertIs(result.budget_status, BudgetStatus.UNDECIDED)

    def test_no_budget_requested(self):
        problem = build_problem(
            prices=[1], demands=[1], capacity=1,
            max_charge_power=1, max_discharge_power=1,
        )
        result = solve(problem)
        self.assertIsNone(result.budget)
        self.assertIsNone(result.budget_status)


class InputHandling(unittest.TestCase):
    def test_rational_inputs_as_strings_and_fractions(self):
        problem = build_problem(
            prices=["3/2", Fraction(5, 2)], demands=["1/2", "0.5"],
            capacity="7/2", max_charge_power="3/2", max_discharge_power=1,
            charge_efficiency="0.5", discharge_efficiency=Fraction(3, 4),
            initial_energy="1/2", final_energy="1/2",
        )
        result = solve(problem)
        self.assertIs(result.status, Status.OPTIMAL)
        self.assertEqual(result.schedule[-1].energy_after, Fraction(1, 2))

    def test_type_and_value_validation(self):
        base = dict(prices=[1], demands=[0], capacity=1,
                    max_charge_power=1, max_discharge_power=1)
        with self.assertRaises(ValueError):
            build_problem(**{**base, "charge_efficiency": 0})
        with self.assertRaises(ValueError):
            build_problem(**{**base, "discharge_efficiency": "1.5"})
        with self.assertRaises(ValueError):
            build_problem(**{**base, "demands": [-1]})
        with self.assertRaises(ValueError):
            build_problem(**{**base, "capacity": 0})
        with self.assertRaises(ValueError):
            build_problem(**{**base, "final_energy": 2})
        with self.assertRaises(ValueError):
            build_problem(**{**base, "prices": [1, 2]})
        with self.assertRaises(ValueError):
            build_problem(**{**base, "prices": ["abc"]})
        with self.assertRaises(TypeError):
            to_fraction(object(), "x")

    def test_float_inputs_use_decimal_interpretation(self):
        self.assertEqual(to_fraction(0.1), Fraction(1, 10))


class ScheduleInvariant(unittest.TestCase):
    def test_schedule_energy_balance_and_bounds(self):
        problem = build_problem(
            prices=[4, 2, 1, -2, 1, 3, 6, 5],
            demands=[3, 2, 2, 1, 4, 5, 6, 4],
            capacity=12, max_charge_power=6, max_discharge_power=5,
            charge_efficiency=Fraction(9, 10),
            discharge_efficiency=Fraction(9, 10),
            initial_energy=3, final_energy=3,
        )
        result = solve(problem)
        self.assertIs(result.status, Status.OPTIMAL)
        energy = Fraction(3)
        total = Fraction(0)
        for d in result.schedule:
            self.assertFalse(d.charge > 0 and d.discharge > 0)
            self.assertLessEqual(d.charge, 6)
            self.assertLessEqual(d.discharge, 5)
            energy += d.charge * Fraction(9, 10) - d.discharge
            self.assertEqual(energy, d.energy_after)
            self.assertLessEqual(Fraction(0), energy)
            self.assertLessEqual(energy, Fraction(12))
            self.assertEqual(d.grid_import,
                             d.demand + d.charge - d.delivered)
            self.assertEqual(d.cost, d.price * d.grid_import)
            total += d.cost
        self.assertEqual(energy, Fraction(3))
        self.assertEqual(total, result.optimal_cost)


class CrossCheckWithBruteForce(unittest.TestCase):
    def test_random_small_instances(self):
        rng = random.Random(20260926)
        for trial in range(60):
            n = rng.randint(1, 4)
            prices = [Fraction(rng.randint(-4, 6), rng.choice([1, 1, 2]))
                      for _ in range(n)]
            demands = [Fraction(rng.randint(0, 3)) for _ in range(n)]
            capacity = Fraction(rng.randint(1, 6), rng.choice([1, 2]))
            eta_c_den = rng.choice([1, 2, 3, 4])
            eta_d_den = rng.choice([1, 2, 4])
            problem = build_problem(
                prices=prices, demands=demands,
                capacity=capacity,
                max_charge_power=Fraction(rng.randint(0, 3),
                                          rng.choice([1, 2])),
                max_discharge_power=Fraction(rng.randint(0, 3),
                                             rng.choice([1, 2])),
                charge_efficiency=Fraction(
                    rng.randint(1, eta_c_den), eta_c_den),
                discharge_efficiency=Fraction(
                    rng.randint(1, eta_d_den), eta_d_den),
                initial_energy=Fraction(0),
                final_energy=Fraction(rng.randint(0, int(capacity))),
            )
            expected = brute_force_optimum(problem)
            result = solve(problem)
            if expected is None:
                self.assertIs(result.status, Status.INFEASIBLE,
                              f"trial {trial}: DP feasible but brute force not")
            else:
                self.assertIs(result.status, Status.OPTIMAL,
                              f"trial {trial}: DP infeasible but brute force "
                              f"found {expected}")
                self.assertEqual(result.optimal_cost, expected,
                                 f"trial {trial}: cost mismatch")


if __name__ == "__main__":
    unittest.main()
