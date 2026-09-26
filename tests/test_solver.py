import itertools
import json
import random
import unittest
from fractions import Fraction
from pathlib import Path

from batchsched import (
    BUDGET_EXCEEDED,
    BUDGET_MET,
    BUDGET_UNDECIDED,
    FEASIBLE,
    INFEASIBLE,
    OPTIMAL,
    InstanceError,
    OversizeJobError,
    decide_budget,
    load_instance,
    solve,
    verify_schedule,
)

EXAMPLES = Path(__file__).resolve().parent.parent / "examples"


def make_instance(capacity, setup_cost, jobs):
    """jobs: list of (id, family, size, time, deadline, cost)."""
    return load_instance({
        "capacity": capacity,
        "setup_cost": setup_cost,
        "jobs": [
            {"id": jid, "family": fam, "size": s, "time": t, "deadline": d, "cost": c}
            for jid, fam, s, t, d, c in jobs
        ],
    })


def brute_force_min_batches(inst):
    """Independent exact check: every schedule is a permutation of jobs cut
    into consecutive segments; try all permutations and all cut positions."""
    n = len(inst.jobs)
    best = None
    for perm in itertools.permutations(range(n)):
        for cuts in range(1 << max(0, n - 1)):
            segments = []
            start = 0
            for i in range(n - 1):
                if cuts >> i & 1:
                    segments.append(perm[start:i + 1])
                    start = i + 1
            segments.append(perm[start:])
            t = Fraction(0)
            ok = True
            for seg in segments:
                if len({inst.jobs[i].family for i in seg}) != 1:
                    ok = False
                    break
                size = sum((inst.jobs[i].size for i in seg), Fraction(0))
                if size > inst.capacity:
                    ok = False
                    break
                t += max(inst.jobs[i].time for i in seg)
                if any(inst.jobs[i].deadline < t for i in seg):
                    ok = False
                    break
            if ok and (best is None or len(segments) < best):
                best = len(segments)
    return best


class CostModelTests(unittest.TestCase):
    def test_setup_cost_charged_per_batch_not_per_job(self):
        inst = make_instance(10, 7, [
            ("a", "F", 4, 2, 10, 1),
            ("b", "F", 5, 3, 10, 2),
        ])
        res = solve(inst)
        self.assertEqual(res.status, OPTIMAL)
        self.assertEqual(len(res.batches), 1)
        # one setup (7) + job costs (1 + 2), not two setups
        self.assertEqual(res.cost, Fraction(10))

    def test_incompatible_jobs_never_share_a_batch(self):
        # both jobs would fit in one batch by capacity, but families differ
        inst = make_instance(10, 5, [
            ("a", "F1", 1, 1, 10, 0),
            ("b", "F2", 1, 1, 10, 0),
        ])
        res = solve(inst)
        self.assertEqual(res.status, OPTIMAL)
        self.assertEqual(len(res.batches), 2)
        self.assertEqual(res.cost, Fraction(10))  # 2 setups, merging is forbidden

    def test_jobs_are_indivisible(self):
        inst = make_instance(10, 3, [
            ("a", "F", 7, 1, 10, 0),
            ("b", "F", 7, 1, 10, 0),
        ])
        res = solve(inst)
        self.assertEqual(res.status, OPTIMAL)
        self.assertEqual(len(res.batches), 2)  # 7 + 7 > 10, and no splitting
        self.assertEqual(res.cost, Fraction(6))

    def test_deadline_can_force_a_split(self):
        # batching a and b together takes time 5 > deadline(a) = 1
        inst = make_instance(10, 3, [
            ("a", "F", 1, 1, 1, 0),
            ("b", "F", 1, 5, 6, 0),
        ])
        res = solve(inst)
        self.assertEqual(res.status, OPTIMAL)
        self.assertEqual(len(res.batches), 2)
        self.assertEqual(res.cost, Fraction(6))

    def test_empty_instance(self):
        inst = load_instance({"capacity": 5, "setup_cost": 2, "jobs": []})
        res = solve(inst)
        self.assertEqual(res.status, OPTIMAL)
        self.assertEqual(res.cost, Fraction(0))
        self.assertEqual(res.batches, [])


class ValidationTests(unittest.TestCase):
    def test_oversize_job_rejected(self):
        with self.assertRaises(OversizeJobError):
            make_instance(10, 1, [("big", "F", 11, 1, 5, 0)])

    def test_invalid_field_rejected(self):
        with self.assertRaises(InstanceError):
            load_instance({"capacity": 10, "setup_cost": 1,
                           "jobs": [{"id": "x", "family": "F", "size": 0,
                                     "time": 1, "deadline": 1}]})

    def test_rational_numbers_parsed_exactly(self):
        inst = load_instance({
            "capacity": "3/2",
            "setup_cost": "5/2",
            "jobs": [
                {"id": "r1", "family": "A", "size": "3/4", "time": "1/2",
                 "deadline": 3, "cost": "1/2"},
                {"id": "r2", "family": "A", "size": "3/4", "time": 1,
                 "deadline": 5, "cost": 0},
                {"id": "r3", "family": "A", "size": "1/2", "time": "1/4",
                 "deadline": 6, "cost": "1/4"},
            ],
        })
        res = solve(inst)
        self.assertEqual(res.status, OPTIMAL)
        self.assertEqual(len(res.batches), 2)  # all three = 2 > 3/2
        self.assertEqual(res.cost, Fraction(23, 4))  # exact rational arithmetic


class ExactnessTests(unittest.TestCase):
    def test_matches_brute_force_on_random_instances(self):
        rng = random.Random(20260926)
        for trial in range(12):
            n = rng.randint(1, 6)
            capacity = rng.randint(4, 8)
            jobs = [
                (f"j{k}", rng.choice("AB"), rng.randint(1, capacity),
                 rng.randint(1, 4), rng.randint(1, 14), 0)
                for k in range(n)
            ]
            inst = make_instance(capacity, rng.randint(1, 4), jobs)
            res = solve(inst)
            expected = brute_force_min_batches(inst)
            if expected is None:
                self.assertEqual(res.status, INFEASIBLE, f"trial {trial}")
            else:
                self.assertEqual(res.status, OPTIMAL, f"trial {trial}")
                self.assertEqual(len(res.batches), expected, f"trial {trial}")
                idx = [[next(i for i, j in enumerate(inst.jobs) if j.jid == jid)
                        for jid in batch] for batch in res.batches]
                self.assertTrue(verify_schedule(inst, idx), f"trial {trial}")

    def test_node_limit_downgrades_to_feasible(self):
        # capacity lower bound (1 batch) is below the greedy solution
        # (2 batches, forced by the deadline), so the DP must explore nodes
        inst = make_instance(10, 4, [
            ("a", "F", 4, 1, 1, 0),
            ("b", "F", 6, 5, 6, 0),
        ])
        res = solve(inst, node_limit=0)
        self.assertEqual(res.status, FEASIBLE)  # solution known, not proven
        self.assertTrue(res.truncated)
        self.assertIsNotNone(res.cost)


class BudgetTests(unittest.TestCase):
    def setUp(self):
        with open(EXAMPLES / "optimal_small.json", encoding="utf-8") as fh:
            self.inst = load_instance(json.load(fh))
        # optimum for this instance is 16 (3 batches * 4 + job costs 4)

    def test_budget_met(self):
        res = decide_budget(self.inst, Fraction(16))
        self.assertEqual(res.status, BUDGET_MET)
        self.assertLessEqual(res.cost, Fraction(16))

    def test_budget_exceeded_proven(self):
        res = decide_budget(self.inst, Fraction(15))
        self.assertEqual(res.status, BUDGET_EXCEEDED)
        self.assertFalse(res.truncated)

    def test_budget_undecided_when_search_truncated(self):
        res = decide_budget(self.inst, Fraction(15), node_limit=0)
        self.assertEqual(res.status, BUDGET_UNDECIDED)

    def test_job_costs_alone_exceed_budget(self):
        inst = make_instance(10, 0, [("a", "F", 1, 1, 5, 9)])
        res = decide_budget(inst, Fraction(5))
        self.assertEqual(res.status, BUDGET_EXCEEDED)


class ExampleFileTests(unittest.TestCase):
    def load(self, name):
        with open(EXAMPLES / name, encoding="utf-8") as fh:
            return load_instance(json.load(fh))

    def test_optimal_small(self):
        res = solve(self.load("optimal_small.json"))
        self.assertEqual(res.status, OPTIMAL)
        self.assertEqual(res.cost, Fraction(16))
        self.assertEqual(len(res.batches), 3)

    def test_infeasible_deadline(self):
        res = solve(self.load("infeasible_deadline.json"))
        self.assertEqual(res.status, INFEASIBLE)

    def test_rejected_oversize(self):
        with self.assertRaises(OversizeJobError):
            self.load("rejected_oversize.json")


if __name__ == "__main__":
    unittest.main()
