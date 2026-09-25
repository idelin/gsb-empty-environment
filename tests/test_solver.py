import unittest

from migration import (INFEASIBLE, OPTIMAL, UNDECIDED, Batch, Column, Type,
                       build_plan, problem_from_batches, solve, verify)


def make_problem(weights, initial, capacities, budget=10**9, affinity=None,
                 anti=None):
    pids = sorted(weights)
    parts = Batch([Column("pid", Type.STRING, pids),
                   Column("weight", Type.INT64, [weights[p] for p in pids]),
                   Column("slot", Type.STRING, [initial[p] for p in pids])])
    sids = sorted(capacities)
    slots = Batch([Column("sid", Type.STRING, sids),
                   Column("capacity", Type.INT64, [capacities[s] for s in sids])])

    def pairs(batch_pairs):
        if not batch_pairs:
            return None
        return Batch([Column("left", Type.STRING, [p[0] for p in batch_pairs]),
                      Column("right", Type.STRING, [p[1] for p in batch_pairs])])

    return problem_from_batches(parts, slots, budget=budget,
                                affinity=pairs(affinity),
                                anti_affinity=pairs(anti))


class TestExactSolver(unittest.TestCase):
    def setUp(self):
        self.weights = {"a": 60, "b": 40, "c": 50, "d": 30}
        self.initial = {"a": "s0", "b": "s0", "c": "s1", "d": "s1"}
        self.caps = {"s0": 100, "s1": 100}

    def test_min_max_load_then_min_bytes(self):
        problem = make_problem(self.weights, self.initial, self.caps)
        result = solve(problem)
        self.assertEqual(result.status, OPTIMAL)
        self.assertEqual(result.max_load, 90)
        self.assertEqual(result.moved_bytes, 70)  # swap b and d
        self.assertEqual(result.lower_bound, 90)
        self.assertEqual(result.gap, 0)
        self.assertEqual(verify(problem, result.assignment), [])

    def test_budget_limits_moves(self):
        problem = make_problem(self.weights, self.initial, self.caps, budget=50)
        result = solve(problem)
        self.assertEqual(result.status, OPTIMAL)
        self.assertEqual(result.max_load, 100)  # cannot afford the 70-byte swap
        self.assertEqual(result.moved_bytes, 0)
        self.assertEqual(verify(problem, result.assignment), [])

    def test_anti_affinity_enforced(self):
        problem = make_problem({"x": 10, "y": 10, "z": 10},
                               {"x": "s0", "y": "s0", "z": "s0"},
                               {"s0": 30, "s1": 30}, anti=[("x", "y")])
        result = solve(problem)
        self.assertEqual(result.status, OPTIMAL)
        self.assertEqual(result.max_load, 20)
        self.assertNotEqual(result.assignment["x"], result.assignment["y"])
        self.assertEqual(verify(problem, result.assignment), [])

    def test_affinity_enforced(self):
        problem = make_problem({"x": 10, "y": 10, "z": 10},
                               {"x": "s0", "y": "s1", "z": "s1"},
                               {"s0": 30, "s1": 30}, affinity=[("x", "y")])
        result = solve(problem)
        self.assertEqual(result.status, OPTIMAL)
        self.assertEqual(result.max_load, 20)
        self.assertEqual(result.assignment["x"], result.assignment["y"])
        self.assertEqual(verify(problem, result.assignment), [])

    def test_oversized_partition_not_split(self):
        problem = make_problem({"big": 150, "small": 10},
                               {"big": "s0", "small": "s1"},
                               {"s0": 100, "s1": 100})
        result = solve(problem)
        self.assertEqual(result.status, INFEASIBLE)
        self.assertEqual(result.unmet_bytes, 50)
        self.assertIn("split", result.detail)

    def test_total_capacity_shortfall(self):
        problem = make_problem({"a": 80, "b": 80, "c": 80},
                               {"a": "s0", "b": "s0", "c": "s1"},
                               {"s0": 100, "s1": 100})
        result = solve(problem)
        self.assertEqual(result.status, INFEASIBLE)
        self.assertEqual(result.unmet_bytes, 40)

    def test_undecided_reports_bound_and_gap(self):
        problem = make_problem(self.weights, self.initial, self.caps)
        result = solve(problem, node_limit=1)
        self.assertEqual(result.status, UNDECIDED)
        self.assertIsNone(result.assignment)
        self.assertEqual(result.lower_bound, 90)  # proven bound still reported
        self.assertIsNone(result.gap)

    def test_deterministic_plan(self):
        problem = make_problem(self.weights, self.initial, self.caps)
        r1, r2 = solve(problem), solve(problem)
        self.assertEqual(r1.assignment, r2.assignment)
        p1 = build_plan(problem, r1, "plan", 1)
        p2 = build_plan(problem, r2, "plan", 1)
        self.assertEqual(p1.moves, p2.moves)
        self.assertEqual(list(p1.moves),
                         sorted(p1.moves, key=lambda m: (m.pid, m.src, m.dst)))

    def test_exact_big_integer_arithmetic(self):
        big = 2**60
        weights = {"a": big + 3, "b": big, "c": big - 3}
        initial = {"a": "s0", "b": "s0", "c": "s1"}
        caps = {"s0": 2 * big, "s1": 2 * big}
        problem = make_problem(weights, initial, caps, budget=10 * big)
        result = solve(problem)
        self.assertEqual(result.status, OPTIMAL)
        self.assertEqual(result.max_load, 2 * big - 3)  # b + c exactly
        self.assertEqual(result.moved_bytes, big)

    def test_verify_detects_tampering(self):
        problem = make_problem(self.weights, self.initial, self.caps)
        bad = {"a": "s0", "b": "s0", "c": "s0", "d": "s1"}
        errors = verify(problem, bad)
        self.assertTrue(any("overflow" in e for e in errors))
        bad2 = dict(bad, d="nowhere")
        self.assertTrue(any("unknown slot" in e for e in verify(problem, bad2)))


if __name__ == "__main__":
    unittest.main()
