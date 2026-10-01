#!/usr/bin/env python3
"""temporal_constraints 的单元测试。所有数据均为合成数据。"""

import json
import subprocess
import sys
import unittest
from pathlib import Path

from temporal_constraints import (
    DelayConstraint,
    TemporalConstraintSystem,
    run_instance,
)

ROOT = Path(__file__).resolve().parent


def check(assignment, intervals, constraints):
    for name, (lo, hi) in intervals.items():
        assert lo <= assignment[name] <= hi, f"{name} 越界"
    for c in constraints:
        got = assignment[c.downstream] - assignment[c.upstream]
        assert got >= c.min_delay, f"{c.describe()} 不满足，实际差 {got}"


class AcceptanceBoundaryTest(unittest.TestCase):
    """验收边界：A∈[0,2]，B∈[1,3]，B 至少晚 A 2 → 可行；
    再要求 A 至少晚 B 1 → 正时差环，不可行。"""

    def setUp(self):
        self.intervals = {"A": (0, 2), "B": (1, 3)}

    def test_single_constraint_feasible(self):
        system = TemporalConstraintSystem(self.intervals)
        result = system.add_constraint(DelayConstraint("A", "B", 2))
        self.assertTrue(result.ok)
        assignment, evidence = system.solve()
        self.assertIsNone(evidence)
        self.assertIsNotNone(assignment)
        check(assignment, self.intervals, system.constraints)

    def test_positive_delay_cycle_infeasible(self):
        system = TemporalConstraintSystem(self.intervals)
        self.assertTrue(system.add_constraint(DelayConstraint("A", "B", 2)).ok)
        result = system.add_constraint(DelayConstraint("B", "A", 1))
        self.assertFalse(result.ok)
        evidence = result.evidence
        self.assertIsNotNone(evidence)
        # 环：B >= A+2 且 A >= B+1 → 0 >= 3
        self.assertEqual(evidence.required_self_delay, 3)
        self.assertEqual(len(evidence.chain), 2)
        self.assertTrue(any("B - A >= 2" in s for s in evidence.chain))
        self.assertTrue(any("A - B >= 1" in s for s in evidence.chain))

    def test_rollback_restores_feasibility(self):
        system = TemporalConstraintSystem(self.intervals)
        system.add_constraint(DelayConstraint("A", "B", 2))
        system.add_constraint(DelayConstraint("B", "A", 1))  # 失败并回滚
        # 回滚后只剩第一条约束，系统可行
        self.assertEqual(len(system.constraints), 1)
        assignment, evidence = system.solve()
        self.assertIsNone(evidence)
        check(assignment, self.intervals, system.constraints)

    def test_intervals_never_modified(self):
        system = TemporalConstraintSystem(self.intervals)
        system.add_constraint(DelayConstraint("A", "B", 2))
        system.add_constraint(DelayConstraint("B", "A", 1))  # 回滚
        self.assertEqual(system.intervals, self.intervals)


class SolverTest(unittest.TestCase):
    def test_zero_delay_cycle_feasible(self):
        # A >= B+0 且 B >= A+0 → 0 >= 0，可行（非正时差环）
        system = TemporalConstraintSystem({"A": (0, 5), "B": (0, 5)})
        self.assertTrue(system.add_constraint(DelayConstraint("A", "B", 0)).ok)
        self.assertTrue(system.add_constraint(DelayConstraint("B", "A", 0)).ok)
        assignment, evidence = system.solve()
        self.assertIsNone(evidence)
        check(assignment, system.intervals, system.constraints)

    def test_interval_conflict_with_delay(self):
        # A∈[0,1]，B∈[5,6]，B >= A+2 可行；B >= A+10 不可行
        system = TemporalConstraintSystem({"A": (0, 1), "B": (5, 6)})
        self.assertTrue(system.add_constraint(DelayConstraint("A", "B", 2)).ok)
        result = system.add_constraint(DelayConstraint("A", "B", 10))
        self.assertFalse(result.ok)
        # 证据应包含区间上/下界约束（环经过源点）
        joined = " ".join(result.evidence.chain)
        self.assertIn("区间", joined)

    def test_chain_assignment(self):
        system = TemporalConstraintSystem(
            {"E1": (0, 10), "E2": (0, 10), "E3": (0, 10)}
        )
        system.add_constraint(DelayConstraint("E1", "E2", 3))
        system.add_constraint(DelayConstraint("E2", "E3", 4))
        assignment, evidence = system.solve()
        self.assertIsNone(evidence)
        check(assignment, system.intervals, system.constraints)

    def test_invalid_interval_rejected(self):
        with self.assertRaises(ValueError):
            TemporalConstraintSystem({"A": (3, 1)})

    def test_unknown_event_rejected(self):
        system = TemporalConstraintSystem({"A": (0, 1)})
        with self.assertRaises(ValueError):
            system.add_constraint(DelayConstraint("A", "ZZZ", 1))

    def test_negative_intervals(self):
        system = TemporalConstraintSystem({"A": (-5, -1), "B": (-4, 0)})
        self.assertTrue(system.add_constraint(DelayConstraint("A", "B", 2)).ok)
        assignment, evidence = system.solve()
        self.assertIsNone(evidence)
        check(assignment, system.intervals, system.constraints)


class CliTest(unittest.TestCase):
    def run_cli(self, *args, stdin_text=None):
        return subprocess.run(
            [sys.executable, str(ROOT / "temporal_constraints.py"), *args],
            input=stdin_text,
            capture_output=True,
            text=True,
        )

    def test_cli_feasible_example(self):
        proc = self.run_cli(str(ROOT / "examples" / "feasible.json"))
        self.assertEqual(proc.returncode, 0, proc.stderr)
        report = json.loads(proc.stdout)
        self.assertTrue(report["feasible"])
        self.assertTrue(report["assignment_verified"])
        check(
            report["assignment"],
            {k: tuple(v) for k, v in report["intervals"].items()},
            [DelayConstraint(**c) for c in report["active_constraints"]],
        )

    def test_cli_infeasible_example_rollback(self):
        proc = self.run_cli(str(ROOT / "examples" / "infeasible_cycle.json"))
        # 失败的约束被回滚，最终系统仍可行，退出码 0
        self.assertEqual(proc.returncode, 0, proc.stderr)
        report = json.loads(proc.stdout)
        self.assertTrue(report["feasible"])
        self.assertTrue(report["steps"][0]["accepted"])
        self.assertFalse(report["steps"][1]["accepted"])
        self.assertTrue(report["steps"][1]["rolled_back"])
        evidence = report["steps"][1]["evidence"]
        self.assertEqual(evidence["type"], "negative_cycle")
        self.assertIn("0 >= 3", evidence["contradiction"])
        # 回滚后只剩第一条约束
        self.assertEqual(len(report["active_constraints"]), 1)

    def test_cli_stdin(self):
        payload = json.dumps(
            {"events": {"A": [0, 2], "B": [1, 3]},
             "constraints": [{"upstream": "A", "downstream": "B", "min_delay": 2}]}
        )
        proc = self.run_cli(stdin_text=payload)
        self.assertEqual(proc.returncode, 0, proc.stderr)
        self.assertTrue(json.loads(proc.stdout)["feasible"])

    def test_cli_bad_input_exit_2(self):
        proc = self.run_cli(stdin_text='{"events": {"A": [5, 1]}}')
        self.assertEqual(proc.returncode, 2)
        self.assertIn("error", proc.stderr)

    def test_cli_initial_infeasible_exit_1(self):
        # 区间本身合法但初始即矛盾的场景：通过空约束不会触发，
        # 构造 lo>hi 会报输入错误；这里验证不可行最终态的退出码路径。
        # 用 run_instance 直接验证 evidence 分支即可（初始系统恒可行，
        # 因为区间合法时空约束系统必可行），故仅检查错误输入路径。
        proc = self.run_cli(stdin_text="not json")
        self.assertEqual(proc.returncode, 2)


class RunInstanceTest(unittest.TestCase):
    def test_report_structure(self):
        report = run_instance(
            {
                "events": {"A": [0, 2], "B": [1, 3]},
                "constraints": [
                    {"upstream": "A", "downstream": "B", "min_delay": 2},
                    {"upstream": "B", "downstream": "A", "min_delay": 1},
                ],
            }
        )
        self.assertTrue(report["feasible"])
        self.assertEqual(
            [s["accepted"] for s in report["steps"]], [True, False]
        )
        check(
            report["assignment"],
            {k: tuple(v) for k, v in report["intervals"].items()},
            [DelayConstraint(**c) for c in report["active_constraints"]],
        )


if __name__ == "__main__":
    unittest.main(verbosity=2)
