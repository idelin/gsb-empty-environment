import unittest

from k_anonymizer.hierarchy import Hierarchy
from k_anonymizer.report import build_report, verify_report
from k_anonymizer.search import (
    Status,
    beam_search,
    equivalence_classes,
    exact_search,
)


def fixture():
    """手工可验证的小例子。

    f1: a,b -> ab；c -> c1；再 -> *（3 层）
    f2: x,y -> *（2 层）
    记录: (a,x) (b,x) (c,x) (c,y)，k=2
    唯一最优可行组合为 (1,1)，损失 = ((0.5+0.5+0+0)/4 + 1) / 2 = 0.625。
    """
    f1 = Hierarchy(
        "f1",
        ["a", "b", "c"],
        {"a": ["ab"], "b": ["ab"], "c": ["c1"], "ab": ["*"], "c1": ["*"]},
    )
    f2 = Hierarchy("f2", ["x", "y"], {"x": ["*"], "y": ["*"]})
    records = [("a", "x"), ("b", "x"), ("c", "x"), ("c", "y")]
    return records, [f1, f2]


class TestExactSearch(unittest.TestCase):
    def test_optimal_solution(self):
        records, hierarchies = fixture()
        result = exact_search(records, hierarchies, k=2)
        self.assertEqual(result.status, Status.OPTIMAL)
        self.assertEqual(result.levels, (1, 1))
        self.assertAlmostEqual(result.loss, 0.625)
        self.assertEqual(result.explored, 6)
        self.assertEqual(result.space_size, 6)

    def test_infeasible_when_k_too_large(self):
        records, hierarchies = fixture()
        result = exact_search(records, hierarchies, k=5)
        self.assertEqual(result.status, Status.INFEASIBLE)
        self.assertIsNone(result.levels)
        self.assertIsNone(result.loss)
        self.assertEqual(result.explored, result.space_size)

    def test_equivalence_class_counts(self):
        records, hierarchies = fixture()
        groups = equivalence_classes(records, hierarchies, (1, 1))
        self.assertEqual(sorted(groups.values(), reverse=True), [2, 2])
        self.assertEqual(sum(groups.values()), len(records))


class TestBeamSearch(unittest.TestCase):
    def test_beam_matches_exact_when_budget_ample(self):
        records, hierarchies = fixture()
        result = beam_search(records, hierarchies, k=2, width=10, budget=100)
        # 评估数覆盖全空间后升级为 OPTIMAL
        self.assertEqual(result.status, Status.OPTIMAL)
        self.assertEqual(result.levels, (1, 1))
        self.assertAlmostEqual(result.loss, 0.625)
        self.assertEqual(result.explored, 6)

    def test_converging_states_deduped(self):
        # (1,1) 可从 (0,1) 与 (1,0) 两条路径到达；去重后每状态只评估一次
        records, hierarchies = fixture()
        result = beam_search(records, hierarchies, k=2, width=10, budget=100)
        self.assertLessEqual(result.explored, result.space_size)

    def test_truncated_no_solution(self):
        records, hierarchies = fixture()
        result = beam_search(records, hierarchies, k=2, width=1, budget=1)
        self.assertEqual(result.status, Status.NO_SOLUTION_TRUNCATED)
        self.assertIsNone(result.levels)
        self.assertLess(result.explored, result.space_size)

    def test_truncated_feasible(self):
        records, hierarchies = fixture()
        # 预算 5：评估 (0,0),(1,0),(0,1),(2,0),(1,1)，找到可行解但未搜完 6 状态空间
        result = beam_search(records, hierarchies, k=2, width=2, budget=5)
        self.assertEqual(result.status, Status.FEASIBLE_TRUNCATED)
        self.assertEqual(result.levels, (1, 1))
        self.assertLess(result.explored, result.space_size)

    def test_invalid_params(self):
        records, hierarchies = fixture()
        with self.assertRaises(ValueError):
            beam_search(records, hierarchies, k=2, width=0)
        with self.assertRaises(ValueError):
            beam_search(records, hierarchies, k=2, budget=0)


class TestReport(unittest.TestCase):
    def test_report_verifiable(self):
        records, hierarchies = fixture()
        result = exact_search(records, hierarchies, k=2)
        report = build_report(result, records, hierarchies, k=2)
        ec = report["equivalence_classes"]
        self.assertEqual(ec["sizes"], [2, 2])
        self.assertEqual(ec["num_classes"], 2)
        self.assertEqual(ec["min_size"], 2)
        self.assertEqual(ec["sum_sizes"], 4)
        self.assertTrue(ec["k_satisfied"])
        self.assertTrue(verify_report(report, records, hierarchies, k=2))

    def test_verify_detects_tampering(self):
        records, hierarchies = fixture()
        result = exact_search(records, hierarchies, k=2)
        report = build_report(result, records, hierarchies, k=2)
        report["equivalence_classes"]["sizes"] = [3, 1]
        self.assertFalse(verify_report(report, records, hierarchies, k=2))

    def test_determinism(self):
        from k_anonymizer.dataset import generate_synthetic

        records1, h1 = generate_synthetic(100, 7)
        records2, h2 = generate_synthetic(100, 7)
        self.assertEqual(records1, records2)
        r1 = exact_search(records1, h1, k=5)
        r2 = exact_search(records2, h2, k=5)
        self.assertEqual(r1.levels, r2.levels)
        self.assertEqual(r1.loss, r2.loss)


if __name__ == "__main__":
    unittest.main()
