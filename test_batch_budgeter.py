"""batch_budgeter 的 unittest 测试。所有数据均为合成数据。"""

import json
import math
import random
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

from batch_budgeter import (
    CostModel,
    Job,
    brute_force_best,
    parse_problem,
    run,
    solve,
    verify_cover_exactly_once,
)

MODULE = Path(__file__).with_name("batch_budgeter.py")


def make_problem(jobs, max_payload=4, fixed_fee=3, tiers=None):
    return {
        "max_payload": max_payload,
        "fixed_fee": fixed_fee,
        "payload_tiers": tiers or [],
        "jobs": jobs,
    }


class AcceptanceBoundaryTest(unittest.TestCase):
    """题目给定的验收边界。"""

    def test_same_tenant_two_payload2_fit_in_one_package(self):
        # 同租户同端点，两载荷各 2，最大载荷 4，固定费 3，无变动费 → 合包成本 3
        result = run(make_problem([
            {"id": "a", "tenant": "t1", "endpoint": "ep", "payload": 2},
            {"id": "b", "tenant": "t1", "endpoint": "ep", "payload": 2},
        ]))
        self.assertEqual(result["total_cost"], 3)
        self.assertEqual(result["package_count"], 1)
        self.assertEqual(result["packages"][0]["payload"], 4)
        self.assertEqual(result["verification"]["status"], "confirmed")

    def test_different_tenants_must_not_share_package(self):
        # 不同租户严格禁止混包 → 两包成本 6
        result = run(make_problem([
            {"id": "a", "tenant": "t1", "endpoint": "ep", "payload": 2},
            {"id": "b", "tenant": "t2", "endpoint": "ep", "payload": 2},
        ]))
        self.assertEqual(result["total_cost"], 6)
        self.assertEqual(result["package_count"], 2)
        tenants = {p["tenant"] for p in result["packages"]}
        self.assertEqual(tenants, {"t1", "t2"})


class LegalityTest(unittest.TestCase):
    def test_different_endpoints_not_mixed(self):
        result = run(make_problem([
            {"id": "a", "tenant": "t1", "endpoint": "ep1", "payload": 2},
            {"id": "b", "tenant": "t1", "endpoint": "ep2", "payload": 2},
        ]))
        self.assertEqual(result["total_cost"], 6)
        self.assertEqual(result["package_count"], 2)

    def test_payload_overflow_forces_split(self):
        # 2 + 3 > 4，必须拆成两包
        result = run(make_problem([
            {"id": "a", "tenant": "t1", "endpoint": "ep", "payload": 2},
            {"id": "b", "tenant": "t1", "endpoint": "ep", "payload": 3},
        ]))
        self.assertEqual(result["total_cost"], 6)
        self.assertEqual(result["package_count"], 2)

    def test_no_cross_tenant_in_any_package(self):
        jobs, model = parse_problem(make_problem([
            {"id": f"j{i}", "tenant": f"t{i % 3}", "endpoint": "ep", "payload": 1}
            for i in range(6)
        ]))
        solution = solve(jobs, model)
        for pkg in solution.packages:
            self.assertEqual(len({j.tenant for j in pkg.jobs}), 1)
            self.assertLessEqual(pkg.payload, model.max_payload)
        verify_cover_exactly_once(jobs, solution)


class CostModelTest(unittest.TestCase):
    def test_tier_fee_steps(self):
        model = CostModel(max_payload=10, fixed_fee=3,
                          payload_tiers=((2, 1.0), (5, 2.5), (10, 4.0)))
        self.assertEqual(model.package_cost(0), 4.0)   # 落入第一档
        self.assertEqual(model.package_cost(2), 4.0)
        self.assertEqual(model.package_cost(3), 5.5)
        self.assertEqual(model.package_cost(10), 7.0)

    def test_tier_fee_prefers_cheaper_packing(self):
        # 固定费 3，阶梯 [4→0, 8→10]：两个载荷 4 合包总载荷 8 触发 10 元档，
        # 合包 3+10=13 贵于分包 3+3=6，最优应分包。
        result = run(make_problem(
            [{"id": "a", "tenant": "t", "endpoint": "e", "payload": 4},
             {"id": "b", "tenant": "t", "endpoint": "e", "payload": 4}],
            max_payload=8, fixed_fee=3, tiers=[[4, 0], [8, 10]],
        ))
        self.assertEqual(result["total_cost"], 6)
        self.assertEqual(result["package_count"], 2)

    def test_payload_beyond_tier_table_rejected(self):
        model = CostModel(max_payload=10, fixed_fee=1, payload_tiers=((5, 1.0),))
        with self.assertRaises(ValueError):
            model.tier_fee(6)


class InputValidationTest(unittest.TestCase):
    def test_single_job_exceeding_max_payload_infeasible(self):
        with self.assertRaises(ValueError):
            parse_problem(make_problem(
                [{"id": "a", "tenant": "t", "endpoint": "e", "payload": 5}],
                max_payload=4,
            ))

    def test_duplicate_job_id_rejected(self):
        with self.assertRaises(ValueError):
            parse_problem(make_problem([
                {"id": "a", "tenant": "t", "endpoint": "e", "payload": 1},
                {"id": "a", "tenant": "t", "endpoint": "e", "payload": 1},
            ]))

    def test_empty_job_list(self):
        result = run(make_problem([]))
        self.assertEqual(result["total_cost"], 0)
        self.assertEqual(result["package_count"], 0)


class BruteForceCrossCheckTest(unittest.TestCase):
    """DP 结果与独立暴力枚举对照（合成随机小规模数据）。"""

    def test_random_small_cases_match_brute_force(self):
        rng = random.Random(20260928)
        for case in range(30):
            n = rng.randint(1, 8)
            jobs = [
                Job(
                    job_id=f"j{i}",
                    tenant=f"t{rng.randint(1, 3)}",
                    endpoint=f"e{rng.randint(1, 2)}",
                    payload=rng.randint(1, 5),
                )
                for i in range(n)
            ]
            model = CostModel(
                max_payload=rng.randint(5, 10),
                fixed_fee=rng.choice([1, 2, 3, 5]),
                payload_tiers=tuple(rng.choice([
                    (), ((4, 1.0), (10, 3.0)), ((3, 0.5), (6, 2.0), (10, 5.0)),
                ])),
            )
            solution = solve(jobs, model)
            verify_cover_exactly_once(jobs, solution)
            brute = brute_force_best(jobs, model)
            self.assertTrue(
                math.isclose(solution.total_cost, brute, abs_tol=1e-9),
                f"case {case}: DP={solution.total_cost} brute={brute}",
            )


class CliTest(unittest.TestCase):
    def test_cli_end_to_end(self):
        problem = make_problem([
            {"id": "a", "tenant": "t1", "endpoint": "ep", "payload": 2},
            {"id": "b", "tenant": "t1", "endpoint": "ep", "payload": 2},
            {"id": "c", "tenant": "t2", "endpoint": "ep", "payload": 2},
        ])
        with tempfile.NamedTemporaryFile(
            "w", suffix=".json", delete=False, encoding="utf-8"
        ) as fh:
            json.dump(problem, fh)
            path = fh.name
        proc = subprocess.run(
            [sys.executable, str(MODULE), path],
            capture_output=True, text=True,
        )
        self.assertEqual(proc.returncode, 0, proc.stderr)
        out = json.loads(proc.stdout)
        self.assertEqual(out["total_cost"], 6)  # t1 合包 3 + t2 单包 3
        self.assertEqual(out["package_count"], 2)
        self.assertEqual(out["verification"]["status"], "confirmed")

    def test_cli_rejects_bad_input(self):
        with tempfile.NamedTemporaryFile(
            "w", suffix=".json", delete=False, encoding="utf-8"
        ) as fh:
            fh.write('{"max_payload": 4}')
            path = fh.name
        proc = subprocess.run(
            [sys.executable, str(MODULE), path],
            capture_output=True, text=True,
        )
        self.assertEqual(proc.returncode, 2)
        self.assertIn("错误", proc.stderr)


if __name__ == "__main__":
    unittest.main()
