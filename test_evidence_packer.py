"""evidence_packer 的 unittest 测试。所有数据均为合成数据。"""

import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

import evidence_packer as ep


def item(iid, size, value, deps=(), group=None):
    return ep.Item(iid, size, value, tuple(deps), group)


class AcceptanceBoundaryTests(unittest.TestCase):
    """题目验收边界：a(2,5)->b(3,0)。"""

    def setUp(self):
        self.items = [item("a", 2, 5, deps=("b",)), item("b", 3, 0)]

    def test_capacity_4_cannot_select_a(self):
        report = ep.build_report(self.items, 4)
        self.assertTrue(report["proven_optimal"])
        self.assertNotIn("a", report["selection"]["ids"])
        # 空集（值0、大小0）以更小总大小赢得对 {b}（值0、大小3）的平局
        self.assertEqual(report["selection"]["ids"], [])
        self.assertEqual(report["selection"]["total_value"], 0)

    def test_capacity_5_selects_a_and_b_size_5(self):
        report = ep.build_report(self.items, 5)
        self.assertTrue(report["proven_optimal"])
        self.assertEqual(report["selection"]["ids"], ["a", "b"])
        # 不得遗漏 b 而误报大小 2
        self.assertEqual(report["selection"]["total_size"], 5)
        self.assertEqual(report["selection"]["total_value"], 5)
        self.assertTrue(report["feasibility_recheck"]["feasible"])
        # 拓扑：依赖 b 先于依赖者 a
        self.assertEqual(report["dependency_topology"]["topological_order"], ["b", "a"])


class DependencyRuleTests(unittest.TestCase):

    def test_shared_dependency_counted_once(self):
        # a、c 都依赖 b；b 的大小只计一次：2+3+1=6 恰好装满
        items = [item("a", 2, 5, deps=("b",)), item("b", 3, 0),
                 item("c", 1, 1, deps=("b",))]
        report = ep.build_report(items, 6)
        self.assertEqual(report["selection"]["ids"], ["a", "b", "c"])
        self.assertEqual(report["selection"]["total_size"], 6)
        self.assertEqual(report["selection"]["total_value"], 6)

    def test_transitive_closure(self):
        # a->b->c；容量仅够 a 自身时不可选 a
        items = [item("a", 1, 10, deps=("b",)), item("b", 1, 0, deps=("c",)),
                 item("c", 1, 0)]
        self.assertNotIn("a", ep.build_report(items, 1)["selection"]["ids"])
        report = ep.build_report(items, 3)
        self.assertEqual(report["selection"]["ids"], ["a", "b", "c"])
        self.assertEqual(report["dependency_topology"]["topological_order"],
                         ["c", "b", "a"])

    def test_dependency_cycle_is_feasible_and_reported(self):
        items = [item("a", 1, 2, deps=("b",)), item("b", 1, 3, deps=("a",))]
        report = ep.build_report(items, 2)
        self.assertEqual(report["selection"]["ids"], ["a", "b"])
        self.assertFalse(report["dependency_topology"]["acyclic"])


class MutexGroupTests(unittest.TestCase):

    def test_mutex_group_at_most_one(self):
        items = [item("x1", 2, 4, group="x"), item("x2", 2, 5, group="x"),
                 item("y", 1, 1)]
        report = ep.build_report(items, 4)
        ids = report["selection"]["ids"]
        self.assertEqual(ids, ["x2", "y"])  # 同组取更优版本
        self.assertEqual(report["selection"]["total_value"], 6)

    def test_mutex_conflict_inside_dependency_closure(self):
        # a 依赖 b 与 c，而 b、c 同组互斥 => a 不可选
        items = [item("a", 1, 100, deps=("b", "c")),
                 item("b", 1, 0, group="g"), item("c", 1, 0, group="g")]
        report = ep.build_report(items, 3)
        self.assertNotIn("a", report["selection"]["ids"])
        self.assertTrue(report["proven_optimal"])


class TieBreakTests(unittest.TestCase):

    def test_tie_prefers_smaller_size(self):
        items = [item("big", 4, 5), item("small", 2, 5)]
        report = ep.build_report(items, 4)
        self.assertEqual(report["selection"]["ids"], ["small"])
        self.assertEqual(report["selection"]["total_size"], 2)

    def test_tie_prefers_lexicographic_ids(self):
        items = [item("b", 2, 5), item("a", 2, 5)]
        report = ep.build_report(items, 2)
        self.assertEqual(report["selection"]["ids"], ["a"])


class OptimalityStatusTests(unittest.TestCase):

    def test_budget_exhaustion_reports_best_known(self):
        items = [item(f"f{i:02d}", 1, i) for i in range(12)]
        report = ep.build_report(items, 6, max_nodes=3)
        self.assertFalse(report["proven_optimal"])
        self.assertEqual(report["status"], "best_known_not_proven_optimal")
        self.assertTrue(report["search_stats"]["budget_exhausted"])
        # 即便未证最优，候选解本身仍须可行
        self.assertTrue(report["feasibility_recheck"]["feasible"])
        self.assertLessEqual(report["selection"]["total_size"], 6)

    def test_full_search_proves_optimal(self):
        items = [item(f"f{i:02d}", 1, i) for i in range(12)]
        report = ep.build_report(items, 6)
        self.assertTrue(report["proven_optimal"])
        self.assertEqual(report["selection"]["total_value"], 11 + 10 + 9 + 8 + 7 + 6)


class FeasibilityRecheckTests(unittest.TestCase):

    def setUp(self):
        self.items = [item("a", 2, 5, deps=("b",)), item("b", 3, 0),
                      item("v1", 1, 1, group="v"), item("v2", 1, 1, group="v")]

    def test_valid_selection_passes(self):
        r = ep.verify_selection(self.items, 5, ["a", "b"])
        self.assertTrue(r["feasible"])
        self.assertEqual(r["recomputed_total_size"], 5)

    def test_missing_dependency_detected(self):
        r = ep.verify_selection(self.items, 5, ["a"])
        self.assertFalse(r["feasible"])
        self.assertFalse(r["checks"]["dependency_closure_complete"])

    def test_mutex_violation_detected(self):
        r = ep.verify_selection(self.items, 5, ["v1", "v2"])
        self.assertFalse(r["feasible"])
        self.assertFalse(r["checks"]["mutex_groups_respected"])

    def test_over_capacity_detected(self):
        r = ep.verify_selection(self.items, 4, ["a", "b"])
        self.assertFalse(r["feasible"])
        self.assertFalse(r["checks"]["within_capacity"])

    def test_unknown_id_detected(self):
        r = ep.verify_selection(self.items, 5, ["ghost"])
        self.assertFalse(r["feasible"])
        self.assertFalse(r["checks"]["all_ids_known"])


class InputValidationTests(unittest.TestCase):

    def parse(self, payload):
        return ep.parse_instance(payload)

    def test_duplicate_id_rejected(self):
        with self.assertRaises(ep.InputError):
            self.parse({"capacity": 1, "items": [
                {"id": "a", "size": 1, "value": 1},
                {"id": "a", "size": 1, "value": 2}]})

    def test_unknown_dependency_rejected(self):
        with self.assertRaises(ep.InputError):
            self.parse({"capacity": 1, "items": [
                {"id": "a", "size": 1, "value": 1, "dependencies": ["nope"]}]})

    def test_negative_size_rejected(self):
        with self.assertRaises(ep.InputError):
            self.parse({"capacity": 1, "items": [
                {"id": "a", "size": -1, "value": 1}]})

    def test_negative_capacity_rejected(self):
        with self.assertRaises(ep.InputError):
            self.parse({"capacity": -1, "items": []})


class CliTests(unittest.TestCase):

    def run_cli(self, payload, *extra):
        with tempfile.NamedTemporaryFile("w", suffix=".json", delete=False) as fh:
            json.dump(payload, fh)
            path = fh.name
        try:
            proc = subprocess.run(
                [sys.executable, "evidence_packer.py", path, *extra],
                capture_output=True, text=True,
                cwd=Path(__file__).parent)
            return proc
        finally:
            Path(path).unlink()

    def test_cli_acceptance_case(self):
        payload = {"capacity": 5, "items": [
            {"id": "a", "size": 2, "value": 5, "dependencies": ["b"]},
            {"id": "b", "size": 3, "value": 0}]}
        proc = self.run_cli(payload)
        self.assertEqual(proc.returncode, 0, proc.stderr)
        report = json.loads(proc.stdout)
        self.assertEqual(report["selection"]["ids"], ["a", "b"])
        self.assertEqual(report["selection"]["total_size"], 5)
        self.assertEqual(report["status"], "proven_optimal")

    def test_cli_max_nodes_flag(self):
        payload = {"capacity": 3, "items": [
            {"id": f"f{i}", "size": 1, "value": i} for i in range(10)]}
        proc = self.run_cli(payload, "--max-nodes", "2")
        report = json.loads(proc.stdout)
        self.assertEqual(report["status"], "best_known_not_proven_optimal")

    def test_cli_invalid_input_exit_code(self):
        proc = self.run_cli({"capacity": -1, "items": []})
        self.assertEqual(proc.returncode, 2)
        self.assertIn("输入错误", proc.stderr)


if __name__ == "__main__":
    unittest.main()
