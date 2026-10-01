import json
import random
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
sys.path.insert(0, str(ROOT))

from migsched.scheduler import (
    CycleError,
    Migration,
    build_predecessors,
    conflicts,
    critical_path_len,
    exact_schedule,
    list_schedule,
    lower_bound,
    plan,
    verify_schedule,
)

PYTHON = sys.executable


def M(name, reads=(), writes=(), deps=()):
    return Migration(name, frozenset(reads), frozenset(writes), tuple(deps))


def acceptance_migrations():
    return [
        M("A", writes={"x"}),
        M("B", reads={"x"}),
        M("C", writes={"y"}),
    ]


class ConflictTests(unittest.TestCase):
    def test_read_read_no_conflict(self):
        self.assertFalse(conflicts(M("A", reads={"x"}), M("B", reads={"x"})))

    def test_write_read_conflict(self):
        self.assertTrue(conflicts(M("A", writes={"x"}), M("B", reads={"x"})))

    def test_read_write_conflict(self):
        self.assertTrue(conflicts(M("A", reads={"x"}), M("B", writes={"x"})))

    def test_write_write_conflict(self):
        self.assertTrue(conflicts(M("A", writes={"x"}), M("B", writes={"x"})))

    def test_disjoint_no_conflict(self):
        self.assertFalse(conflicts(M("A", writes={"x"}), M("B", writes={"y"})))


class AcceptanceBoundaryTests(unittest.TestCase):
    """验收边界：A 写 x，B 读 x，C 写 y，原序 A B C，k=2。"""

    def test_exact_small_example(self):
        result = plan(acceptance_migrations(), k=2)
        self.assertEqual(result["rounds"], [["A", "C"], ["B"]])
        self.assertEqual(result["num_rounds"], 2)
        self.assertEqual(result["lower_bound"], 2)
        self.assertTrue(result["optimal"])
        self.assertEqual(result["status"], "optimal")
        self.assertTrue(result["verification"]["ok"])
        self.assertEqual(result["conflict_edges"], [["A", "B"]])

    def test_a_b_never_same_round(self):
        result = plan(acceptance_migrations(), k=2)
        for rnd in result["rounds"]:
            self.assertFalse({"A", "B"} <= set(rnd), "A 与 B 不得同轮")

    def test_conflict_edge_direction_follows_original_order(self):
        preds, conflict_edges, _ = build_predecessors(acceptance_migrations())
        self.assertIn(0, preds[1])  # A 在 B 前
        self.assertEqual(conflict_edges, [(0, 1)])
        self.assertEqual(preds[2], set())  # C 无前置


class ConstraintTests(unittest.TestCase):
    def test_read_read_parallel_same_round(self):
        result = plan([M("A", reads={"x"}), M("B", reads={"x"})], k=2)
        self.assertEqual(result["num_rounds"], 1)

    def test_write_write_serializes(self):
        result = plan([M("A", writes={"x"}), M("B", writes={"x"})], k=2)
        self.assertEqual(result["num_rounds"], 2)
        self.assertEqual(result["rounds"], [["A"], ["B"]])

    def test_read_then_write_serializes(self):
        result = plan([M("A", reads={"x"}), M("B", writes={"x"})], k=2)
        self.assertEqual(result["num_rounds"], 2)

    def test_explicit_dep_serializes(self):
        result = plan([M("A"), M("B", deps=["A"])], k=2)
        self.assertEqual(result["rounds"], [["A"], ["B"]])

    def test_dep_may_reference_later_position(self):
        result = plan([M("A", deps=["B"]), M("B")], k=2)
        self.assertEqual(result["rounds"], [["B"], ["A"]])

    def test_k1_fully_serial(self):
        result = plan([M("A"), M("B"), M("C")], k=1)
        self.assertEqual(result["num_rounds"], 3)

    def test_no_conflicts_capacity_only(self):
        result = plan([M(str(i)) for i in range(5)], k=2)
        self.assertEqual(result["num_rounds"], 3)  # ceil(5/2)
        self.assertTrue(result["optimal"])

    def test_empty_instance(self):
        result = plan([], k=2)
        self.assertEqual(result["num_rounds"], 0)
        self.assertTrue(result["optimal"])

    def test_chain_critical_path_bound(self):
        migrations = [M("m0")]
        for i in range(1, 6):
            migrations.append(M(f"m{i}", deps=[f"m{i - 1}"]))
        preds, _, _ = build_predecessors(migrations)
        self.assertEqual(critical_path_len(preds), 6)
        self.assertEqual(lower_bound(preds, k=10), 6)
        result = plan(migrations, k=10)
        self.assertEqual(result["num_rounds"], 6)

    def test_cycle_detected(self):
        with self.assertRaises(CycleError):
            build_predecessors([M("A", deps=["B"]), M("B", deps=["A"])])

    def test_cycle_mixing_conflict_and_dep(self):
        # 冲突边 A->B（A 写 x，B 读 x），显式依赖 B<-A 即 A 依赖 B，构成循环
        with self.assertRaises(CycleError):
            build_predecessors(
                [M("A", writes={"x"}, deps=["B"]), M("B", reads={"x"})]
            )

    def test_unknown_dep_rejected(self):
        with self.assertRaises(ValueError):
            build_predecessors([M("A", deps=["ZZZ"])])

    def test_duplicate_name_rejected(self):
        with self.assertRaises(ValueError):
            build_predecessors([M("A"), M("A")])

    def test_invalid_k_rejected(self):
        with self.assertRaises(ValueError):
            plan([M("A")], k=0)


class VerifyTests(unittest.TestCase):
    def _preds(self, migrations):
        return build_predecessors(migrations)[0]

    def test_verify_accepts_feasible(self):
        migrations = acceptance_migrations()
        preds = self._preds(migrations)
        res = verify_schedule(migrations, preds, [[0, 2], [1]], k=2)
        self.assertTrue(res["ok"])
        self.assertEqual(res["checked_precedence_edges"], 1)

    def test_verify_rejects_same_round_conflict(self):
        migrations = acceptance_migrations()
        preds = self._preds(migrations)
        res = verify_schedule(migrations, preds, [[0, 1], [2]], k=2)
        self.assertFalse(res["ok"])

    def test_verify_rejects_over_capacity(self):
        migrations = [M("A"), M("B"), M("C")]
        preds = self._preds(migrations)
        res = verify_schedule(migrations, preds, [[0, 1, 2]], k=2)
        self.assertFalse(res["ok"])

    def test_verify_rejects_missing_and_duplicate(self):
        migrations = [M("A"), M("B")]
        preds = self._preds(migrations)
        res = verify_schedule(migrations, preds, [[0], [0]], k=2)
        self.assertFalse(res["ok"])


class ExactVsHeuristicTests(unittest.TestCase):
    def test_random_small_instances(self):
        rng = random.Random(20261001)
        keys = ["k1", "k2", "k3", "k4"]
        for trial in range(60):
            n = rng.randint(1, 8)
            k = rng.randint(1, 3)
            migrations = []
            for i in range(n):
                reads = {key for key in keys if rng.random() < 0.3}
                writes = {key for key in keys if rng.random() < 0.3}
                deps = []
                if i > 0 and rng.random() < 0.25:
                    deps = [f"m{rng.randrange(i)}"]
                migrations.append(M(f"m{i}", reads=reads, writes=writes, deps=deps))
            preds, _, _ = build_predecessors(migrations)
            exact_rounds, _, _ = exact_schedule(preds, k)
            heur_rounds = list_schedule(preds, k)
            lb = lower_bound(preds, k)
            self.assertLessEqual(lb, len(exact_rounds))
            self.assertLessEqual(len(exact_rounds), len(heur_rounds))
            for rounds in (exact_rounds, heur_rounds):
                res = verify_schedule(migrations, preds, rounds, k)
                self.assertTrue(res["ok"], res["errors"])


class BudgetInterruptionTests(unittest.TestCase):
    def test_interruption_reports_bounds(self):
        # 14 个互不冲突的迁移，k=4：分支因子 C(14,4)=1001，小预算必然中断
        migrations = [M(f"m{i}") for i in range(14)]
        preds, _, _ = build_predecessors(migrations)
        rounds, expanded, proven_lb = exact_schedule(preds, k=4, budget=5)
        self.assertIsNone(rounds)
        self.assertEqual(expanded, 5)
        self.assertGreaterEqual(proven_lb, 1)

        result = plan(migrations, k=4, budget=5)
        self.assertEqual(result["status"], "interrupted")
        self.assertLessEqual(result["lower_bound"], result["upper_bound"])
        self.assertTrue(result["verification"]["ok"])
        self.assertTrue(any("中断" in note for note in result["notes"]))
        # 下界至少为 ceil(14/4)=4，上界为启发式结果
        self.assertGreaterEqual(result["lower_bound"], 4)

    def test_heuristic_path_for_large_n(self):
        migrations = [M(f"m{i}") for i in range(20)]
        result = plan(migrations, k=3, exact_limit=16)
        self.assertEqual(result["status"], "heuristic")
        self.assertTrue(any("未证最优" in note for note in result["notes"]))
        self.assertTrue(result["verification"]["ok"])
        self.assertLessEqual(result["lower_bound"], result["num_rounds"])


class CliTests(unittest.TestCase):
    def run_cli(self, *argv, stdin=None):
        return subprocess.run(
            [PYTHON, "-m", "migsched", *argv],
            capture_output=True,
            text=True,
            cwd=ROOT,
            input=stdin,
        )

    def test_demo_acceptance(self):
        proc = self.run_cli("demo")
        self.assertEqual(proc.returncode, 0, proc.stderr)
        out = json.loads(proc.stdout)
        self.assertEqual(out["rounds"], [["A", "C"], ["B"]])
        self.assertTrue(out["optimal"])

    def test_plan_from_file_and_verify(self):
        spec_path = ROOT / "examples" / "acceptance.json"
        proc = self.run_cli("plan", str(spec_path))
        self.assertEqual(proc.returncode, 0, proc.stderr)
        out = json.loads(proc.stdout)
        self.assertEqual(out["rounds"], [["A", "C"], ["B"]])
        self.assertEqual(out["num_rounds"], 2)

        with tempfile.NamedTemporaryFile(
            "w", suffix=".json", delete=False, encoding="utf-8"
        ) as fh:
            json.dump(out, fh)
            plan_path = fh.name
        proc = self.run_cli("verify", str(spec_path), plan_path)
        self.assertEqual(proc.returncode, 0, proc.stderr)
        self.assertTrue(json.loads(proc.stdout)["ok"])

    def test_verify_rejects_bad_plan(self):
        spec_path = ROOT / "examples" / "acceptance.json"
        bad = {"rounds": [["A", "B"], ["C"]]}  # A 与 B 同轮，违反冲突顺序
        with tempfile.NamedTemporaryFile(
            "w", suffix=".json", delete=False, encoding="utf-8"
        ) as fh:
            json.dump(bad, fh)
            plan_path = fh.name
        proc = self.run_cli("verify", str(spec_path), plan_path)
        self.assertEqual(proc.returncode, 1)
        self.assertFalse(json.loads(proc.stdout)["ok"])

    def test_plan_from_stdin_with_k_override(self):
        spec = json.dumps(
            {"migrations": [{"name": "A", "writes": ["x"]},
                            {"name": "B", "reads": ["x"]}]}
        )
        proc = self.run_cli("plan", "-", "--k", "2", stdin=spec)
        self.assertEqual(proc.returncode, 0, proc.stderr)
        out = json.loads(proc.stdout)
        self.assertEqual(out["rounds"], [["A"], ["B"]])

    def test_plan_invalid_input_exit_code(self):
        proc = self.run_cli("plan", "-", stdin="not json")
        self.assertEqual(proc.returncode, 2)


if __name__ == "__main__":
    unittest.main()
