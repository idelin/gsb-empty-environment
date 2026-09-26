import random
import unittest
from fractions import Fraction

from maintenance.model import Group, Instance, Job, Shift
from maintenance.samples import long_job_instance, sample_instance
from maintenance.solver import Status, brute_force, solve


def assert_selections_feel_feasible(testcase, instance, result):
    """校验返回解满足：工时完整、单班上限、组同时上限。"""
    jobs = {j.job_id: j for j in instance.jobs}
    limits = {g.name: g.limit for g in instance.groups}
    lengths = [s.length for s in instance.shifts]
    load = {(g.name, k): 0 for g in instance.groups for k in range(len(lengths))}
    seen = set()
    for a in result.selections:
        testcase.assertNotIn(a.job_id, seen)
        seen.add(a.job_id)
        job = jobs[a.job_id]
        testcase.assertEqual(sum(a.pieces), job.duration, "工时必须完整分配")
        for k, x in enumerate(a.pieces):
            testcase.assertLessEqual(x, lengths[k], "单作业单班次不超过班次长度")
            load[(job.group, k)] += x
    for (g, k), v in load.items():
        testcase.assertLessEqual(v, limits[g] * lengths[k],
                                 f"组 {g} 班次 {k} 超过同时上限折算容量")


class TestStatuses(unittest.TestCase):
    def test_zero_budget_is_budget_undecided(self):
        r = solve(sample_instance(), max_nodes=0)
        self.assertIs(r.status, Status.BUDGET_UNDECIDED)
        self.assertIsNone(r.value)
        self.assertEqual(r.selections, ())
        self.assertEqual(r.nodes_expanded, 0)

    def test_small_budget_is_feasible_not_proven(self):
        r = solve(sample_instance(), max_nodes=3)
        self.assertIs(r.status, Status.FEASIBLE)
        self.assertIsNotNone(r.value)
        self.assertFalse(r.proven_optimal)
        self.assertLessEqual(r.nodes_expanded, 3)
        assert_selections_feel_feasible(self, sample_instance(), r)

    def test_unlimited_budget_is_optimal(self):
        r = solve(sample_instance())
        self.assertIs(r.status, Status.OPTIMAL)
        self.assertTrue(r.proven_optimal)

    def test_empty_job_set(self):
        inst = Instance(shifts=(Shift("s1", 2),), groups=(Group("g1", 1),), jobs=())
        r = solve(inst)
        self.assertIs(r.status, Status.OPTIMAL)
        self.assertEqual(r.value, 0)
        self.assertEqual(r.selections, ())

    def test_negative_budget_rejected(self):
        with self.assertRaises(ValueError):
            solve(sample_instance(), max_nodes=-1)


class TestOptimality(unittest.TestCase):
    def test_sample_matches_brute_force(self):
        inst = sample_instance()
        self.assertEqual(solve(inst).value, brute_force(inst))

    def test_long_job_matches_brute_force(self):
        inst = long_job_instance()
        self.assertEqual(solve(inst).value, brute_force(inst))

    def test_random_instances_match_brute_force(self):
        rng = random.Random(20260926)
        for trial in range(30):
            shifts = tuple(
                Shift(f"s{k}", rng.randint(1, 4)) for k in range(rng.randint(1, 3))
            )
            groups = tuple(Group(f"g{g}", rng.randint(1, 2)) for g in range(2))
            jobs = tuple(
                Job(
                    f"j{i}",
                    f"g{rng.randint(0, 1)}",
                    rng.randint(1, 5),
                    Fraction(rng.randint(0, 9), rng.choice([1, 1, 2])),
                    Fraction(rng.randint(0, 3), rng.choice([1, 2])),
                )
                for i in range(rng.randint(0, 6))
            )
            inst = Instance(shifts=shifts, groups=groups, jobs=jobs)
            r = solve(inst)
            self.assertIs(r.status, Status.OPTIMAL)
            self.assertEqual(r.value, brute_force(inst), f"第 {trial} 例不一致")
            assert_selections_feel_feasible(self, inst, r)


class TestInterruptionCost(unittest.TestCase):
    def test_spanning_shifts_counts_extra_startup(self):
        inst = long_job_instance()
        r = solve(inst)
        self.assertIs(r.status, Status.OPTIMAL)
        sel = {a.job_id: a for a in r.selections}
        # F1: 10 - 2*3 = 4（跨 2 班），F2: 5 - 1 = 4，F3: 6 - 2*3 = 0 不选
        self.assertIn("F1", sel)
        self.assertIn("F2", sel)
        self.assertNotIn("F3", sel)
        self.assertEqual(sel["F1"].shifts_used, 2)
        self.assertEqual(r.value, Fraction(8))

    def test_unprofitable_interruption_rejected(self):
        inst = Instance(
            shifts=(Shift("s1", 2), Shift("s2", 2)),
            groups=(Group("g1", 1),),
            jobs=(Job("j1", "g1", 4, Fraction(5), Fraction(3)),),  # 5 - 2*3 < 0
        )
        r = solve(inst)
        self.assertEqual(r.value, 0)
        self.assertEqual(r.selections, ())


class TestExactRationalArithmetic(unittest.TestCase):
    def test_fractional_scores_exact(self):
        inst = Instance(
            shifts=(Shift("s1", 3),),
            groups=(Group("g1", 1),),
            jobs=(
                Job("a", "g1", 1, Fraction(1, 3), Fraction(0)),
                Job("b", "g1", 1, Fraction(1, 3), Fraction(0)),
                Job("c", "g1", 1, Fraction(1, 3), Fraction(0)),
            ),
        )
        r = solve(inst)
        self.assertEqual(r.value, Fraction(1))  # 精确等于 1，而非 0.999...

    def test_result_is_deterministic(self):
        inst = sample_instance()
        r1, r2 = solve(inst), solve(inst)
        self.assertEqual(r1.value, r2.value)
        self.assertEqual(r1.selections, r2.selections)


if __name__ == "__main__":
    unittest.main()
