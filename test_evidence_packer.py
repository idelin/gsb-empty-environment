#!/usr/bin/env python3
"""evidence_packer 的 unittest 测试。所有数据均为合成数据。"""
import json
import unittest

from evidence_packer import (
    EvidenceFile,
    Instance,
    Solver,
    build_report,
    load_instance,
    verify_selection,
)


def make_instance(capacity, files):
    """files: list of (id, size, value, deps, group)。"""
    return Instance(
        capacity=capacity,
        files={
            fid: EvidenceFile(
                fid=fid, size=size, value=value,
                deps=tuple(deps), group=group)
            for fid, size, value, deps, group in files
        },
    )


def solve(capacity, files, budget=None):
    inst = make_instance(capacity, files)
    return inst, Solver(inst, node_budget=budget).solve()


class AcceptanceTest(unittest.TestCase):
    """题目验收边界：a 大小2价值5 依赖 b 大小3价值0。"""

    def setUp(self):
        self.files = [("a", 2, 5, ["b"], None), ("b", 3, 0, [], None)]

    def test_capacity_4_rejects_a(self):
        _, sol = solve(4, self.files)
        self.assertNotIn("a", sol.files)
        self.assertEqual(sol.total_value, 0)
        self.assertEqual(sol.files, ())  # 空集优于只带价值0的 b
        self.assertTrue(sol.proven_optimal)

    def test_capacity_5_takes_a_and_b(self):
        inst, sol = solve(5, self.files)
        self.assertEqual(sol.files, ("a", "b"))
        self.assertEqual(sol.total_size, 5)  # 不得遗漏 b 报大小 2
        self.assertEqual(sol.total_value, 5)
        self.assertTrue(sol.proven_optimal)
        self.assertEqual(verify_selection(inst.files, 5, sol.files), [])


class DependencyClosureTest(unittest.TestCase):

    def test_transitive_closure(self):
        # a->b->c，选 a 必须带出 b、c。
        files = [("a", 1, 10, ["b"], None), ("b", 1, 0, ["c"], None),
                 ("c", 1, 0, [], None)]
        inst, sol = solve(3, files)
        self.assertEqual(sol.files, ("a", "b", "c"))
        self.assertEqual(sol.total_size, 3)
        self.assertEqual(verify_selection(inst.files, 3, sol.files), [])

    def test_shared_dependency_counted_once(self):
        # c 和 d 都依赖 b；选 c+d 时 b 的大小只计一次。
        files = [("b", 2, 0, [], None), ("c", 1, 4, ["b"], None),
                 ("d", 1, 4, ["b"], None)]
        _, sol = solve(4, files)  # 2+1+1=4 恰好装下
        self.assertEqual(sol.files, ("b", "c", "d"))
        self.assertEqual(sol.total_size, 4)
        self.assertEqual(sol.total_value, 8)

    def test_dependency_can_be_selected_independently(self):
        # b 自身价值高时，不选 a 也可单独选 b。
        files = [("a", 2, 1, ["b"], None), ("b", 3, 10, [], None)]
        _, sol = solve(3, files)
        self.assertEqual(sol.files, ("b",))
        self.assertEqual(sol.total_value, 10)


class MutexGroupTest(unittest.TestCase):

    def test_only_one_version_per_group(self):
        # 同一版本组 v 的两个互斥版本，选价值高者。
        files = [("x1", 2, 5, [], "v"), ("x2", 2, 7, [], "v")]
        _, sol = solve(4, files)
        self.assertEqual(sol.files, ("x2",))
        self.assertEqual(sol.total_value, 7)

    def test_mutex_with_dependency(self):
        # 高价值版本连带昂贵依赖后超出容量，只能选低价值版本。
        files = [("x1", 1, 5, [], "v"), ("x2", 1, 9, ["dep"], "v"),
                 ("dep", 4, 0, [], None)]
        _, sol = solve(4, files)
        self.assertEqual(sol.files, ("x1",))

    def test_distinct_groups_coexist(self):
        files = [("x", 1, 3, [], "g1"), ("y", 1, 4, [], "g2")]
        _, sol = solve(2, files)
        self.assertEqual(sol.files, ("x", "y"))


class TieBreakTest(unittest.TestCase):

    def test_smaller_size_wins_tie(self):
        # 价值同为 5，选总大小更小的。
        files = [("p", 3, 5, [], None), ("q", 1, 5, [], None)]
        _, sol = solve(3, files)
        self.assertEqual(sol.files, ("q",))
        self.assertEqual(sol.total_size, 1)

    def test_lexicographic_wins_after_size_tie(self):
        # 价值、总大小均相同，选排序后 ID 元组字典序更小者。
        files = [("b", 1, 5, [], None), ("a", 1, 5, [], None)]
        _, sol = solve(1, files)
        self.assertEqual(sol.files, ("a",))

    def test_empty_beats_zero_value_nonempty(self):
        files = [("z", 1, 0, [], None)]
        _, sol = solve(1, files)
        self.assertEqual(sol.files, ())


class BudgetTest(unittest.TestCase):

    FILES = [("f%d" % i, 1, i % 3, [], None) for i in range(12)]

    def test_budget_exhausted_reports_best_known(self):
        _, sol = solve(6, self.FILES, budget=5)
        self.assertFalse(sol.proven_optimal)
        self.assertLessEqual(sol.nodes_explored, 5)

    def test_generous_budget_proves_optimal(self):
        _, sol = solve(6, self.FILES, budget=10**9)
        self.assertTrue(sol.proven_optimal)
        # 容量 6：四个价值 2 的全拿，再补两个价值 1 的。
        self.assertEqual(sol.total_value, 2 * 4 + 1 * 2)

    def test_unlimited_matches_generous_budget(self):
        _, s1 = solve(6, self.FILES)
        _, s2 = solve(6, self.FILES, budget=10**9)
        self.assertEqual(s1.files, s2.files)
        self.assertTrue(s1.proven_optimal)


class VerifySelectionTest(unittest.TestCase):

    def setUp(self):
        self.inst = make_instance(5, [
            ("a", 2, 5, ["b"], None), ("b", 3, 0, [], None),
            ("c", 1, 1, [], "g"), ("d", 1, 1, [], "g"),
        ])

    def test_feasible_selection_passes(self):
        self.assertEqual(
            verify_selection(self.inst.files, 5, ["a", "b"]), [])

    def test_missing_dependency_detected(self):
        problems = verify_selection(self.inst.files, 5, ["a"])
        self.assertTrue(any("依赖闭包不完整" in p for p in problems))

    def test_over_capacity_detected(self):
        problems = verify_selection(self.inst.files, 4, ["a", "b"])
        self.assertTrue(any("超出容量" in p for p in problems))

    def test_mutex_violation_detected(self):
        problems = verify_selection(self.inst.files, 5, ["c", "d"])
        self.assertTrue(any("互斥组冲突" in p for p in problems))

    def test_unknown_file_detected(self):
        problems = verify_selection(self.inst.files, 5, ["ghost"])
        self.assertTrue(any("未知文件" in p for p in problems))


class LoadInstanceTest(unittest.TestCase):

    def test_roundtrip_json(self):
        obj = {"capacity": 5, "files": [
            {"id": "a", "size": 2, "value": 5, "deps": ["b"]},
            {"id": "b", "size": 3, "value": 0},
        ]}
        inst = load_instance(json.loads(json.dumps(obj)))
        sol = Solver(inst).solve()
        self.assertEqual(sol.files, ("a", "b"))

    def test_unknown_dependency_rejected(self):
        with self.assertRaises(ValueError):
            load_instance({"capacity": 1, "files": [
                {"id": "a", "size": 1, "value": 1, "deps": ["nope"]}]})

    def test_duplicate_id_rejected(self):
        with self.assertRaises(ValueError):
            load_instance({"capacity": 1, "files": [
                {"id": "a", "size": 1, "value": 1},
                {"id": "a", "size": 1, "value": 2}]})

    def test_negative_size_rejected(self):
        with self.assertRaises(ValueError):
            load_instance({"capacity": 1, "files": [
                {"id": "a", "size": -1, "value": 1}]})


class ReportTest(unittest.TestCase):

    def test_report_structure_and_topology(self):
        obj = {"capacity": 5, "files": [
            {"id": "a", "size": 2, "value": 5, "deps": ["b"]},
            {"id": "b", "size": 3, "value": 0},
        ]}
        inst = load_instance(obj)
        report = build_report(inst, Solver(inst).solve())
        self.assertEqual(report["status"], "optimal")
        self.assertTrue(report["proven_optimal"])
        self.assertEqual(report["files"], ["a", "b"])
        self.assertEqual(report["dependency_topology"], {"a": ["b"], "b": []})
        self.assertEqual(report["total_size"], 5)
        self.assertTrue(report["verification"]["feasible"])
        json.dumps(report)  # 报告必须可 JSON 序列化


if __name__ == "__main__":
    unittest.main()
