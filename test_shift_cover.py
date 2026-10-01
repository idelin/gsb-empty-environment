#!/usr/bin/env python3
"""shift_cover 的 unittest 测试。所有数据均为合成数据。"""
import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

import shift_cover
from shift_cover import InputError, Solver, parse_instance

WEEK = shift_cover.WEEK_MINUTES


def solve(instance, **kw):
    return Solver(parse_instance(instance)).solve(**kw)


class AcceptanceTest(unittest.TestCase):
    def test_acceptance_boundary(self):
        """验收边界：需求  "2 人；单人班成本 3 与覆盖相同区间成本 5；
        应选两个成本 3 班，总成本 6。"""
        inst = {
            "demands": [{"start": 0, "end": 120, "required": 2}],
            "shifts": [
                {"id": "cheap", "start": 0, "duration": 120, "cost": 3},
                {"id": "pricey", "start": 0, "duration": 120, "cost": 5},
            ],
        }
        r = solve(inst)
        self.assertEqual(r["status"], "optimal")
        self.assertTrue(r["proven_optimal"])
        self.assertEqual(r["cost"], 6)
        self.assertEqual(r["total_count"], 2)
        self.assertEqual(r["assignment"], {"cheap": 2})
        self.assertEqual(r["cost_lower_bound"], 6)
        self.assertEqual(r["cost_upper_bound"], 6)


class OptimalityTest(unittest.TestCase):
    def test_tie_break_total_count(self):
        """成本相同取总人数更少：A×2(成本4,2人) 优于 B×2+C×2(成本4,4人)。"""
        inst = {
            "demands": [{"start": 0, "end": 60, "required": 2}],
            "shifts": [
                {"id": "A", "start": 0, "duration": 60, "cost": 2},
                {"id": "B", "start": 0, "duration": 30, "cost": 1},
                {"id": "C", "start": 30, "duration": 30, "cost": 1},
            ],
        }
        r = solve(inst)
        self.assertEqual(r["status"], "optimal")
        self.assertEqual(r["cost"], 4)
        self.assertEqual(r["total_count"], 2)
        self.assertEqual(r["assignment"], {"A": 2})

    def test_tie_break_lexicographic(self):
        """成本与总人数都相同，取按班次 ID 升序人数向量字典序最小：
        (0,1) < (1,0)，故选 B=1。"""
        inst = {
            "demands": [{"start": 0, "end": 60, "required": 1}],
            "shifts": [
                {"id": "A", "start": 0, "duration": 60, "cost": 2},
                {"id": "B", "start": 0, "duration": 60, "cost": 2},
            ],
        }
        r = solve(inst)
        self.assertEqual(r["status"], "optimal")
        self.assertEqual(r["assignment"], {"B": 1})
        self.assertEqual(r["count_vector"], [0, 1])

    def test_cross_midnight_shift(self):
        """跨日班次：1400 起 200 分钟覆盖到次日。"""
        inst = {
            "demands": [{"start": 1380, "end": 1500, "required": 1}],
            "shifts": [
                {"id": "early", "start": 1300, "duration": 100, "cost": 3},
                {"id": "late", "start": 1400, "duration": 200, "cost": 3},
            ],
        }
        r = solve(inst)
        self.assertEqual(r["status"], "optimal")
        self.assertEqual(r["cost"], 6)
        self.assertEqual(r["assignment"], {"early": 1, "late": 1})

    def test_wraparound_week(self):
        """跨周末：需求区间与班次都回绕周末边界。"""
        inst = {
            "demands": [{"start": WEEK - 10, "end": 20, "required": 1}],
            "shifts": [
                {"id": "sun_late", "start": WEEK - 20, "duration": 30, "cost": 4},
                {"id": "mon_early", "start": 0, "duration": 30, "cost": 4},
            ],
        }
        r = solve(inst)
        self.assertEqual(r["status"], "optimal")
        self.assertEqual(r["cost"], 8)
        self.assertEqual(r["assignment"], {"mon_early": 1, "sun_late": 1})

    def test_zero_demand(self):
        inst = {"demands": [], "shifts": [
            {"id": "A", "start": 0, "duration": 60, "cost": 1}]}
        r = solve(inst)
        self.assertEqual(r["status"], "optimal")
        self.assertEqual(r["cost"], 0)
        self.assertEqual(r["assignment"], {})

    def test_infeasible(self):
        inst = {
            "demands": [{"start": 500, "end": 600, "required": 1}],
            "shifts": [{"id": "A", "start": 0, "duration": 60, "cost": 1}],
        }
        r = solve(inst)
        self.assertEqual(r["status"], "infeasible")
        self.assertFalse(r["proven_optimal"])

    def test_fractional_cost(self):
        inst = {
            "demands": [{"start": 0, "end": 60, "required": 2}],
            "shifts": [{"id": "A", "start": 0, "duration": 60, "cost": 2.5}],
        }
        r = solve(inst)
        self.assertEqual(r["status"], "optimal")
        self.assertEqual(r["cost"], 5)
        self.assertEqual(r["total_count"], 2)


class BudgetTest(unittest.TestCase):
    """预算中断：必须给出可行上界与合法下界，且不得声称最优。"""

    def _big_instance(self):
        shifts = []
        for k in range(8):
            shifts.append({
                "id": f"S{k:02d}",
                "start": (k * 180) % WEEK,
                "duration": 240 + (k % 3) * 60,
                "cost": 5 + (k % 4),
            })
        demands = [{"start": k * 240, "end": k * 240 + 300, "required": 1 + k % 3}
                   for k in range(5)]
        return {"demands": demands, "shifts": shifts}

    def test_budget_interruption_bounds(self):
        inst = self._big_instance()
        limited = solve(inst, max_nodes=0, max_seconds=60.0)
        self.assertEqual(limited["status"], "feasible")
        self.assertFalse(limited["proven_optimal"])
        self.assertTrue(limited["budget_exhausted"])
        self.assertIsNotNone(limited["cost"])  # 可行上界存在
        self.assertIsNotNone(limited["cost_lower_bound"])
        self.assertLessEqual(limited["cost_lower_bound"], limited["cost"])

        full = solve(inst, max_nodes=100000, max_seconds=30.0)
        self.assertEqual(full["status"], "optimal")
        # 中断时的下界必须合法：不超过真实最优值
        self.assertLessEqual(limited["cost_lower_bound"], full["cost"])
        # 中断时的上界（可行解成本）不小于最优值
        self.assertLessEqual(full["cost"], limited["cost"])

    def test_budget_nodes_one(self):
        inst = self._big_instance()
        r = solve(inst, max_nodes=1, max_seconds=60.0)
        self.assertIn(r["status"], ("feasible", "optimal"))
        if r["status"] == "feasible":
            self.assertFalse(r["proven_optimal"])
            self.assertLessEqual(r["cost_lower_bound"], r["cost"])


class ValidationTest(unittest.TestCase):
    def test_duration_too_long(self):
        with self.assertRaises(InputError):
            parse_instance({"demands": [], "shifts": [
                {"id": "A", "start": 0, "duration": 1441, "cost": 1}]})

    def test_empty_interval(self):
        with self.assertRaises(InputError):
            parse_instance({"demands": [{"start": 10, "end": 10, "required": 1}],
                            "shifts": []})

    def test_duplicate_id(self):
        with self.assertRaises(InputError):
            parse_instance({"demands": [], "shifts": [
                {"id": "A", "start": 0, "duration": 60, "cost": 1},
                {"id": "A", "start": 0, "duration": 60, "cost": 2}]})

    def test_negative_cost(self):
        with self.assertRaises(InputError):
            parse_instance({"demands": [], "shifts": [
                {"id": "A", "start": 0, "duration": 60, "cost": -1}]})


class CliTest(unittest.TestCase):
    def test_cli_file_and_stdin(self):
        inst = {
            "demands": [{"start": 0, "end": 120, "required": 2}],
            "shifts": [
                {"id": "cheap", "start": 0, "duration": 120, "cost": 3},
                {"id": "pricey", "start": 0, "duration": 120, "cost": 5},
            ],
        }
        with tempfile.NamedTemporaryFile(
                "w", suffix=".json", delete=False) as f:
            json.dump(inst, f)
            path = f.name
        try:
            out = subprocess.run(
                [sys.executable, "shift_cover.py", path],
                capture_output=True, text=True, cwd=Path(__file__).parent)
            self.assertEqual(out.returncode, 0, out.stderr)
            r = json.loads(out.stdout)
            self.assertEqual(r["status"], "optimal")
            self.assertEqual(r["cost"], 6)

            out2 = subprocess.run(
                [sys.executable, "shift_cover.py", "--pretty"],
                input=json.dumps(inst), capture_output=True, text=True,
                cwd=Path(__file__).parent)
            self.assertEqual(out2.returncode, 0, out2.stderr)
            self.assertEqual(json.loads(out2.stdout)["cost"], 6)
        finally:
            Path(path).unlink()

    def test_cli_bad_input_exit_code(self):
        out = subprocess.run(
            [sys.executable, "shift_cover.py"],
            input='{"demands": [{"start": 1, "end": 1, "required": 1}]}',
            capture_output=True, text=True, cwd=Path(__file__).parent)
        self.assertEqual(out.returncode, 2)
        self.assertIn("error", out.stderr)


if __name__ == "__main__":
    unittest.main(verbosity=2)
