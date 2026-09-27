#!/usr/bin/env python3.11
"""budgeter 的 unittest 测试。所有数据均为合成数据。"""
import json
import random
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

from budgeter import (
    Batch,
    Config,
    Solution,
    Job,
    Tier,
    batch_cost,
    brute_force_min_cost,
    main,
    parse_input,
    solve,
    verify_solution,
)

REPO_ROOT = Path(__file__).resolve().parent


def make_config(**overrides) -> Config:
    base = dict(max_payload=4, fixed_fee=3.0, tiers=())
    base.update(overrides)
    return Config(**base)


class AcceptanceBoundaryTests(unittest.TestCase):
    """题目给定的验收边界。"""

    def test_same_tenant_two_payload2_fit_in_one_batch(self):
        # 同租户两载荷各2、最大载荷4、固定费3、无变动费 -> 合包成本3
        config = make_config()
        jobs = [
            Job("j1", "tenant-a", "chat", 2),
            Job("j2", "tenant-a", "chat", 2),
        ]
        solution = solve(config, jobs)
        self.assertTrue(solution.optimal)
        self.assertEqual(len(solution.batches), 1)
        self.assertAlmostEqual(solution.total_cost, 3.0)

    def test_cross_tenant_never_mixed(self):
        # 不同租户必须两包，成本 6
        config = make_config()
        jobs = [
            Job("j1", "tenant-a", "chat", 2),
            Job("j2", "tenant-b", "chat", 2),
        ]
        solution = solve(config, jobs)
        self.assertTrue(solution.optimal)
        self.assertEqual(len(solution.batches), 2)
        self.assertAlmostEqual(solution.total_cost, 6.0)
        for batch in solution.batches:
            tenants = {j.tenant for j in batch.jobs}
            self.assertEqual(len(tenants), 1)


class LegalityTests(unittest.TestCase):
    def test_cross_endpoint_not_mixed(self):
        config = make_config()
        jobs = [
            Job("j1", "tenant-a", "chat", 2),
            Job("j2", "tenant-a", "embed", 2),
        ]
        solution = solve(config, jobs)
        self.assertEqual(len(solution.batches), 2)
        self.assertAlmostEqual(solution.total_cost, 6.0)

    def test_payload_overflow_forces_split(self):
        config = make_config()
        jobs = [Job("j1", "t", "e", 3), Job("j2", "t", "e", 3)]
        solution = solve(config, jobs)
        self.assertEqual(len(solution.batches), 2)
        self.assertAlmostEqual(solution.total_cost, 6.0)

    def test_zero_payload_jobs_batch_together(self):
        config = make_config()
        jobs = [Job("j1", "t", "e", 0), Job("j2", "t", "e", 0)]
        solution = solve(config, jobs)
        self.assertEqual(len(solution.batches), 1)
        self.assertAlmostEqual(solution.total_cost, 3.0)

    def test_duplicate_job_id_rejected(self):
        data = {
            "config": {"max_payload": 4, "fixed_fee": 3},
            "jobs": [
                {"id": "j1", "tenant": "t", "endpoint": "e", "payload": 1},
                {"id": "j1", "tenant": "t", "endpoint": "e", "payload": 1},
            ],
        }
        with self.assertRaises(ValueError):
            parse_input(data)

    def test_oversized_job_rejected(self):
        data = {
            "config": {"max_payload": 4, "fixed_fee": 3},
            "jobs": [{"id": "j1", "tenant": "t", "endpoint": "e", "payload": 5}],
        }
        with self.assertRaises(ValueError):
            parse_input(data)


class TieredCostTests(unittest.TestCase):
    def test_batch_cost_tiers(self):
        config = make_config(tiers=(Tier(2, 1.0), Tier(4, 5.0)))
        self.assertAlmostEqual(batch_cost(2, config), 4.0)   # 3 + 1
        self.assertAlmostEqual(batch_cost(3, config), 8.0)   # 3 + 5
        self.assertAlmostEqual(batch_cost(4, config), 8.0)   # 3 + 5

    def test_tier_beyond_top_uses_highest(self):
        config = make_config(max_payload=10, tiers=(Tier(2, 1.0), Tier(4, 5.0)))
        self.assertAlmostEqual(batch_cost(9, config), 8.0)

    def test_merging_cheaper_with_mild_tier_fee(self):
        # 固定费3，阶梯 <=2 收0、<=4 收1：合包 3+1=4 < 分包 2*3=6
        config = make_config(tiers=(Tier(2, 0.0), Tier(4, 1.0)))
        jobs = [Job("j1", "t", "e", 2), Job("j2", "t", "e", 2)]
        solution = solve(config, jobs)
        self.assertEqual(len(solution.batches), 1)
        self.assertAlmostEqual(solution.total_cost, 4.0)

    def test_splitting_cheaper_with_steep_tier_fee(self):
        # 固定费1，阶梯 <=2 收0、<=4 收100：分包 2*1=2 < 合包 1+100=101
        config = make_config(fixed_fee=1.0, tiers=(Tier(2, 0.0), Tier(4, 100.0)))
        jobs = [Job("j1", "t", "e", 2), Job("j2", "t", "e", 2)]
        solution = solve(config, jobs)
        self.assertEqual(len(solution.batches), 2)
        self.assertAlmostEqual(solution.total_cost, 2.0)


class ExactSolverVsEnumerationTests(unittest.TestCase):
    """随机合成小规模实例：子集DP结果必须与独立枚举一致。"""

    def test_random_small_instances_match_brute_force(self):
        rng = random.Random(20260927)
        for case in range(30):
            n = rng.randint(1, 8)
            config = Config(
                max_payload=rng.randint(2, 6),
                fixed_fee=float(rng.randint(0, 5)),
                tiers=tuple(
                    sorted(
                        (Tier(rng.randint(1, 6), float(rng.randint(0, 8))) for _ in range(rng.randint(0, 3))),
                        key=lambda t: t.up_to,
                    )
                ),
            )
            jobs = [
                Job(
                    id=f"j{i}",
                    tenant=f"t{rng.randint(1, 2)}",
                    endpoint=f"e{rng.randint(1, 2)}",
                    payload=rng.randint(0, config.max_payload),
                )
                for i in range(n)
            ]
            solution = solve(config, jobs)
            enum_best, _ = brute_force_min_cost(config, jobs)
            self.assertAlmostEqual(
                solution.total_cost,
                enum_best,
                msg=f"case {case}: config={config} jobs={jobs}",
            )
            report = verify_solution(config, jobs, solution)
            self.assertTrue(report["coverage_ok"], report["problems"])
            self.assertTrue(report["legality_ok"], report["problems"])
            self.assertTrue(report["enumeration"]["consistent"])


class VerificationTests(unittest.TestCase):
    def test_verify_detects_cross_tenant_batch(self):
        config = make_config()
        jobs = [Job("j1", "ta", "e", 1), Job("j2", "tb", "e", 1)]
        bad = Solution(
            batches=[Batch((jobs[0], jobs[1]))],
            total_cost=3.0,
            optimal=False,
            notes=[],
        )
        report = verify_solution(config, jobs, bad)
        self.assertFalse(report["legality_ok"])

    def test_verify_detects_missing_job(self):
        config = make_config()
        jobs = [Job("j1", "t", "e", 1), Job("j2", "t", "e", 1)]
        bad = Solution(batches=[Batch((jobs[0],))], total_cost=3.0, optimal=False, notes=[])
        report = verify_solution(config, jobs, bad)
        self.assertFalse(report["coverage_ok"])

    def test_greedy_path_marks_not_optimal(self):
        config = make_config(exact_threshold=2)
        jobs = [Job(f"j{i}", "t", "e", 1) for i in range(5)]
        solution = solve(config, jobs)
        self.assertFalse(solution.optimal)
        self.assertTrue(solution.notes)
        report = verify_solution(config, jobs, solution)
        self.assertTrue(report["coverage_ok"])
        self.assertTrue(report["legality_ok"])


class CliTests(unittest.TestCase):
    def run_cli(self, payload: dict, *extra_args: str) -> subprocess.CompletedProcess:
        with tempfile.NamedTemporaryFile(
            "w", suffix=".json", delete=False, encoding="utf-8"
        ) as fh:
            json.dump(payload, fh)
            path = fh.name
        return subprocess.run(
            [sys.executable, str(REPO_ROOT / "budgeter.py"), path, *extra_args],
            capture_output=True,
            text=True,
        )

    def test_cli_acceptance_same_tenant(self):
        payload = {
            "config": {"max_payload": 4, "fixed_fee": 3, "payload_tiers": []},
            "jobs": [
                {"id": "j1", "tenant": "tenant-a", "endpoint": "chat", "payload": 2},
                {"id": "j2", "tenant": "tenant-a", "endpoint": "chat", "payload": 2},
            ],
        }
        result = self.run_cli(payload, "--json")
        self.assertEqual(result.returncode, 0, result.stderr)
        out = json.loads(result.stdout)
        self.assertTrue(out["optimal"])
        self.assertEqual(out["num_batches"], 1)
        self.assertAlmostEqual(out["total_cost"], 3.0)
        self.assertTrue(out["verification"]["enumeration"]["consistent"])

    def test_cli_acceptance_cross_tenant(self):
        payload = {
            "config": {"max_payload": 4, "fixed_fee": 3, "payload_tiers": []},
            "jobs": [
                {"id": "j1", "tenant": "tenant-a", "endpoint": "chat", "payload": 2},
                {"id": "j2", "tenant": "tenant-b", "endpoint": "chat", "payload": 2},
            ],
        }
        result = self.run_cli(payload, "--json")
        self.assertEqual(result.returncode, 0, result.stderr)
        out = json.loads(result.stdout)
        self.assertEqual(out["num_batches"], 2)
        self.assertAlmostEqual(out["total_cost"], 6.0)

    def test_cli_invalid_input_returns_2(self):
        payload = {
            "config": {"max_payload": 4, "fixed_fee": 3},
            "jobs": [{"id": "j1", "tenant": "t", "endpoint": "e", "payload": 99}],
        }
        result = self.run_cli(payload)
        self.assertEqual(result.returncode, 2)
        self.assertIn("输入错误", result.stderr)


if __name__ == "__main__":
    unittest.main(verbosity=2)
