import unittest
from fractions import Fraction

from maintopt import Device, Instance
from maintopt.verify import check_feasible, plan_cost, plan_score, segments_of


def base_instance(**overrides):
    kwargs = dict(
        horizon=4,
        group_limits={"g": 2},
        budget=5,
        devices=(
            Device("a", "g", 2, Fraction(3), 1),
            Device("b", "g", 1, Fraction(1), 2),
        ),
    )
    kwargs.update(overrides)
    return Instance(**kwargs)


class SegmentsTest(unittest.TestCase):
    def test_contiguous_is_one_segment(self):
        self.assertEqual(segments_of((1, 2, 3)), 1)

    def test_interruption_adds_segment(self):
        self.assertEqual(segments_of((0, 2, 3)), 2)
        self.assertEqual(segments_of((0, 2, 4)), 3)


class FeasibilityTest(unittest.TestCase):
    def test_valid_plan_passes(self):
        instance = base_instance()
        errors = check_feasible(instance, {"a": (0, 1), "b": (1,)})
        self.assertEqual(errors, [])

    def test_group_simultaneous_limit_enforced(self):
        instance = base_instance(group_limits={"g": 1})
        errors = check_feasible(instance, {"a": (0, 1), "b": (1,)})
        self.assertTrue(any("limit" in e for e in errors))

    def test_budget_enforced(self):
        instance = base_instance(budget=1)
        errors = check_feasible(instance, {"a": (0, 1), "b": (2,)})
        self.assertTrue(any("budget" in e for e in errors))

    def test_interruption_costs_extra_startup(self):
        instance = base_instance()
        split = plan_cost(instance, {"a": (0, 2)})
        whole = plan_cost(instance, {"a": (0, 1)})
        self.assertEqual(split, 2)   # two segments -> startup charged twice
        self.assertEqual(whole, 1)

    def test_incomplete_job_rejected(self):
        instance = base_instance()
        errors = check_feasible(instance, {"a": (0,)})
        self.assertTrue(any("needs 2 slots" in e for e in errors))

    def test_out_of_window_and_blocked_slots_rejected(self):
        instance = base_instance(blocked_slots=frozenset({1}))
        errors = check_feasible(instance, {"a": (1, 2)})
        self.assertTrue(any("blocked" in e for e in errors))
        errors = check_feasible(instance, {"a": (3, 4)})
        self.assertTrue(any("outside window" in e for e in errors))

    def test_unknown_device_rejected(self):
        instance = base_instance()
        errors = check_feasible(instance, {"ghost": (0,)})
        self.assertTrue(any("unknown device" in e for e in errors))

    def test_score_is_exact_rational_weight(self):
        instance = base_instance()
        self.assertEqual(plan_score(instance, {"a": (0, 1), "b": (2,)}), Fraction(4))


if __name__ == "__main__":
    unittest.main()
