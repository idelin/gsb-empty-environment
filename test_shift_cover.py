"""shift_cover 的单元测试。所有数据均为合成数据。"""
import json
import subprocess
import sys
import unittest
from pathlib import Path

import shift_cover as sc

HERE = Path(__file__).resolve().parent


def solve(demands, shifts, **kw):
    parsed_demands, parsed_shifts = sc._parse_input(
        {"demands": demands, "shifts": shifts}
    )
    return sc.solve_instance(parsed_demands, parsed_shifts, **kw)


class AcceptanceTest(unittest.TestCase):
    def test_two_cheap_shifts_beat_one_expensive(self):
        # 验收边界：需求 2 人；单人班成本 3 与覆盖相同区间成本 5 的班。
        # 应选两个成本 3 的班，总成本 6。
        result = solve(
            demands=[{"start": 600, "end": 660, "required": 2}],
            shifts=[
                {"id": "cheap", "start": 600, "end": 660, "cost": 3},
                {"id": "pricey", "start": 600, "end": 660, "cost": 5},
            ],
        )
        self.assertEqual(result["status"], "optimal")
        self.assertTrue(result["proven_optimal"])
        self.assertEqual(result["total_cost"], 6)
        self.assertEqual(result["total_staff"], 2)
        self.assertEqual(result["assignment"], {"cheap": 2})
        self.assertEqual(result["lower_bound"], 6)
        self.assertEqual(result["upper_bound"], 6)


class OptimalityTest(unittest.TestCase):
    def test_wrap_around_shift_covering_week_boundary(self):
        # 需求跨周末（10080 分钟回绕），跨日班次覆盖它。
        result = solve(
            demands=[{"start": 10000, "end": 200, "required": 1}],
            shifts=[
                {"id": "night", "start": 9960, "end": 240, "cost": 4},
                {"id": "day", "start": 200, "end": 600, "cost": 1},
            ],
        )
        self.assertEqual(result["status"], "optimal")
        self.assertEqual(result["assignment"], {"night": 1})
        self.assertEqual(result["total_cost"], 4)

    def test_tie_break_prefers_fewer_staff(self):
        # X 一个班覆盖两段（1 人），Y+Z 各覆盖一段（2 人），成本同为 6。
        result = solve(
            demands=[
                {"start": 0, "end": 60, "required": 1},
                {"start": 60, "end": 120, "required": 1},
            ],
            shifts=[
                {"id": "X", "start": 0, "end": 120, "cost": 6},
                {"id": "Y", "start": 0, "end": 60, "cost": 3},
                {"id": "Z", "start": 60, "end": 120, "cost": 3},
            ],
        )
        self.assertEqual(result["status"], "optimal")
        self.assertEqual(result["total_cost"], 6)
        self.assertEqual(result["total_staff"], 1)
        self.assertEqual(result["assignment"], {"X": 1})

    def test_tie_break_lexicographic_by_shift_id(self):
        # 成本、人数都相同，按班次 ID 排序的人数向量取字典序最小：
        # 向量按 ID 序为 (count_A, count_B)，(0,1) < (1,0)，故应选 B。
        result = solve(
            demands=[{"start": 0, "end": 60, "required": 1}],
            shifts=[
                {"id": "B", "start": 0, "end": 60, "cost": 3},
                {"id": "A", "start": 0, "end": 60, "cost": 3},
            ],
        )
        self.assertEqual(result["status"], "optimal")
        self.assertEqual(result["assignment"], {"B": 1})

    def test_overlapping_demands_take_max(self):
        # 重叠区需求取最大值：中段需 2 人。
        result = solve(
            demands=[
                {"start": 0, "end": 120, "required": 1},
                {"start": 60, "end": 180, "required": 2},
            ],
            shifts=[{"id": "S", "start": 0, "end": 180, "cost": 2}],
        )
        self.assertEqual(result["status"], "optimal")
        self.assertEqual(result["total_cost"], 4)
        self.assertEqual(result["assignment"], {"S": 2})

    def test_zero_demand_is_trivially_optimal(self):
        result = solve(
            demands=[],
            shifts=[{"id": "S", "start": 0, "end": 60, "cost": 1}],
        )
        self.assertEqual(result["status"], "optimal")
        self.assertEqual(result["total_cost"], 0)
        self.assertEqual(result["assignment"], {})


class InfeasibleTest(unittest.TestCase):
    def test_uncovered_demand_segment(self):
        result = solve(
            demands=[{"start": 0, "end": 60, "required": 1}],
            shifts=[{"id": "S", "start": 60, "end": 120, "cost": 1}],
        )
        self.assertEqual(result["status"], "infeasible")
        self.assertFalse(result["proven_optimal"])
        self.assertIn("uncovered_segments", result)


class BudgetTest(unittest.TestCase):
    def test_budget_interruption_returns_valid_bounds(self):
        # 节点预算为 1：搜索必然中断，须给出可行上界与合法下界，
        # 且不得声称最优。该实例真实最优成本为 6。
        result = solve(
            demands=[{"start": 600, "end": 660, "required": 2}],
            shifts=[
                {"id": "cheap", "start": 600, "end": 660, "cost": 3},
                {"id": "pricey", "start": 600, "end": 660, "cost": 5},
            ],
            node_limit=1,
        )
        self.assertEqual(result["status"], "feasible")
        self.assertFalse(result["proven_optimal"])
        self.assertIsNotNone(result["assignment"])
        self.assertLessEqual(result["lower_bound"], 6)
        self.assertGreaterEqual(result["upper_bound"], 6)
        self.assertLessEqual(result["lower_bound"], result["upper_bound"])
        self.assertIn("未证明", result["message"])

    def test_time_limit_zero_interrupts(self):
        result = solve(
            demands=[{"start": 0, "end": 60, "required": 3}],
            shifts=[{"id": "S", "start": 0, "end": 60, "cost": 2}],
            time_limit=0.0,
        )
        self.assertIn(result["status"], ("feasible", "no_solution"))
        self.assertFalse(result["proven_optimal"])
        if result["status"] == "feasible":
            self.assertLessEqual(result["lower_bound"], result["upper_bound"])


class ValidationTest(unittest.TestCase):
    def test_shift_longer_than_one_day_rejected(self):
        with self.assertRaises(sc.InputError):
            sc._parse_input(
                {
                    "demands": [],
                    "shifts": [{"id": "L", "start": 0, "end": 1441, "cost": 1}],
                }
            )

    def test_duplicate_shift_id_rejected(self):
        with self.assertRaises(sc.InputError):
            sc._parse_input(
                {
                    "demands": [],
                    "shifts": [
                        {"id": "A", "start": 0, "end": 60, "cost": 1},
                        {"id": "A", "start": 60, "end": 120, "cost": 1},
                    ],
                }
            )

    def test_minute_out_of_range_rejected(self):
        with self.assertRaises(sc.InputError):
            sc._parse_input(
                {
                    "demands": [{"start": 0, "end": 10080, "required": 1}],
                    "shifts": [],
                }
            )


class CliTest(unittest.TestCase):
    def test_cli_end_to_end(self):
        payload = {
            "demands": [{"start": 600, "end": 660, "required": 2}],
            "shifts": [
                {"id": "cheap", "start": 600, "end": 660, "cost": 3},
                {"id": "pricey", "start": 600, "end": 660, "cost": 5},
            ],
        }
        proc = subprocess.run(
            [sys.executable, str(HERE / "shift_cover.py"), "--compact"],
            input=json.dumps(payload),
            capture_output=True,
            text=True,
        )
        self.assertEqual(proc.returncode, 0, proc.stderr)
        out = json.loads(proc.stdout)
        self.assertEqual(out["status"], "optimal")
        self.assertEqual(out["total_cost"], 6)
        self.assertEqual(out["assignment"], {"cheap": 2})

    def test_cli_rejects_bad_input(self):
        proc = subprocess.run(
            [sys.executable, str(HERE / "shift_cover.py")],
            input="{not json",
            capture_output=True,
            text=True,
        )
        self.assertEqual(proc.returncode, 2)


if __name__ == "__main__":
    unittest.main()
