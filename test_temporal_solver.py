#!/usr/bin/env python3.11
"""temporal_solver 的 unittest 测试（全部使用合成数据）。"""
import json
import os
import subprocess
import sys
import tempfile
import unittest

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from temporal_solver import ConstraintSystem, run_spec  # noqa: E402

HERE = os.path.dirname(os.path.abspath(__file__))
SOLVER = os.path.join(HERE, "temporal_solver.py")


def assert_assignment_valid(testcase, system, assignment):
    """校验赋值同时满足全部区间与已提交差分约束。"""
    testcase.assertIsNotNone(assignment)
    for name, (lo, hi) in system.intervals.items():
        testcase.assertIn(name, assignment)
        testcase.assertLessEqual(lo, assignment[name])
        testcase.assertLessEqual(assignment[name], hi)
    for upstream, downstream, delay in system.constraints:
        testcase.assertGreaterEqual(
            assignment[downstream] - assignment[upstream], delay)


class AcceptanceTest(unittest.TestCase):
    """验收边界：A∈[0,2], B∈[1,3], B>=A+2 可行；再加 A>=B+1 构成正时差环。"""

    def setUp(self):
        self.system = ConstraintSystem()
        self.system.add_event("A", 0, 2)
        self.system.add_event("B", 1, 3)

    def test_feasible_base_case(self):
        ok, assignment = self.system.add_constraint("A", "B", 2)
        self.assertTrue(ok)
        assert_assignment_valid(self, self.system, assignment)
        # 验证确实需要拉满：B - A >= 2 且 B <= 3, A >= 0
        self.assertGreaterEqual(assignment["B"] - assignment["A"], 2)

    def test_positive_cycle_infeasible_and_rollback(self):
        ok, _ = self.system.add_constraint("A", "B", 2)
        self.assertTrue(ok)
        intervals_before = dict(self.system.intervals)
        constraints_before = list(self.system.constraints)

        ok, conflict = self.system.add_constraint("B", "A", 1)
        self.assertFalse(ok)
        # 冲突证据：A >= B + 1 且 B >= A + 2 => A >= A + 3
        self.assertEqual(conflict["min_self_delay"], 3)
        cycle_text = " ; ".join(conflict["cycle"])
        self.assertIn("A >= B + 1", cycle_text)
        self.assertIn("B >= A + 2", cycle_text)

        # 回滚后：约束集合与区间均保持添加前状态，系统仍可行
        self.assertEqual(self.system.constraints, constraints_before)
        self.assertEqual(self.system.intervals, intervals_before)
        assignment, conflict2 = self.system.solve()
        self.assertIsNone(conflict2)
        assert_assignment_valid(self, self.system, assignment)


class SolverTest(unittest.TestCase):
    def test_no_constraints_assignment_within_intervals(self):
        s = ConstraintSystem()
        s.add_event("X", 5, 9)
        s.add_event("Y", -3, 0)
        assignment, conflict = s.solve()
        self.assertIsNone(conflict)
        assert_assignment_valid(self, s, assignment)

    def test_chain_of_three_events(self):
        s = ConstraintSystem()
        s.add_event("E1", 0, 10)
        s.add_event("E2", 0, 10)
        s.add_event("E3", 0, 10)
        self.assertTrue(s.add_constraint("E1", "E2", 3)[0])
        self.assertTrue(s.add_constraint("E2", "E3", 4)[0])
        assignment, conflict = s.solve()
        self.assertIsNone(conflict)
        assert_assignment_valid(self, s, assignment)
        self.assertGreaterEqual(assignment["E3"] - assignment["E1"], 7)

    def test_bound_conflict_infeasible(self):
        # A∈[0,1], B∈[3,4]，要求 A >= B + 0：与区间上界冲突
        s = ConstraintSystem()
        s.add_event("A", 0, 1)
        s.add_event("B", 3, 4)
        ok, conflict = s.add_constraint("B", "A", 0)
        self.assertFalse(ok)
        self.assertGreater(conflict["min_self_delay"], 0)
        self.assertTrue(conflict["cycle"])
        # 回滚后可行
        assignment, _ = s.solve()
        assert_assignment_valid(self, s, assignment)

    def test_zero_delay_cycle_is_feasible(self):
        # 互相 >= +0 不构成正时差环，应可行
        s = ConstraintSystem()
        s.add_event("P", 0, 5)
        s.add_event("Q", 0, 5)
        self.assertTrue(s.add_constraint("P", "Q", 0)[0])
        self.assertTrue(s.add_constraint("Q", "P", 0)[0])

    def test_invalid_interval_rejected(self):
        s = ConstraintSystem()
        with self.assertRaises(ValueError):
            s.add_event("Bad", 5, 1)

    def test_unknown_event_rejected(self):
        s = ConstraintSystem()
        s.add_event("A", 0, 1)
        with self.assertRaises(KeyError):
            s.add_constraint("A", "Ghost", 1)

    def test_multiple_failures_each_rolled_back(self):
        s = ConstraintSystem()
        s.add_event("A", 0, 2)
        s.add_event("B", 1, 3)
        s.add_event("C", 0, 9)
        self.assertTrue(s.add_constraint("A", "B", 2)[0])
        self.assertFalse(s.add_constraint("B", "A", 1)[0])  # 回滚
        self.assertTrue(s.add_constraint("B", "C", 1)[0])   # 仍可继续添加
        self.assertFalse(s.add_constraint("C", "A", 5)[0])  # 回滚
        self.assertEqual(len(s.constraints), 2)
        assignment, conflict = s.solve()
        self.assertIsNone(conflict)
        assert_assignment_valid(self, s, assignment)


class CliTest(unittest.TestCase):
    def run_cli(self, spec):
        with tempfile.NamedTemporaryFile(
                "w", suffix=".json", delete=False, encoding="utf-8") as f:
            json.dump(spec, f)
            path = f.name
        try:
            proc = subprocess.run(
                [sys.executable, SOLVER, path],
                capture_output=True, text=True, timeout=30)
            return proc.returncode, json.loads(proc.stdout)
        finally:
            os.unlink(path)

    def test_cli_feasible(self):
        code, out = self.run_cli({
            "events": {"A": [0, 2], "B": [1, 3]},
            "constraints": [{"upstream": "A", "downstream": "B", "delay": 2}],
        })
        self.assertEqual(code, 0)
        self.assertTrue(out["feasible"])
        a, b = out["assignment"]["A"], out["assignment"]["B"]
        self.assertTrue(0 <= a <= 2 and 1 <= b <= 3 and b - a >= 2)

    def test_cli_infeasible_exit_1_with_conflict(self):
        code, out = self.run_cli({
            "events": {"A": [0, 2], "B": [1, 3]},
            "constraints": [
                {"upstream": "A", "downstream": "B", "delay": 2},
                {"upstream": "B", "downstream": "A", "delay": 1},
            ],
        })
        self.assertEqual(code, 1)
        self.assertFalse(out["feasible"])
        self.assertEqual(out["conflict"]["min_self_delay"], 3)
        statuses = [step["status"] for step in out["steps"]]
        self.assertEqual(statuses, ["accepted", "rolled_back"])
        # 回滚后仍给出已提交约束下的合法赋值
        a, b = out["assignment"]["A"], out["assignment"]["B"]
        self.assertTrue(0 <= a <= 2 and 1 <= b <= 3 and b - a >= 2)

    def test_cli_stdin(self):
        proc = subprocess.run(
            [sys.executable, SOLVER],
            input=json.dumps({"events": {"E": [2, 4]}, "constraints": []}),
            capture_output=True, text=True, timeout=30)
        self.assertEqual(proc.returncode, 0)
        out = json.loads(proc.stdout)
        self.assertTrue(out["feasible"])
        self.assertTrue(2 <= out["assignment"]["E"] <= 4)


if __name__ == "__main__":
    unittest.main(verbosity=2)
