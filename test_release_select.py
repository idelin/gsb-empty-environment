#!/usr/bin/env python3.11
"""release_select 的 unittest 测试. 所有数据均为合成数据."""
import json
import subprocess
import sys
import unittest
from pathlib import Path

import release_select as rs

ROOT = Path(__file__).resolve().parent


def solve(instance, **kwargs):
    return rs.build_report(instance, **kwargs)


def make_change(cid, benefit, cost, deps=(), mutex=()):
    return rs.Change(cid, benefit, cost, tuple(deps), tuple(mutex))


class AcceptanceBoundaryTest(unittest.TestCase):
    """验收边界: A 收益5 依赖 B, B 收益-2, 容量允许时整体收益 3; 不得单选 A 报 5."""

    def setUp(self):
        self.instance = {
            "capacity": 10,
            "changes": [
                {"id": "A", "benefit": 5, "cost": 2, "deps": ["B"]},
                {"id": "B", "benefit": -2, "cost": 2},
            ],
        }

    def test_selects_whole_closure_with_net_benefit_3(self):
        report = solve(self.instance)
        self.assertEqual(report["selection"]["ids"], ["A", "B"])
        self.assertEqual(report["selection"]["benefit"], 3)
        self.assertEqual(report["selection"]["cost"], 4)
        self.assertTrue(report["evidence"]["all_constraints_satisfied"])

    def test_cannot_select_a_alone(self):
        changes, capacity = rs.parse_instance(self.instance)
        units, unit_of, _ = rs.build_units(changes)
        closures = rs.compute_closures(units)
        alone = frozenset({unit_of["A"]})
        self.assertIsNone(rs.evaluate_selection(units, closures, alone, capacity))
        report = solve(self.instance)
        self.assertNotEqual(report["selection"]["ids"], ["A"])
        self.assertNotEqual(report["selection"]["benefit"], 5)

    def test_capacity_blocking_dependency_forces_drop(self):
        instance = json.loads(json.dumps(self.instance))
        instance["capacity"] = 3  # A+B 需要 4, 装不下
        report = solve(instance)
        self.assertEqual(report["selection"]["ids"], [])
        self.assertEqual(report["selection"]["benefit"], 0)


class ConstraintTest(unittest.TestCase):

    def test_mutex_excludes_coselection(self):
        report = solve({
            "capacity": 100,
            "changes": [
                {"id": "A", "benefit": 5, "cost": 1, "mutex": ["B"]},
                {"id": "B", "benefit": 4, "cost": 1},
                {"id": "C", "benefit": 1, "cost": 1},
            ],
        })
        self.assertEqual(report["selection"]["ids"], ["A", "C"])
        self.assertEqual(report["selection"]["benefit"], 6)
        mutex_ev = report["evidence"]["mutex"]
        self.assertEqual(len(mutex_ev), 1)
        self.assertTrue(mutex_ev[0]["satisfied"])
        self.assertFalse(mutex_ev[0]["both_selected"])

    def test_dependency_cycle_taken_as_one_group(self):
        report = solve({
            "capacity": 10,
            "changes": [
                {"id": "A", "benefit": 5, "cost": 1, "deps": ["B"]},
                {"id": "B", "benefit": -2, "cost": 1, "deps": ["A"]},
            ],
        })
        self.assertEqual(report["selection"]["ids"], ["A", "B"])
        self.assertEqual(report["selection"]["benefit"], 3)
        forced = [g for g in report["groups"] if g["forced_together"]]
        self.assertEqual(len(forced), 1)
        self.assertEqual(forced[0]["members"], ["A", "B"])
        self.assertTrue(forced[0]["selected"])

    def test_cycle_with_negative_total_is_dropped(self):
        report = solve({
            "capacity": 10,
            "changes": [
                {"id": "A", "benefit": 1, "cost": 1, "deps": ["B"]},
                {"id": "B", "benefit": -5, "cost": 1, "deps": ["A"]},
            ],
        })
        self.assertEqual(report["selection"]["ids"], [])

    def test_transitive_dependency_closure(self):
        report = solve({
            "capacity": 10,
            "changes": [
                {"id": "A", "benefit": 5, "cost": 1, "deps": ["B"]},
                {"id": "B", "benefit": 1, "cost": 1, "deps": ["C"]},
                {"id": "C", "benefit": -1, "cost": 1},
            ],
        })
        self.assertEqual(report["selection"]["ids"], ["A", "B", "C"])
        self.assertEqual(report["selection"]["benefit"], 5)
        deps_ev = report["evidence"]["dependencies"]
        self.assertEqual(len(deps_ev), 2)
        self.assertTrue(all(e["satisfied"] for e in deps_ev))

    def test_internal_mutex_conflict_in_cycle_is_unselectable(self):
        report = solve({
            "capacity": 10,
            "changes": [
                {"id": "A", "benefit": 9, "cost": 1, "deps": ["B"], "mutex": ["B"]},
                {"id": "B", "benefit": 9, "cost": 1, "deps": ["A"]},
            ],
        })
        self.assertEqual(report["selection"]["ids"], [])
        self.assertTrue(report["groups"][0]["internal_mutex_conflict"])


class TieBreakTest(unittest.TestCase):

    def test_tie_prefers_fewer_changes(self):
        report = solve({
            "capacity": 2,
            "changes": [
                {"id": "X", "benefit": 5, "cost": 2},
                {"id": "Y", "benefit": 3, "cost": 1},
                {"id": "Z", "benefit": 2, "cost": 1},
            ],
        })
        self.assertEqual(report["selection"]["benefit"], 5)
        self.assertEqual(report["selection"]["ids"], ["X"])

    def test_tie_prefers_lexicographically_smaller_ids(self):
        report = solve({
            "capacity": 1,
            "changes": [
                {"id": "B", "benefit": 5, "cost": 1},
                {"id": "A", "benefit": 5, "cost": 1},
            ],
        })
        self.assertEqual(report["selection"]["ids"], ["A"])


class VerifierTest(unittest.TestCase):

    INSTANCES = [
        {"capacity": 10, "changes": [
            {"id": "A", "benefit": 5, "cost": 2, "deps": ["B"]},
            {"id": "B", "benefit": -2, "cost": 2}]},
        {"capacity": 4, "changes": [
            {"id": "A", "benefit": 5, "cost": 2, "deps": ["B"], "mutex": ["C"]},
            {"id": "B", "benefit": -2, "cost": 2},
            {"id": "C", "benefit": 4, "cost": 3},
            {"id": "D", "benefit": 1, "cost": 1, "deps": ["C"]}]},
        {"capacity": 6, "changes": [
            {"id": "A", "benefit": 3, "cost": 1, "deps": ["B"]},
            {"id": "B", "benefit": 2, "cost": 1, "deps": ["A"]},
            {"id": "C", "benefit": 4, "cost": 2, "mutex": ["D"]},
            {"id": "D", "benefit": 5, "cost": 3},
            {"id": "E", "benefit": -1, "cost": 1}]},
        {"capacity": 0, "changes": [
            {"id": "A", "benefit": 7, "cost": 1}]},
    ]

    def test_verifier_confirms_solver_on_all_instances(self):
        for instance in self.INSTANCES:
            with self.subTest(instance=instance):
                report = solve(instance)
                verification = report["verification"]
                self.assertTrue(verification["verified"])
                self.assertTrue(verification["matches_solver"])
                self.assertTrue(verification["optimality_proven"])
                self.assertEqual(verification["enumerated_sets"],
                                 1 << verification["units"])

    def test_verifier_reports_unverified_beyond_limit(self):
        instance = {"capacity": 10, "changes": [
            {"id": "A", "benefit": 5, "cost": 1, "deps": ["B"]},
            {"id": "B", "benefit": -2, "cost": 1}]}
        report = solve(instance, verify_limit=1)
        verification = report["verification"]
        self.assertFalse(verification["verified"])
        self.assertIn("未验证", verification["reason"])

    def test_no_verify_flag_reports_unverified(self):
        report = solve(self.INSTANCES[0], verify=False)
        self.assertFalse(report["verification"]["verified"])


class ValidationTest(unittest.TestCase):

    def test_unknown_reference_rejected(self):
        with self.assertRaises(ValueError):
            rs.parse_instance({"capacity": 1, "changes": [
                {"id": "A", "benefit": 1, "cost": 1, "deps": ["ZZZ"]}]})

    def test_duplicate_id_rejected(self):
        with self.assertRaises(ValueError):
            rs.parse_instance({"capacity": 1, "changes": [
                {"id": "A", "benefit": 1, "cost": 1},
                {"id": "A", "benefit": 2, "cost": 1}]})

    def test_negative_cost_rejected(self):
        with self.assertRaises(ValueError):
            rs.parse_instance({"capacity": 1, "changes": [
                {"id": "A", "benefit": 1, "cost": -1}]})


class CliTest(unittest.TestCase):

    def test_cli_on_boundary_example(self):
        proc = subprocess.run(
            [sys.executable, str(ROOT / "release_select.py"),
             str(ROOT / "examples" / "boundary.json")],
            capture_output=True, text=True, cwd=ROOT)
        self.assertEqual(proc.returncode, 0, proc.stderr)
        report = json.loads(proc.stdout)
        self.assertEqual(report["selection"]["ids"], ["A", "B"])
        self.assertEqual(report["selection"]["benefit"], 3)
        self.assertTrue(report["verification"]["verified"])
        self.assertTrue(report["verification"]["optimality_proven"])

    def test_cli_rejects_invalid_input(self):
        proc = subprocess.run(
            [sys.executable, str(ROOT / "release_select.py"), "-"],
            input='{"capacity": 1, "changes": [{"id": "A", "benefit": 1, "cost": 1, "deps": ["ZZZ"]}]}',
            capture_output=True, text=True, cwd=ROOT)
        self.assertEqual(proc.returncode, 2)
        self.assertIn("ZZZ", proc.stderr)


if __name__ == "__main__":
    unittest.main()
