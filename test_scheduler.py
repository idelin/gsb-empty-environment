#!/usr/bin/env python3
"""scheduler.py 的单元测试。所有数据均为合成数据。"""
import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import scheduler as sc


def job(jid, release=0, deadline=10, duration=1, weight=1, preds=()):
    return sc.Job(jid, release, deadline, duration, weight, tuple(preds))


class HalfOpenBoundaryTests(unittest.TestCase):
    """验收边界：维护 [3,5)，时长 3 的工单可 0 开始 3 结束，不可 1 开始 4 结束。"""

    def setUp(self):
        self.windows = sc.merge_windows([[3, 5]])

    def test_touching_maintenance_start_ok(self):
        self.assertEqual(sc.earliest_start(0, 3, self.windows), 0)  # [0,3) 合法

    def test_overlapping_start_rejected(self):
        # [1,4) 与 [3,5) 相交，须跳到维护结束时刻 5
        self.assertEqual(sc.earliest_start(1, 3, self.windows), 5)

    def test_touching_maintenance_end_ok(self):
        self.assertEqual(sc.earliest_start(5, 3, self.windows), 5)  # [5,8) 合法

    def test_overlap_predicate_half_open(self):
        self.assertFalse(sc.overlaps(0, 3, 3, 5))   # 端点接触不冲突
        self.assertFalse(sc.overlaps(3, 5, 0, 3))
        self.assertTrue(sc.overlaps(1, 4, 3, 5))    # 真相交
        self.assertTrue(sc.overlaps(2, 6, 3, 5))

    def test_solver_schedules_touching_maintenance(self):
        res = sc.solve([job("A", release=0, deadline=10, duration=3)], [[3, 5]])
        self.assertEqual(res["schedule"]["A"], (0, 3))


class DeadlineTests(unittest.TestCase):
    """验收边界：工单在截止时刻完成算按时。"""

    def test_finish_exactly_at_deadline(self):
        res = sc.solve([job("A", release=0, deadline=4, duration=4)], [])
        self.assertEqual(res["schedule"]["A"], (0, 4))
        self.assertEqual(res["weight"], 1)

    def test_finish_one_past_deadline_impossible(self):
        res = sc.solve([job("A", release=0, deadline=3, duration=4)], [])
        self.assertEqual(res["schedule"], {})
        self.assertEqual(res["weight"], 0)


class PrecedenceTests(unittest.TestCase):
    def test_precedence_order_and_touching_allowed(self):
        jobs = [job("A", duration=2, deadline=10),
                job("B", duration=2, deadline=10, preds=["A"])]
        res = sc.solve(jobs, [])
        self.assertEqual(res["schedule"]["A"], (0, 2))
        self.assertEqual(res["schedule"]["B"], (2, 4))  # 紧接开工（接触允许）

    def test_cycle_rejected(self):
        with self.assertRaises(ValueError):
            sc.parse_jobs([
                {"id": "A", "deadline": 5, "duration": 1, "predecessors": ["B"]},
                {"id": "B", "deadline": 5, "duration": 1, "predecessors": ["A"]},
            ])

    def test_unknown_predecessor_rejected(self):
        with self.assertRaises(ValueError):
            sc.parse_jobs([{"id": "A", "deadline": 5, "duration": 1,
                            "predecessors": ["GHOST"]}])


class ObjectiveTests(unittest.TestCase):
    def test_weight_maximized_before_completion(self):
        # 两单互斥（都需独占 [0,5) 窗口），权重高者胜出，即使完成时间更晚
        jobs = [job("LOW", release=0, deadline=5, duration=1, weight=1),
                job("HIGH", release=0, deadline=5, duration=5, weight=10)]
        res = sc.solve(jobs, [])
        self.assertEqual(set(res["schedule"]), {"HIGH"})
        self.assertEqual(res["weight"], 10)
        self.assertEqual(res["completion"], 5)

    def test_completion_minimized_on_weight_tie(self):
        # 权重相同且都能排下：短单先排可最小化总完成时间
        jobs = [job("LONG", duration=3, deadline=10, weight=1),
                job("SHORT", duration=1, deadline=10, weight=1)]
        res = sc.solve(jobs, [])
        self.assertEqual(res["weight"], 2)
        self.assertEqual(res["completion"], 1 + 4)  # SHORT [0,1), LONG [1,4)
        self.assertEqual(res["schedule"]["SHORT"], (0, 1))
        self.assertEqual(res["schedule"]["LONG"], (1, 4))

    def test_release_time_respected(self):
        res = sc.solve([job("A", release=7, deadline=20, duration=2)], [])
        self.assertEqual(res["schedule"]["A"], (7, 9))

    def test_maintenance_forces_gap(self):
        jobs = [job("A", duration=2, deadline=20), job("B", duration=2, deadline=20)]
        res = sc.solve(jobs, [[2, 6]])
        self.assertEqual(res["schedule"]["A"], (0, 2))
        self.assertEqual(res["schedule"]["B"], (6, 8))  # 跳过维护窗口


class UnscheduledReasonTests(unittest.TestCase):
    def test_no_feasible_slot(self):
        jobs = [job("X", release=0, deadline=10, duration=5)]
        res = sc.run({"jobs": [{"id": "X", "release": 0, "deadline": 10,
                                "duration": 5}],
                      "maintenance": [[0, 10]]}, do_verify=False)
        self.assertEqual(res["schedule"], [])
        self.assertEqual(res["unscheduled"][0]["reason_code"], "no_feasible_slot")

    def test_infeasible_precedence_chain(self):
        # J2 自身可行，但前置 J1 最早 [5,8)，J2 需 [8,11) 超过截止 9
        payload = {"jobs": [
            {"id": "J1", "release": 5, "deadline": 10, "duration": 3, "weight": 1},
            {"id": "J2", "release": 0, "deadline": 9, "duration": 3, "weight": 1,
             "predecessors": ["J1"]},
        ], "maintenance": []}
        res = sc.run(payload, do_verify=False)
        self.assertIn("J1", {s["id"] for s in res["schedule"]})
        un = {u["id"]: u["reason_code"] for u in res["unscheduled"]}
        self.assertEqual(un["J2"], "infeasible_precedence_chain")

    def test_resource_contention(self):
        payload = {"jobs": [
            {"id": "BIG", "release": 0, "deadline": 5, "duration": 5, "weight": 10},
            {"id": "SMALL", "release": 0, "deadline": 5, "duration": 1, "weight": 1},
        ], "maintenance": []}
        res = sc.run(payload, do_verify=False)
        un = {u["id"]: u["reason_code"] for u in res["unscheduled"]}
        self.assertEqual(un["SMALL"], "resource_contention")


class VerificationTests(unittest.TestCase):
    def test_small_instance_optimal_verified(self):
        payload = {"jobs": [
            {"id": "A", "release": 0, "deadline": 8, "duration": 2, "weight": 3},
            {"id": "B", "release": 1, "deadline": 9, "duration": 3, "weight": 5},
            {"id": "C", "release": 0, "deadline": 10, "duration": 2, "weight": 4,
             "predecessors": ["A"]},
        ], "maintenance": [[4, 5]]}
        res = sc.run(payload)
        self.assertEqual(res["verification"]["status"], "optimal_verified")
        so = res["verification"]["solver_objective"]
        bo = res["verification"]["brute_force_objective"]
        self.assertEqual(so, bo)

    def test_verification_disabled(self):
        res = sc.run({"jobs": [], "maintenance": []}, do_verify=False)
        self.assertEqual(res["verification"]["status"], "disabled")

    def test_verification_skipped_when_too_large(self):
        jobs = [{"id": f"J{i}", "release": 0, "deadline": 40, "duration": 1,
                 "weight": 1} for i in range(6)]
        res = sc.run({"jobs": jobs, "maintenance": []}, max_combos=10)
        self.assertEqual(res["verification"]["status"], "skipped")


class SolutionPropertyTests(unittest.TestCase):
    """对若干合成算例，直接校验输出满足全部硬约束。"""

    def check_solution(self, payload):
        res = sc.run(payload, do_verify=True)
        jobs = {j["id"]: j for j in payload["jobs"]}
        windows = sc.merge_windows(payload.get("maintenance", []))
        sched = {s["id"]: (s["start"], s["end"]) for s in res["schedule"]}
        for jid, (s, e) in sched.items():
            j = jobs[jid]
            self.assertEqual(e - s, j["duration"])          # 不可抢占、时长完整
            self.assertGreaterEqual(s, j.get("release", 0))
            self.assertLessEqual(e, j["deadline"])          # 截止时刻完成算按时
            for a, b in windows:
                self.assertFalse(sc.overlaps(s, e, a, b))   # 避开维护
            for p in j.get("predecessors", []):
                self.assertIn(p, sched)
                self.assertLessEqual(sched[p][1], s)        # 前置先完成
        ordered = sorted(sched.values())
        for (s1, e1), (s2, e2) in zip(ordered, ordered[1:]):
            self.assertLessEqual(e1, s2)                    # 单机不重叠
        return res

    def test_mixed_instance(self):
        payload = {"jobs": [
            {"id": "A", "release": 0, "deadline": 12, "duration": 3, "weight": 4},
            {"id": "B", "release": 2, "deadline": 12, "duration": 2, "weight": 6},
            {"id": "C", "release": 0, "deadline": 12, "duration": 1, "weight": 2,
             "predecessors": ["B"]},
            {"id": "D", "release": 6, "deadline": 12, "duration": 4, "weight": 5},
        ], "maintenance": [[3, 5], [9, 10]]}
        res = self.check_solution(payload)
        self.assertEqual(res["verification"]["status"], "optimal_verified")

    def test_acceptance_boundary_instance(self):
        # 验收边界算例：维护 [3,5)，时长 3 的工单恰好在 [0,3) 完成
        payload = {"jobs": [
            {"id": "W", "release": 0, "deadline": 3, "duration": 3, "weight": 7},
        ], "maintenance": [[3, 5]]}
        res = self.check_solution(payload)
        self.assertEqual(res["schedule"], [{"id": "W", "start": 0, "end": 3}])
        self.assertEqual(res["verification"]["status"], "optimal_verified")


class CliTests(unittest.TestCase):
    ROOT = Path(__file__).resolve().parent

    def run_cli(self, payload, *extra):
        with tempfile.NamedTemporaryFile(
                "w", suffix=".json", delete=False, encoding="utf-8") as f:
            json.dump(payload, f)
            path = f.name
        try:
            proc = subprocess.run(
                [sys.executable, str(self.ROOT / "scheduler.py"), path, *extra],
                capture_output=True, text=True, timeout=60)
        finally:
            Path(path).unlink(missing_ok=True)
        return proc

    def test_cli_end_to_end(self):
        payload = {"jobs": [
            {"id": "A", "release": 0, "deadline": 10, "duration": 3, "weight": 5},
            {"id": "B", "release": 0, "deadline": 10, "duration": 2, "weight": 4},
        ], "maintenance": [[3, 5]]}
        proc = self.run_cli(payload)
        self.assertEqual(proc.returncode, 0, proc.stderr)
        out = json.loads(proc.stdout)
        self.assertEqual(out["objective"]["total_weight"], 9)
        self.assertEqual(out["verification"]["status"], "optimal_verified")

    def test_cli_no_verify_flag(self):
        proc = self.run_cli({"jobs": [], "maintenance": []}, "--no-verify")
        self.assertEqual(proc.returncode, 0, proc.stderr)
        self.assertEqual(json.loads(proc.stdout)["verification"]["status"],
                         "disabled")

    def test_cli_invalid_input_exit_code(self):
        proc = self.run_cli({"jobs": [{"id": "A", "deadline": 1, "duration": 0}]})
        self.assertEqual(proc.returncode, 2)
        self.assertIn("error", proc.stderr)


if __name__ == "__main__":
    unittest.main(verbosity=2)
