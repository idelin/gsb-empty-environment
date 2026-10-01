"""unittest suite for the budgeted-coverage toolkit (Python 3.11, stdlib)."""
from __future__ import annotations

import os
import random
import tempfile
import unittest
from fractions import Fraction

from budgeted_coverage import (
    BranchAndBound,
    CheckpointMismatch,
    CoverageSet,
    Instance,
    Status,
    brute_force,
    gap_report,
    greedy,
    make_witness,
    verify_witness,
)


def random_instance(rng: random.Random, n_sets: int, n_elements: int) -> Instance:
    elements = [f"e{j}" for j in range(n_elements)]
    names = [f"s{i}" for i in range(n_sets)]
    sets = []
    for i, name in enumerate(names):
        kwargs = {}
        if i > 0 and rng.random() < 0.25:
            kwargs["requires"] = [rng.choice(names[:i])]
        if i > 0 and rng.random() < 0.15:
            kwargs["excludes"] = [rng.choice(names[:i])]
        sets.append(
            CoverageSet(
                name,
                cost=rng.randint(0, 4),
                elements=rng.sample(elements, rng.randint(0, 3)),
                **kwargs,
            )
        )
    weights = {e: rng.randint(0, 5) for e in elements}
    min_cover = {e: rng.randint(1, 2) for e in elements if rng.random() < 0.3}
    return Instance(sets, weights, budget=rng.randint(2, 8), min_cover=min_cover)


class TestModel(unittest.TestCase):
    def test_profit_counts_element_once(self):
        inst = Instance(
            [CoverageSet("a", 1, ["x"]), CoverageSet("b", 1, ["x"])],
            {"x": 7},
            budget=2,
        )
        self.assertEqual(inst.profit_of(["a", "b"]), 7)  # not 14

    def test_min_cover_threshold(self):
        inst = Instance(
            [CoverageSet("a", 1, ["x"]), CoverageSet("b", 1, ["x"])],
            {"x": 5},
            budget=2,
            min_cover={"x": 2},
        )
        self.assertEqual(inst.profit_of(["a"]), 0)       # below threshold
        self.assertEqual(inst.profit_of(["a", "b"]), 5)  # counted exactly once

    def test_empty_set_adds_no_profit(self):
        inst = Instance(
            [CoverageSet("empty", 0, []), CoverageSet("a", 1, ["x"])],
            {"x": 3},
            budget=1,
        )
        self.assertEqual(inst.profit_of(["empty"]), 0)
        self.assertEqual(inst.profit_of(["empty", "a"]), 3)

    def test_dependency_violation_detected(self):
        inst = Instance(
            [CoverageSet("a", 1, ["x"], requires=["b"]), CoverageSet("b", 1, ["y"])],
            {"x": 1, "y": 1},
            budget=5,
        )
        ev = inst.evaluate(["a"])
        self.assertFalse(ev.feasible)
        self.assertTrue(any("required" in v for v in ev.violations))
        self.assertTrue(inst.evaluate(["a", "b"]).feasible)

    def test_conflict_detected_both_directions(self):
        inst = Instance(
            [CoverageSet("a", 1, ["x"], excludes=["b"]), CoverageSet("b", 1, ["y"])],
            {"x": 1, "y": 1},
            budget=5,
        )
        self.assertFalse(inst.evaluate(["a", "b"]).feasible)

    def test_budget_violation_detected(self):
        inst = Instance([CoverageSet("a", 3, ["x"])], {"x": 1}, budget=2)
        ev = inst.evaluate(["a"])
        self.assertFalse(ev.feasible)
        self.assertTrue(any("budget" in v for v in ev.violations))

    def test_rational_data_exact(self):
        inst = Instance(
            [CoverageSet("a", Fraction(1, 3), ["x"])],
            {"x": Fraction(2, 5)},
            budget=Fraction(1, 2),
        )
        ev = inst.evaluate(["a"])
        self.assertTrue(ev.feasible)
        self.assertEqual(ev.profit, Fraction(2, 5))
        self.assertEqual(ev.cost, Fraction(1, 3))


class TestGreedy(unittest.TestCase):
    def test_zero_cost_sets_first_no_division_by_zero(self):
        inst = Instance(
            [
                CoverageSet("z", 0, ["x"]),
                CoverageSet("a", 1, ["x", "y"]),
                CoverageSet("b", 1, ["y"]),
            ],
            {"x": 5, "y": 1},
            budget=1,
        )
        result = greedy(inst)  # must not raise ZeroDivisionError
        self.assertIn("z", result.selected)
        self.assertEqual(result.status, Status.FEASIBLE)

    def test_greedy_never_selects_empty_set(self):
        inst = Instance(
            [CoverageSet("empty", 0, []), CoverageSet("a", 1, ["x"])],
            {"x": 2},
            budget=1,
        )
        result = greedy(inst)
        self.assertNotIn("empty", result.selected)
        self.assertEqual(result.profit, 2)

    def test_greedy_respects_dependencies_and_budget(self):
        inst = Instance(
            [
                CoverageSet("dep", 2, ["y"]),
                CoverageSet("main", 1, ["x"], requires=["dep"]),
                CoverageSet("cheap", 1, ["z"]),
            ],
            {"x": 10, "y": 1, "z": 2},
            budget=2,
        )
        result = greedy(inst)
        ev = inst.evaluate(result.selected)
        self.assertTrue(ev.feasible)
        self.assertLessEqual(result.cost, inst.budget)

    def test_greedy_never_claims_optimality(self):
        rng = random.Random(7)
        for _ in range(20):
            inst = random_instance(rng, n_sets=6, n_elements=5)
            self.assertEqual(greedy(inst).status, Status.FEASIBLE)


class TestExactAndBnB(unittest.TestCase):
    def test_bnb_matches_brute_force_on_random_instances(self):
        rng = random.Random(1234)
        for trial in range(40):
            inst = random_instance(rng, n_sets=rng.randint(2, 9), n_elements=6)
            exact = brute_force(inst)
            result = BranchAndBound(inst).solve()
            self.assertEqual(result.status, Status.OPTIMAL)
            self.assertEqual(result.profit, exact.profit, f"trial {trial}")
            self.assertEqual(result.upper_bound, exact.profit)
            self.assertTrue(inst.evaluate(result.selected).feasible)

    def test_greedy_never_beats_exact(self):
        rng = random.Random(99)
        for _ in range(30):
            inst = random_instance(rng, n_sets=7, n_elements=6)
            self.assertLessEqual(greedy(inst).profit, brute_force(inst).profit)

    def test_upper_bound_is_reliable_when_interrupted(self):
        rng = random.Random(2024)
        for _ in range(20):
            inst = random_instance(rng, n_sets=9, n_elements=6)
            exact = brute_force(inst)
            partial = BranchAndBound(inst).solve(max_nodes=3)
            if partial.status is Status.BUDGET_UNDECIDED:
                self.assertGreaterEqual(partial.upper_bound, exact.profit)
                self.assertLessEqual(partial.profit, exact.profit)

    def test_brute_force_refuses_large_instances(self):
        sets = [CoverageSet(f"s{i}", 1, ["x"]) for i in range(30)]
        inst = Instance(sets, {"x": 1}, budget=30)
        with self.assertRaises(ValueError):
            brute_force(inst)

    def test_prune_records_carry_reasons(self):
        rng = random.Random(5)
        inst = random_instance(rng, n_sets=8, n_elements=6)
        result = BranchAndBound(inst).solve()
        self.assertTrue(result.prunes)
        for record in result.prunes:
            self.assertTrue(record.reason)
            self.assertTrue(record.detail)
            if record.reason == "bound":
                self.assertIn("<= incumbent", record.detail)
            if record.reason == "budget":
                self.assertIn("budget", record.detail)


class TestResume(unittest.TestCase):
    def test_resume_matches_uninterrupted_run(self):
        rng = random.Random(31337)
        inst = random_instance(rng, n_sets=10, n_elements=7)
        reference = BranchAndBound(inst).solve()
        self.assertEqual(reference.status, Status.OPTIMAL)

        solver = BranchAndBound(inst)
        partial = solver.solve(max_nodes=2)
        self.assertEqual(partial.status, Status.BUDGET_UNDECIDED)
        with tempfile.TemporaryDirectory() as tmp:
            path = os.path.join(tmp, "checkpoint.json")
            solver.save_checkpoint(path)
            resumed = BranchAndBound.load(inst, path)
        final = resumed.solve()
        self.assertEqual(final.status, Status.OPTIMAL)
        self.assertEqual(final.profit, reference.profit)
        self.assertEqual(final.selected, reference.selected)

    def test_checkpoint_rejected_after_instance_change(self):
        inst = Instance(
            [CoverageSet("a", 1, ["x"]), CoverageSet("b", 1, ["y"])],
            {"x": 1, "y": 1},
            budget=2,
        )
        solver = BranchAndBound(inst)
        solver.solve(max_nodes=1)
        with tempfile.TemporaryDirectory() as tmp:
            path = os.path.join(tmp, "checkpoint.json")
            solver.save_checkpoint(path)
            smaller = inst.remove_set("a")
            with self.assertRaises(CheckpointMismatch):
                BranchAndBound.load(smaller, path)


class TestWitnessRevalidation(unittest.TestCase):
    def _instance(self):
        return Instance(
            [
                CoverageSet("core", 2, ["x", "y"]),
                CoverageSet("addon", 1, ["z"], requires=["core"]),
                CoverageSet("solo", 1, ["z"]),
            ],
            {"x": 3, "y": 2, "z": 4},
            budget=3,
        )

    def test_witness_roundtrip(self):
        inst = self._instance()
        result = BranchAndBound(inst).solve()
        witness = make_witness(inst, result.selected, result.profit, result.status)
        self.assertTrue(verify_witness(inst, witness).ok)

    def test_old_witness_fails_after_set_removal(self):
        inst = self._instance()
        result = BranchAndBound(inst).solve()
        witness = make_witness(inst, result.selected, result.profit, result.status)
        smaller = inst.remove_set("core")
        check = verify_witness(smaller, witness)
        self.assertFalse(check.ok)
        self.assertTrue(check.reasons)
        # Re-solving on the changed instance yields a fresh, valid witness.
        rerun = BranchAndBound(smaller).solve()
        fresh = make_witness(smaller, rerun.selected, rerun.profit, rerun.status)
        self.assertTrue(verify_witness(smaller, fresh).ok)

    def test_witness_detects_tampered_profit(self):
        inst = self._instance()
        witness = make_witness(inst, ["solo"], Fraction(999), Status.FEASIBLE)
        self.assertFalse(verify_witness(inst, witness).ok)


class TestGapReport(unittest.TestCase):
    def test_gap_values(self):
        report = gap_report(Fraction(8), Fraction(10))
        self.assertEqual(report.absolute_gap, 2)
        self.assertEqual(report.relative_gap, Fraction(1, 5))
        self.assertIn("no approximation guarantee", report.note)

    def test_gap_with_zero_reference(self):
        report = gap_report(Fraction(0), Fraction(0))
        self.assertIsNone(report.relative_gap)


if __name__ == "__main__":
    unittest.main()
