import random
import unittest

from checkpoint.planner import checkpoint_capacity, dag_lower_bound
from checkpoint.planner import plan_chain_bruteforce, plan_chain_optimal
from checkpoint.planner import plan_dag_greedy, segment_cost


def random_chain(rng, n):
    sizes = [8 * rng.randint(1, 8) for _ in range(n + 1)]
    costs = [0.0] + [float(rng.randint(1, 9)) for _ in range(n)]
    cap_min = sizes[0] + 2 * max(sizes)
    budget = cap_min + rng.randint(0, sum(sizes))
    return sizes, costs, budget


class TestChainOptimality(unittest.TestCase):
    def test_dp_matches_bruteforce_on_random_chains(self):
        rng = random.Random(20240901)
        checked = 0
        for _ in range(60):
            n = rng.randint(2, 9)
            sizes, costs, budget = random_chain(rng, n)
            dp = plan_chain_optimal(sizes, costs, budget)
            bf = plan_chain_bruteforce(sizes, costs, budget)
            self.assertEqual(dp is None, bf is None)
            if dp is None:
                continue
            msg = "sizes=%r costs=%r budget=%r" % (sizes, costs, budget)
            self.assertAlmostEqual(dp[1], bf[1], places=9, msg=msg)
            checked += 1
        self.assertGreater(checked, 40)

    def test_dp_plan_is_feasible(self):
        rng = random.Random(7)
        for _ in range(30):
            n = rng.randint(2, 9)
            sizes, costs, budget = random_chain(rng, n)
            plan = plan_chain_optimal(sizes, costs, budget)
            if plan is None:
                continue
            cps, _ = plan
            self.assertEqual(cps[0], 0)
            cap = checkpoint_capacity(sizes, budget)
            self.assertLessEqual(sum(sizes[c] for c in cps), cap)

    def test_required_checkpoints(self):
        rng = random.Random(11)
        for _ in range(30):
            n = rng.randint(3, 9)
            sizes, costs, budget = random_chain(rng, n)
            required = (rng.randint(1, n),)
            dp = plan_chain_optimal(sizes, costs, budget, required=required)
            bf = plan_chain_bruteforce(sizes, costs, budget,
                                       required=required)
            self.assertEqual(dp is None, bf is None)
            if dp is None:
                continue
            self.assertIn(required[0], dp[0])
            self.assertAlmostEqual(dp[1], bf[1], places=9)

    def test_infeasible_budget_returns_none(self):
        sizes = [64, 64, 64, 64]
        costs = [0.0, 1.0, 1.0, 1.0]
        self.assertIsNone(plan_chain_optimal(sizes, costs, budget=100))
        self.assertIsNone(plan_chain_bruteforce(sizes, costs, budget=100))

    def test_segment_cost_matches_manual_trace(self):
        costs = [0.0, 1.0, 2.0, 4.0]
        self.assertEqual(segment_cost(costs, 0, 4, 3),
                         3 * 1.0 + 2 * 2.0 + 4.0)


class TestDagHeuristic(unittest.TestCase):
    def test_greedy_respects_budget_and_bound(self):
        rng = random.Random(3)
        for _ in range(30):
            names = ["n%d" % i for i in range(8)]
            sizes = {nm: 8 * rng.randint(1, 8) for nm in names}
            costs = {nm: float(rng.randint(1, 9)) for nm in names}
            budget = 2 * max(sizes.values()) + rng.randint(
                0, sum(sizes.values()))
            plan = plan_dag_greedy(sizes, costs, budget)
            lb = dag_lower_bound(sizes, costs, budget)
            if plan is None:
                continue
            cap = budget - 2 * max(sizes.values())
            self.assertLessEqual(sum(sizes[nm] for nm in plan), cap)
            optimistic = sum(c for nm, c in costs.items() if nm not in plan)
            self.assertGreaterEqual(optimistic, lb - 1e-9)

    def test_lower_bound_is_tight_when_all_fits(self):
        sizes = {"a": 8, "b": 8}
        costs = {"a": 3.0, "b": 5.0}
        budget = 16 + 2 * 8
        self.assertEqual(dag_lower_bound(sizes, costs, budget), 0.0)


if __name__ == "__main__":
    unittest.main()
