#!/usr/bin/env python3
"""migration_scheduler 的 unittest 测试（全部使用合成数据）。"""
import json
import pathlib
import random
import subprocess
import sys
import unittest

ROOT = pathlib.Path(__file__).resolve().parent
sys.path.insert(0, str(ROOT))

from migration_scheduler import (
    Migration,
    budget_bounds,
    build_predecessors,
    conflicts,
    exact_min_rounds,
    greedy_schedule,
    lower_bound_rounds,
    max_tasks_within,
    solve,
    topo_order,
    verify_schedule,
)


def mig(name, reads=(), writes=(), deps=()):
    return Migration(name=name, reads=frozenset(reads),
                     writes=frozenset(writes), deps=tuple(deps))


def acceptance_migrations():
    # 验收边界：A 写 x，B 读 x，C 写 y，原序 A B C
    return [mig("A", writes=["x"]), mig("B", reads=["x"]), mig("C", writes=["y"])]


class ConflictTests(unittest.TestCase):
    def test_write_read_conflict(self):
        self.assertTrue(conflicts(mig("A", writes=["x"]), mig("B", reads=["x"])))

    def test_read_write_conflict(self):
        self.assertTrue(conflicts(mig("A", reads=["x"]), mig("B", writes=["x"])))

    def test_write_write_conflict(self):
        self.assertTrue(conflicts(mig("A", writes=["x"]), mig("B", writes=["x"])))

    def test_read_read_no_conflict(self):
        self.assertFalse(conflicts(mig("A", reads=["x"]), mig("B", reads=["x"])))

    def test_disjoint_no_conflict(self):
        self.assertFalse(conflicts(mig("A", writes=["x"]), mig("B", writes=["y"])))


class AcceptanceTests(unittest.TestCase):
    """验收边界：k=2 时第一轮可 A C，第二轮 B；不得同轮 A B。"""

    def test_acceptance_plan(self):
        ms = acceptance_migrations()
        preds = build_predecessors(ms)
        self.assertEqual(preds["B"], {"A"})
        self.assertEqual(preds["C"], set())
        result = solve(ms, k=2)
        self.assertEqual(result["rounds"], [["A", "C"], ["B"]])
        self.assertEqual(result["round_count"], 2)
        self.assertTrue(result["optimal"])
        self.assertTrue(result["evidence"]["ok"])

    def test_a_b_never_same_round(self):
        ms = acceptance_migrations()
        for k in (1, 2, 3):
            result = solve(ms, k=k)
            for r in result["rounds"]:
                self.assertFalse("A" in r and "B" in r,
                                 f"k={k} 时 A、B 不得同轮: {r}")
            pos = {n: i for i, r in enumerate(result["rounds"]) for n in r}
            self.assertLess(pos["A"], pos["B"])

    def test_k1_fully_sequential(self):
        result = solve(acceptance_migrations(), k=1)
        self.assertEqual(result["rounds"], [["A"], ["B"], ["C"]])


class PrecedenceTests(unittest.TestCase):
    def test_explicit_deps_respected(self):
        ms = [mig("A", writes=["a"]), mig("B", writes=["b"], deps=["A"])]
        result = solve(ms, k=2)
        pos = {n: i for i, r in enumerate(result["rounds"]) for n in r}
        self.assertLess(pos["A"], pos["B"])
        self.assertEqual(result["round_count"], 2)

    def test_cycle_detected(self):
        ms = [mig("A", deps=["B"]), mig("B", deps=["A"])]
        preds = build_predecessors(ms)
        with self.assertRaises(ValueError):
            topo_order(ms, preds)

    def test_unknown_dep_rejected(self):
        with self.assertRaises(ValueError):
            build_predecessors([mig("A", deps=["ZZZ"])])

    def test_duplicate_name_rejected(self):
        with self.assertRaises(ValueError):
            build_predecessors([mig("A"), mig("A")])

    def test_chain_lower_bound(self):
        # 5 个迁移写同一键，即使 k 很大也需 5 轮
        ms = [mig(f"M{i}", writes=["x"]) for i in range(5)]
        preds = build_predecessors(ms)
        names = [m.name for m in ms]
        self.assertEqual(lower_bound_rounds(names, 10, preds), 5)
        result = solve(ms, k=10)
        self.assertEqual(result["round_count"], 5)
        self.assertTrue(result["optimal"])


class ExactnessTests(unittest.TestCase):
    def test_greedy_gap_case(self):
        # 合成案例：贪心 4 轮，精确最优 3 轮
        ms = (
            [mig(f"D{i}", writes=[f"w{i}"]) for i in range(3)]
            + [mig(f"C{i}", writes=["k1"]) for i in range(3)]
        )
        preds = build_predecessors(ms)
        greedy = greedy_schedule(ms, 2, preds)
        total, schedule = exact_min_rounds(ms, 2, preds)
        self.assertEqual(len(greedy), 4)
        self.assertEqual(total, 3)
        self.assertEqual(lower_bound_rounds([m.name for m in ms], 2, preds), 3)
        self.assertTrue(verify_schedule(ms, 2, preds, schedule)["ok"])

    def test_exact_never_worse_than_greedy_random(self):
        rng = random.Random(20261001)
        for trial in range(40):
            n = rng.randint(1, 8)
            keys = ["k0", "k1", "k2"]
            ms = []
            for i in range(n):
                reads = {k for k in keys if rng.random() < 0.4}
                writes = {k for k in keys if rng.random() < 0.4}
                ms.append(mig(f"M{i}", reads=reads, writes=writes))
            k = rng.randint(1, 3)
            preds = build_predecessors(ms)
            names = [m.name for m in ms]
            total, schedule = exact_min_rounds(ms, k, preds)
            greedy = greedy_schedule(ms, k, preds)
            lb = lower_bound_rounds(names, k, preds)
            self.assertGreaterEqual(total, lb, f"trial={trial}")
            self.assertLessEqual(total, len(greedy), f"trial={trial}")
            self.assertTrue(verify_schedule(ms, k, preds, schedule)["ok"],
                            f"trial={trial}")


class BudgetTests(unittest.TestCase):
    def test_acceptance_budget(self):
        ms = acceptance_migrations()
        preds = build_predecessors(ms)
        b1 = budget_bounds(ms, 2, preds, 1)
        self.assertEqual((b1["lower_completed"], b1["upper_completed"],
                          b1["exact_max_completed"]), (2, 2, 2))
        b0 = budget_bounds(ms, 2, preds, 0)
        self.assertEqual((b0["lower_completed"], b0["upper_completed"],
                          b0["exact_max_completed"]), (0, 0, 0))
        b2 = budget_bounds(ms, 2, preds, 2)
        self.assertEqual(b2["exact_max_completed"], 3)

    def test_budget_bounds_sandwich_random(self):
        rng = random.Random(7)
        for _ in range(20):
            n = rng.randint(1, 8)
            ms = [mig(f"M{i}", writes=[rng.choice(["a", "b", "c"])])
                  for i in range(n)]
            k = rng.randint(1, 3)
            t = rng.randint(0, 4)
            preds = build_predecessors(ms)
            b = budget_bounds(ms, k, preds, t)
            self.assertLessEqual(b["lower_completed"], b["exact_max_completed"])
            self.assertLessEqual(b["exact_max_completed"], b["upper_completed"])
            self.assertEqual(b["exact_max_completed"],
                             max_tasks_within(ms, k, preds, t))

    def test_budget_exact_disabled_for_large(self):
        ms = [mig(f"M{i}", writes=[f"w{i}"]) for i in range(25)]
        preds = build_predecessors(ms)
        b = budget_bounds(ms, 3, preds, 2, exact_limit=20)
        self.assertFalse(b["exact"])
        self.assertIsNone(b["exact_max_completed"])
        self.assertEqual(b["upper_completed"], 6)


class VerifyTests(unittest.TestCase):
    def test_verify_rejects_infeasible(self):
        ms = acceptance_migrations()
        preds = build_predecessors(ms)
        bad = verify_schedule(ms, 2, preds, [["A", "B"], ["C"]])
        self.assertFalse(bad["ok"])
        self.assertFalse(bad["checks"][2]["ok"])  # 前置约束检查失败

    def test_verify_rejects_duplicate(self):
        ms = acceptance_migrations()
        preds = build_predecessors(ms)
        bad = verify_schedule(ms, 2, preds, [["A", "C"], ["B", "A"]])
        self.assertFalse(bad["ok"])


class CliTests(unittest.TestCase):
    def run_cli(self, *args, stdin=None):
        return subprocess.run(
            [sys.executable, str(ROOT / "migration_scheduler.py"), *args],
            capture_output=True, text=True, input=stdin,
        )

    def test_cli_acceptance_file(self):
        proc = self.run_cli(str(ROOT / "examples" / "acceptance.json"),
                            "--budget", "1")
        self.assertEqual(proc.returncode, 0, proc.stderr)
        out = json.loads(proc.stdout)
        self.assertEqual(out["rounds"], [["A", "C"], ["B"]])
        self.assertTrue(out["optimal"])
        self.assertEqual(out["budget"]["exact_max_completed"], 2)

    def test_cli_stdin_and_k_override(self):
        payload = json.dumps({
            "k": 1,
            "migrations": [
                {"name": "A", "writes": ["x"]},
                {"name": "B", "reads": ["x"]},
                {"name": "C", "writes": ["y"]},
            ],
        })
        proc = self.run_cli("-", "--k", "2", stdin=payload)
        self.assertEqual(proc.returncode, 0, proc.stderr)
        out = json.loads(proc.stdout)
        self.assertEqual(out["round_count"], 2)

    def test_cli_cycle_error(self):
        payload = json.dumps({
            "k": 2,
            "migrations": [
                {"name": "A", "deps": ["B"]},
                {"name": "B", "deps": ["A"]},
            ],
        })
        proc = self.run_cli("-", stdin=payload)
        self.assertEqual(proc.returncode, 2)
        self.assertIn("环", proc.stderr)


if __name__ == "__main__":
    unittest.main(verbosity=2)
