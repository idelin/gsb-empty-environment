"""unittest suite for the budgeted project-selection solver."""

from __future__ import annotations

import os
import random
import sys
import unittest
from fractions import Fraction
from itertools import combinations, product

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from project_select import (  # noqa: E402
    Instance,
    Project,
    check_selection,
    is_feasible_selection,
    solve,
)
from project_select.solver import (  # noqa: E402
    strongly_connected_components,
)


def brute_force_best(instance: Instance):
    """Independent reference implementation: enumerate project subsets."""
    pids = sorted(instance.projects)
    best_value = None
    best_set = frozenset()
    for bits in range(1 << len(pids)):
        sel = frozenset(p for i, p in enumerate(pids) if bits >> i & 1)
        if not is_feasible_selection(instance, sel):
            continue
        value = sum((instance.projects[p].profit for p in sel), Fraction(0))
        if best_value is None or value > best_value:
            best_value = value
            best_set = sel
    return best_value, best_set


class TestBasicRules(unittest.TestCase):
    def test_profitable_project_selected(self):
        inst = Instance([Project("a", 2, 5)], budget=10)
        res = solve(inst)
        self.assertEqual(res.status, "optimal")
        self.assertEqual(res.selected, frozenset({"a"}))
        self.assertEqual(res.total_profit, 5)
        self.assertEqual(res.total_cost, 2)

    def test_unprofitable_project_skipped_when_free_choice(self):
        inst = Instance([Project("a", 2, -5)], budget=10)
        res = solve(inst)
        self.assertEqual(res.selected, frozenset())
        self.assertEqual(res.total_profit, 0)
        self.assertFalse(res.budget_binding)

    def test_child_without_parent_is_illegal(self):
        inst = Instance(
            [Project("parent", 1, 0), Project("child", 1, 9, deps=["parent"])],
            budget=10)
        errs = check_selection(inst, {"child"})
        self.assertTrue(any("without its dependency" in e for e in errs))
        self.assertFalse(is_feasible_selection(inst, {"child"}))
        self.assertTrue(is_feasible_selection(inst, {"child", "parent"}))

    def test_negative_dependency_pulled_in_by_profitable_child(self):
        inst = Instance(
            [Project("parent", 10, -3),
             Project("child", 2, 10, deps=["parent"])],
            budget=12)
        res = solve(inst)
        self.assertEqual(res.selected, frozenset({"parent", "child"}))
        self.assertEqual(res.total_profit, 7)
        self.assertEqual(res.total_cost, 12)
        # Without the profitable child, the negative project stays out.
        res2 = solve(Instance([Project("parent", 10, -3)], budget=12))
        self.assertEqual(res2.selected, frozenset())

    def test_shared_dependency_paid_once(self):
        inst = Instance(
            [Project("lib", 3, 1),
             Project("app1", 2, 5, deps=["lib"]),
             Project("app2", 2, 5, deps=["lib"])],
            budget=20)
        res = solve(inst)
        self.assertEqual(res.selected, frozenset({"lib", "app1", "app2"}))
        # lib appears once in the union even though both apps depend on it.
        self.assertEqual(res.total_cost, 3 + 2 + 2)
        self.assertEqual(res.total_profit, 11)

    def test_shared_dependency_cost_merge_matters_for_budget(self):
        # Picking either app alone costs 5; picking both costs 7 (not 10).
        inst = Instance(
            [Project("lib", 3, 0),
             Project("app1", 2, 5, deps=["lib"]),
             Project("app2", 2, 5, deps=["lib"])],
            budget=8)
        res = solve(inst)
        self.assertEqual(res.total_cost, 7)
        self.assertEqual(res.total_profit, 10)
        tight = solve(Instance(inst.projects.values(), budget=6))
        self.assertEqual(tight.total_cost, 5)
        self.assertEqual(tight.total_profit, 5)


class TestMutexAndBudget(unittest.TestCase):
    def test_mutex_picks_better_one(self):
        inst = Instance(
            [Project("a", 1, 4), Project("b", 1, 7)],
            mutex=[("a", "b")], budget=10)
        res = solve(inst)
        self.assertEqual(res.selected, frozenset({"b"}))

    def test_mutex_forced_through_dependency(self):
        # A requires B; C conflicts with B, so A and C cannot coexist.
        inst = Instance(
            [Project("b", 1, 2),
             Project("a", 1, 9, deps=["b"]),
             Project("c", 1, 5)],
            mutex=[("b", "c")], budget=10)
        res = solve(inst)
        self.assertEqual(res.selected, frozenset({"a", "b"}))
        self.assertFalse(is_feasible_selection(inst, {"a", "c"}))

    def test_budget_binding_vs_undecided(self):
        # Two projects each costing 6 with profit 5, budget 6.
        inst = Instance(
            [Project("a", 6, 5), Project("b", 6, 5)], budget=6)
        res = solve(inst)
        self.assertEqual(res.total_profit, 5)
        self.assertTrue(res.budget_binding)       # budget is decisive
        # Generous budget: same optimum, leftover money, not binding.
        loose = solve(Instance(inst.projects.values(), budget=20))
        self.assertEqual(loose.total_profit, 10)
        self.assertEqual(loose.budget_remaining, 8)
        self.assertFalse(loose.budget_binding)

    def test_budget_undecided_with_unspendable_slack(self):
        # Best combo costs 5 but no item fits the 4 leftover -> slack.
        inst = Instance([Project("a", 5, 9)], budget=9)
        res = solve(inst)
        self.assertEqual(res.selected, frozenset({"a"}))
        self.assertEqual(res.budget_remaining, 4)
        self.assertFalse(res.budget_binding)

    def test_infeasible_required_exceeds_budget(self):
        inst = Instance([Project("a", 10, 100)], budget=5, required=["a"])
        res = solve(inst)
        self.assertEqual(res.status, "infeasible")

    def test_infeasible_required_mutex_pair(self):
        inst = Instance(
            [Project("a", 1, 1), Project("b", 1, 1)],
            mutex=[("a", "b")], budget=10, required=["a", "b"])
        self.assertEqual(solve(inst).status, "infeasible")

    def test_rational_data(self):
        inst = Instance(
            [Project("lib", Fraction(1, 2), Fraction(-1, 3)),
             Project("app", Fraction(1, 2), Fraction(7, 2), deps=["lib"])],
            budget=Fraction(3, 2))
        res = solve(inst)
        self.assertEqual(res.total_cost, Fraction(1))
        self.assertEqual(res.total_profit, Fraction(19, 6))
        self.assertEqual(res.selected, frozenset({"lib", "app"}))


class TestCycles(unittest.TestCase):
    def test_scc_detection(self):
        sccs = strongly_connected_components(
            ["a", "b", "c"],
            {"a": ["b"], "b": ["a"], "c": []})
        groups = {frozenset(s) for s in sccs}
        self.assertIn(frozenset({"a", "b"}), groups)
        self.assertIn(frozenset({"c"}), groups)

    def test_cycle_treated_as_whole_group(self):
        # a and b depend on each other: atomic group with cost 6, profit 8.
        inst = Instance(
            [Project("a", 2, 5, deps=["b"]),
             Project("b", 4, 3, deps=["a"])],
            budget=10)
        res = solve(inst, on_cycle="group")
        self.assertEqual(res.selected, frozenset({"a", "b"}))
        self.assertEqual(res.total_cost, 6)
        self.assertEqual(res.total_profit, 8)
        self.assertFalse(is_feasible_selection(inst, {"a"}))

    def test_unprofitable_cycle_group_rejected_by_optimizer(self):
        inst = Instance(
            [Project("a", 2, -5, deps=["b"]),
             Project("b", 4, -1, deps=["a"])],
            budget=10)
        res = solve(inst)
        self.assertEqual(res.selected, frozenset())

    def test_cycle_reject_policy(self):
        inst = Instance(
            [Project("a", 2, 5, deps=["b"]),
             Project("b", 4, 3, deps=["a"])],
            budget=10)
        res = solve(inst, on_cycle="reject")
        self.assertEqual(res.status, "rejected_cycle")
        self.assertEqual(set(res.cycle), {"a", "b"})

    def test_mutex_inside_cycle_group_disables_it(self):
        inst = Instance(
            [Project("a", 2, 50, deps=["b"]),
             Project("b", 4, 50, deps=["a"])],
            mutex=[("a", "b")], budget=100)
        # Atomic group is internally contradictory: it can never be chosen.
        res = solve(inst)
        self.assertEqual(res.selected, frozenset())
        req = solve(Instance(inst.projects.values(),
                             mutex=inst.mutex, budget=100, required=["a"]))
        self.assertEqual(req.status, "infeasible")


class TestRandomEquivalence(unittest.TestCase):
    """Compare the solver against independent subset enumeration."""

    def _random_instance(self, rng, with_required=False, rational=False):
        n = rng.randint(1, 6)
        pids = [f"p{i}" for i in range(n)]
        projects = []
        for i, pid in enumerate(pids):
            deps = tuple(rng.sample(pids[:i], rng.randint(0, min(2, i))))
            if rational:
                cost = Fraction(rng.randint(0, 5), rng.randint(1, 3))
                profit = Fraction(rng.randint(-4, 9), rng.randint(1, 3))
            else:
                cost = rng.randint(0, 5)
                profit = rng.randint(-4, 9)
            projects.append(Project(pid, cost, profit, deps))
        mutex = []
        for a, b in combinations(pids, 2):
            if rng.random() < 0.15:
                mutex.append((a, b))
        budget = Fraction(rng.randint(0, 12),
                          rng.randint(1, 2) if rational else 1)
        required = (rng.sample(pids, rng.randint(0, min(2, n)))
                    if with_required else [])
        return Instance(projects, mutex=mutex, budget=budget,
                        required=required)

    def test_matches_brute_force(self):
        rng = random.Random(20260926)
        for trial in range(400):
            with self.subTest(trial=trial):
                inst = self._random_instance(rng)
                res = solve(inst)
                ref_value, ref_set = brute_force_best(inst)
                self.assertEqual(res.total_profit, ref_value)
                self.assertTrue(is_feasible_selection(inst, res.selected))

    def test_matches_brute_force_with_required_and_rationals(self):
        rng = random.Random(4242)
        for trial in range(200):
            with self.subTest(trial=trial):
                inst = self._random_instance(
                    rng, with_required=True, rational=True)
                res = solve(inst)
                ref_value, _ = brute_force_best(inst)
                if ref_value is None:
                    self.assertEqual(res.status, "infeasible")
                else:
                    self.assertEqual(res.status, "optimal")
                    self.assertEqual(res.total_profit, ref_value)
                    self.assertTrue(
                        is_feasible_selection(inst, res.selected))
                    # Required projects must be present.
                    self.assertTrue(set(inst.required) <= res.selected)

    def test_cycle_instances_match_brute_force(self):
        rng = random.Random(7)
        for trial in range(100):
            n = rng.randint(2, 5)
            pids = [f"x{i}" for i in range(n)]
            # ensure at least one cycle: p0 <-> p1
            extra = {pids[0]: {pids[1]}, pids[1]: {pids[0]}}
            projects = []
            for i, pid in enumerate(pids):
                deps = set(extra.get(pid, ()))
                deps.update(rng.sample(pids[2:],
                                       rng.randint(0, max(0, n - 3))))
                projects.append(Project(
                    pid, rng.randint(0, 4), rng.randint(-3, 8),
                    tuple(deps)))
            mutex = [(a, b) for a, b in combinations(pids, 2)
                     if rng.random() < 0.1]
            inst = Instance(projects, mutex=mutex, budget=rng.randint(0, 10))
            with self.subTest(trial=trial):
                res = solve(inst)
                ref_value, _ = brute_force_best(inst)
                self.assertEqual(res.total_profit, ref_value)
                self.assertTrue(is_feasible_selection(inst, res.selected))


if __name__ == "__main__":
    unittest.main(verbosity=2)
