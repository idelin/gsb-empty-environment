import unittest
from fractions import Fraction
from itertools import combinations

from maintopt import (
    BUDGET_UNDECIDED,
    FEASIBLE,
    OPTIMAL,
    Device,
    Instance,
    solve,
)
from maintopt.verify import check_feasible, plan_cost, plan_score, segments_of


def brute_force_optimum(instance):
    """Independent enumeration of every plan, used as a cross-check."""
    devices = instance.devices
    free = [t for t in range(instance.horizon) if t not in instance.blocked_slots]
    per_device = []
    for device in devices:
        opts = [None]
        for slots in combinations(free, device.duration):
            cost = device.startup_cost * segments_of(slots)
            opts.append((slots, cost))
        per_device.append(opts)
    best = Fraction(0)

    def walk(i, budget_left, usage, score):
        nonlocal best
        if i == len(devices):
            best = max(best, score)
            return
        device = devices[i]
        for option in per_device[i]:
            if option is None:
                walk(i + 1, budget_left, usage, score)
                continue
            slots, cost = option
            if cost > budget_left:
                continue
            if any(usage[t] + 1 > instance.group_limits[device.group] for t in slots):
                continue
            for t in slots:
                usage[t] += 1
            walk(i + 1, budget_left - cost, usage, score + device.score)
            for t in slots:
                usage[t] -= 1

    walk(0, instance.budget, [0] * instance.horizon, Fraction(0))
    return best


class OptimalTest(unittest.TestCase):
    def test_known_optimum(self):
        instance = Instance(
            horizon=3,
            group_limits={"g": 1},
            budget=10,
            devices=(
                Device("a", "g", 2, Fraction(5), 1),
                Device("b", "g", 1, Fraction(2), 1),
                Device("c", "g", 1, Fraction(3), 1),
            ),
        )
        result = solve(instance)
        self.assertEqual(result.status, OPTIMAL)
        self.assertEqual(result.score, Fraction(8))  # a + c
        self.assertEqual(set(result.plan), {"a", "c"})
        self.assertEqual(check_feasible(instance, result.plan), [])

    def test_interruption_split_when_window_broken(self):
        # Slot 1 is blocked, so the duration-2 job must split into two
        # segments and pay its startup cost twice.
        instance = Instance(
            horizon=3,
            group_limits={"g": 1},
            budget=2,
            devices=(Device("x", "g", 2, Fraction(10), 1),),
            blocked_slots=frozenset({1}),
        )
        result = solve(instance)
        self.assertEqual(result.status, OPTIMAL)
        self.assertEqual(result.plan, {"x": (0, 2)})
        self.assertEqual(result.cost, 2)
        self.assertEqual(segments_of(result.plan["x"]), 2)

    def test_interruption_dropped_when_budget_too_tight(self):
        instance = Instance(
            horizon=3,
            group_limits={"g": 1},
            budget=1,  # a split costs 2 -> the job cannot be afforded
            devices=(Device("x", "g", 2, Fraction(10), 1),),
            blocked_slots=frozenset({1}),
        )
        result = solve(instance)
        self.assertEqual(result.status, OPTIMAL)
        self.assertEqual(result.plan, {})
        self.assertEqual(result.score, Fraction(0))

    def test_rational_scores_exact(self):
        instance = Instance(
            horizon=2,
            group_limits={"g": 2},
            budget=10,
            devices=(
                Device("a", "g", 1, Fraction(1, 3), 1),
                Device("b", "g", 1, Fraction(1, 6), 1),
            ),
        )
        result = solve(instance)
        self.assertEqual(result.score, Fraction(1, 2))
        self.assertIsInstance(result.score, Fraction)

    def test_scores_are_weights_not_probabilities(self):
        # Weights above 1 and weights not summing to 1 are perfectly valid.
        instance = Instance(
            horizon=2,
            group_limits={"g": 2},
            budget=10,
            devices=(
                Device("a", "g", 1, Fraction(7, 2), 1),
                Device("b", "g", 1, Fraction(5), 1),
            ),
        )
        result = solve(instance)
        self.assertEqual(result.score, Fraction(17, 2))

    def test_zero_budget_yields_empty_optimum(self):
        instance = Instance(
            horizon=2,
            group_limits={"g": 1},
            budget=0,
            devices=(Device("a", "g", 1, Fraction(4), 1),),
        )
        result = solve(instance)
        self.assertEqual(result.status, OPTIMAL)
        self.assertEqual(result.plan, {})

    def test_matches_brute_force(self):
        instance = Instance(
            horizon=5,
            group_limits={"m": 1, "e": 2},
            budget=5,
            devices=(
                Device("a", "m", 2, Fraction(5), 2),
                Device("b", "m", 1, Fraction(2), 1),
                Device("c", "e", 3, Fraction(7), 2),
                Device("d", "e", 2, Fraction(9, 2), 1),
                Device("e", "e", 1, Fraction(3, 2), 1),
            ),
            blocked_slots=frozenset({2}),
        )
        result = solve(instance)
        self.assertEqual(result.status, OPTIMAL)
        self.assertEqual(result.score, brute_force_optimum(instance))
        self.assertEqual(result.cost, plan_cost(instance, result.plan))
        self.assertEqual(result.score, plan_score(instance, result.plan))


class StatusTest(unittest.TestCase):
    def make_instance(self):
        return Instance(
            horizon=4,
            group_limits={"g": 2},
            budget=10,
            devices=tuple(
                Device(f"d{i}", "g", 1, Fraction(i + 1), 1) for i in range(4)
            ),
        )

    def test_feasible_status_when_nodes_run_out(self):
        instance = self.make_instance()
        full = solve(instance)
        partial = solve(instance, node_budget=3)
        self.assertEqual(partial.status, FEASIBLE)
        self.assertIsNotNone(partial.plan)
        self.assertEqual(check_feasible(instance, partial.plan), [])
        self.assertLessEqual(partial.score, full.score)
        self.assertIsNotNone(partial.bound)
        self.assertGreaterEqual(partial.bound, full.score)

    def test_budget_undecided_status(self):
        result = solve(self.make_instance(), node_budget=0)
        self.assertEqual(result.status, BUDGET_UNDECIDED)
        self.assertIsNone(result.plan)
        self.assertIsNone(result.score)

    def test_statuses_are_distinct(self):
        instance = self.make_instance()
        statuses = {
            solve(instance).status,
            solve(instance, node_budget=3).status,
            solve(instance, node_budget=0).status,
        }
        self.assertEqual(statuses, {OPTIMAL, FEASIBLE, BUDGET_UNDECIDED})


if __name__ == "__main__":
    unittest.main()
