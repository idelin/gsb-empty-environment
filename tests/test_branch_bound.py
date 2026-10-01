import json
import random
import unittest
from fractions import Fraction

from budget_coverage import (
    BranchBoundSolver,
    Problem,
    Status,
    exact_solve,
)


def random_problem(seed):
    rng = random.Random(seed)
    n_elems, n_sets = 6, 7
    weights = [rng.randint(1, 6) for _ in range(n_elems)]
    costs = [rng.randint(0, 4) for _ in range(n_sets)]  # 允许零成本
    elements = [set(rng.sample(range(n_elems), rng.randint(0, 3)))
                for _ in range(n_sets)]
    deps = {}
    for i in range(1, n_sets):
        if rng.random() < 0.25:
            deps[f"s{i}"] = [f"s{rng.randrange(i)}"]  # 只依赖更小下标，无环
    mutex = []
    if rng.random() < 0.5:
        a, b = rng.sample(range(n_sets), 2)
        mutex = [[f"s{a}", f"s{b}"]]
    min_cov = {}
    if rng.random() < 0.5:
        min_cov = {rng.randrange(n_elems): 1}
    return Problem.create(
        element_weights=weights,
        set_ids=[f"s{i}" for i in range(n_sets)],
        set_costs=costs,
        set_elements=elements,
        budget=rng.randint(2, 8),
        dependencies=deps,
        mutex_groups=mutex,
        min_coverage=min_cov,
    )


class TestBranchBound(unittest.TestCase):
    def test_matches_exact_on_random_instances(self):
        for seed in range(10):
            p = random_problem(seed)
            exact = exact_solve(p)
            r = BranchBoundSolver(p).search()
            self.assertEqual(r.status, exact.status, f"seed={seed}")
            self.assertEqual(r.profit, exact.profit, f"seed={seed}")
            if r.selected is not None:
                self.assertFalse(p.violations(p.selection_by_ids(r.selected)),
                                 f"seed={seed}")

    def test_resume_via_snapshot_matches_uninterrupted(self):
        p = random_problem(42)
        full = BranchBoundSolver(p).search()
        solver = BranchBoundSolver(p)
        while True:
            solver.search(max_nodes=2)
            snap = solver.snapshot()
            json.dumps(snap)  # 快照必须可 JSON 序列化
            solver = BranchBoundSolver.restore(p, snap)
            if not solver.stack:
                break
        self.assertEqual(solver.incumbent_profit, full.profit)
        self.assertEqual(solver.nodes_explored, full.nodes_explored)

    def test_partial_search_statuses_and_bound_validity(self):
        for seed in range(10):
            p = random_problem(seed)
            exact = exact_solve(p)
            r = BranchBoundSolver(p).search(max_nodes=1)
            if r.status is Status.UNDECIDED:
                self.assertIsNone(r.profit)
                self.assertIsNotNone(r.upper_bound)
            elif r.status is Status.FEASIBLE:
                self.assertLessEqual(r.lower_bound, r.upper_bound)
            elif r.status is Status.INFEASIBLE:
                self.assertEqual(exact.status, Status.INFEASIBLE)
            if exact.profit is not None and r.upper_bound is not None:
                self.assertGreaterEqual(r.upper_bound, exact.profit,
                                        f"seed={seed}: 上界必须可靠")

    def test_prune_records_have_bound_justifications(self):
        p = Problem.create(
            element_weights=[5, 5],
            set_ids=["A", "B"],
            set_costs=[5, 5],
            set_elements=[{0}, {1}],
            budget=5,
        )
        r = BranchBoundSolver(p).search()
        self.assertEqual(r.status, Status.OPTIMAL)
        self.assertEqual(r.profit, Fraction(5))
        reasons = {pr.reason for pr in r.prunes}
        self.assertIn("budget", reasons)
        self.assertIn("bound", reasons)
        for pr in r.prunes:
            self.assertTrue(pr.detail)
            if pr.reason == "bound":
                self.assertIsNotNone(pr.bound)
                self.assertIsNotNone(pr.incumbent)

    def test_zero_cost_sets_in_bound_no_division_by_zero(self):
        p = Problem.create(
            element_weights=[7, 1],
            set_ids=["Z", "Y"],
            set_costs=[0, 3],
            set_elements=[{0}, {1}],
            budget=1,
        )
        r = BranchBoundSolver(p).search()
        self.assertEqual(r.status, Status.OPTIMAL)
        self.assertEqual(r.profit, Fraction(7))

    def test_snapshot_rejects_changed_problem(self):
        p = random_problem(7)
        solver = BranchBoundSolver(p)
        solver.search(max_nodes=2)
        snap = solver.snapshot()
        p.delete_set("s0")
        with self.assertRaises(ValueError):
            BranchBoundSolver.restore(p, snap)


if __name__ == "__main__":
    unittest.main()
