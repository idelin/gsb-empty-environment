"""Tests for the retractable min/max difference-constraint network."""

import os
import sys
import unittest

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from tempnet import (
    MinMaxNetwork,
    InconsistentNetwork,
    BudgetExceeded,
)
from tempnet.verifier import run_differential, RandomScenario


class TestFeasibilityAndEvidence(unittest.TestCase):
    def test_spec_contradiction_ba_3_5_vs_ab_ge_0(self):
        # B-A in [3,5] together with A-B >= 0 is exactly infeasible.
        net = MinMaxNetwork(["A", "B"])
        net.add_interval("gap", "A", "B", 3, 5)
        with self.assertRaises(InconsistentNetwork) as ctx:
            net.add_interval("order", "B", "A", 0, None)
        cycle = ctx.exception.cycle
        self.assertLess(cycle.total, 0)
        self.assertEqual(set(cycle.constraint_ids), {"gap", "order"})
        self.assertTrue(net.infeasible)

    def test_one_tick_conflict_detected_without_epsilon(self):
        # Requiring B-A >= 4 and B-A <= 3 differs by exactly one tick.
        net = MinMaxNetwork(["A", "B"])
        net.add_min("lo", "A", "B", 4)
        with self.assertRaises(InconsistentNetwork) as ctx:
            net.add_max("hi", "A", "B", 3)
        self.assertEqual(set(ctx.exception.cycle.constraint_ids), {"lo", "hi"})

    def test_boundary_inclusive_is_feasible(self):
        # B-A >= 3 and B-A <= 3 fixes B-A == 3 exactly.
        net = MinMaxNetwork(["A", "B"])
        net.add_min("lo", "A", "B", 3)
        net.add_max("hi", "A", "B", 3)
        net.anchor("A", 0)
        self.assertEqual(net.bounds("B").fixed, 3)

    def test_self_difference_must_be_zero(self):
        net = MinMaxNetwork(["A"])
        with self.assertRaises(InconsistentNetwork) as ctx:
            net.add_interval("bad", "A", "A", None, -1)  # A-A <= -1
        self.assertEqual(ctx.exception.cycle.constraint_ids, ("bad",))

    def test_empty_interval_rejected_upfront(self):
        net = MinMaxNetwork(["A", "B"])
        with self.assertRaises(ValueError):
            net.add_interval("empty", "A", "B", 5, 4)


class TestBoundsAndSchedules(unittest.TestCase):
    def _anchored_sample(self):
        net = MinMaxNetwork(["A", "B", "C", "solo"])
        net.add_exact("fixA", None, "A", 10)
        net.add_interval("ab", "A", "B", 3, 5)
        net.add_min("bc", "B", "C", 2)
        return net

    def test_earliest_latest_windows(self):
        net = self._anchored_sample()
        self.assertEqual(net.bounds("A").fixed, 10)
        self.assertEqual((net.bounds("B").earliest, net.bounds("B").latest),
                         (13, 15))
        self.assertEqual((net.bounds("C").earliest, net.bounds("C").latest),
                         (15, None))

    def test_unconstrained_event_gets_full_range(self):
        net = self._anchored_sample()
        b = net.bounds("solo")
        self.assertTrue(b.unbounded)
        self.assertIsNone(b.earliest)
        self.assertIsNone(b.latest)
        self.assertIsNone(b.fixed)

    def test_schedule_satisfies_all_constraints(self):
        net = self._anchored_sample()
        assignment = net.solve()
        self.assertEqual(assignment["A"], 10)
        self.assertIn(assignment["B"], (13, 14, 15))
        self.assertGreaterEqual(assignment["C"] - assignment["B"], 2)
        self.assertEqual(net.verify_assignment(assignment), [])

    def test_translation_freedom_without_anchor(self):
        # Relative-only network: every uniform translation is feasible.
        net = MinMaxNetwork(["A", "B"])
        net.add_interval("ab", "A", "B", 3, 5)
        s0 = net.solve()
        self.assertEqual(s0["B"] - s0["A"], 3)  # normalized earliest
        # Bounds are both unbounded when there is no anchor at all.
        self.assertTrue(net.bounds("A").unbounded)
        self.assertTrue(net.bounds("B").unbounded)
        # Anchoring any event fixes the translation deterministically.
        net.anchor("A", 100)
        s1 = net.solve()
        self.assertEqual(s1["A"], 100)
        self.assertEqual(s1["B"], 103)
        self.assertEqual(net.verify_assignment(s1), [])

    def test_strict_solve_rejects_free_component(self):
        from tempnet import NoSolution
        net = MinMaxNetwork(["A", "solo"])
        net.add_exact("fix", None, "A", 0)
        with self.assertRaises(NoSolution):
            net.solve(strict=True)


class TestRetraction(unittest.TestCase):
    def test_push_pop_relaxes_unsupported_bounds(self):
        net = MinMaxNetwork(["A", "B"])
        net.add_exact("fix", None, "A", 0)
        net.add_interval("loose", "A", "B", 0, 10)
        self.assertEqual(net.bounds("B").latest, 10)

        net.push()
        net.add_max("tight", "A", "B", 3)
        self.assertEqual(net.bounds("B").latest, 3)
        net.pop()

        # Support withdrawn: the tighter bound must be relaxed again.
        self.assertEqual(net.bounds("B").latest, 10)
        self.assertNotIn("tight", [c.cid for c in net.constraints])

    def test_nested_push_commit_pop(self):
        net = MinMaxNetwork(["A", "B"])
        net.add_exact("fix", None, "A", 0)
        net.add_min("base", "A", "B", 0)
        net.push()
        net.add_max("outer", "A", "B", 10)
        net.push()
        net.add_max("inner", "A", "B", 6)
        self.assertEqual(net.bounds("B").latest, 6)
        net.commit()  # inner folded into outer level
        net.pop()     # withdraws outer + inner
        self.assertIsNone(net.bounds("B").latest)
        self.assertEqual(net.depth(), 0)

    def test_contradictory_hypothesis_popped_restores_feasibility(self):
        net = MinMaxNetwork(["A", "B"])
        net.add_interval("base", "A", "B", 1, 10)
        net.push()
        with self.assertRaises(InconsistentNetwork):
            net.add_min("bad", "A", "B", 11)
        self.assertTrue(net.infeasible)
        net.pop()
        self.assertFalse(net.infeasible)
        self.assertEqual(net.verify_assignment(net.solve()), [])

    def test_remove_single_constraint(self):
        net = MinMaxNetwork(["A", "B"])
        net.add_exact("fix", None, "A", 0)
        net.add_min("keep", "A", "B", 2)
        net.add_max("drop", "A", "B", 2)
        self.assertEqual(net.bounds("B").fixed, 2)
        net.remove_constraint("drop")
        self.assertEqual(net.bounds("B").earliest, 2)
        self.assertIsNone(net.bounds("B").latest)

    def test_versions_save_restore_and_history(self):
        net = MinMaxNetwork(["A", "B"])
        net.add_exact("fix", None, "A", 0)
        net.add_min("m", "A", "B", 1)
        net.save_version("v1")
        net.add_max("cap", "A", "B", 4)
        self.assertEqual(net.bounds("B").latest, 4)
        net.restore_version("v1")
        self.assertIsNone(net.bounds("B").latest)
        self.assertEqual(net.bounds("B").earliest, 1)
        self.assertIn("v1", net.versions())


class TestBudget(unittest.TestCase):
    def test_budget_interruption_is_atomic_and_never_pseudo_feasible(self):
        net = MinMaxNetwork([f"x{i}" for i in range(6)])
        for i in range(5):
            net.add_min(f"m{i}", f"x{i}", f"x{i + 1}", 1)
        before = [row[:] for row in net._d]
        with self.assertRaises(BudgetExceeded):
            # Closure effect of the shortcut needs more than 3 relaxations.
            net.add_max("shortcut", "x0", "x5", 9, budget=3)
        # Network unchanged: same closure, edges and constraints.
        self.assertEqual(net._d, before)
        self.assertFalse(net.infeasible)
        self.assertNotIn("shortcut", [c.cid for c in net.constraints])
        # It is still a genuinely feasible, solvable network.
        self.assertEqual(net.verify_assignment(net.solve()), [])

    def test_zero_budget_accepts_only_redundant_edge(self):
        net = MinMaxNetwork(["A", "B"])
        net.add_max("m", "A", "B", 5)
        # Same-weight duplicate: zero new relaxations required.
        net.add_max("dup", "A", "B", 5, budget=0)
        with self.assertRaises(BudgetExceeded):
            net.add_max("tighter", "A", "B", 4, budget=0)

    def test_sufficient_budget_succeeds(self):
        net = MinMaxNetwork([f"x{i}" for i in range(6)])
        for i in range(5):
            net.add_min(f"m{i}", f"x{i}", f"x{i + 1}", 1)
        net.add_max("shortcut", "x0", "x5", 9, budget=10_000)
        self.assertEqual(net.shortest("x0", "x5"), 9)   # the new upper bound
        self.assertEqual(net.shortest("x5", "x0"), -5)  # chain lower bound


class TestIndependentVerifier(unittest.TestCase):
    def test_random_updates_match_full_algorithm(self):
        checks = run_differential(
            RandomScenario(seed=20260926, n_events=7, n_steps=500)
        )
        self.assertGreaterEqual(checks, 300)

    def test_random_updates_heavy_retraction(self):
        checks = run_differential(
            RandomScenario(seed=4242, n_events=5, n_steps=400,
                           pop_probability=0.6, max_weight=8)
        )
        self.assertGreaterEqual(checks, 200)


if __name__ == "__main__":
    unittest.main(verbosity=2)
