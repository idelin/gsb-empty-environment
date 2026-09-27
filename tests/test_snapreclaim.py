"""Unittest suite for snapreclaim.  All data is synthetic."""
from __future__ import annotations

import itertools
import json
import random
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

from snapreclaim.model import (Problem, ProblemFormatError, Snapshot,
                               compute_reclaim, problem_from_dict)
from snapreclaim.solver import solve_max_reclaim, solve_min_loss


def make_problem(block_sizes, snap_specs):
    """snap_specs: list of (id, loss, [block ids])."""
    return Problem(
        block_sizes=dict(block_sizes),
        snapshots=tuple(
            Snapshot(snap_id=sid, loss=loss, blocks=frozenset(blocks))
            for sid, loss, blocks in snap_specs),
    )


def brute_force_min_loss(problem, required_bytes):
    """Exhaustive reference implementation (min-loss objective)."""
    snaps = problem.snapshots
    best = None
    for r in range(len(snaps) + 1):
        for combo in itertools.combinations(snaps, r):
            ids = sorted(s.snap_id for s in combo)
            gain = compute_reclaim(problem, ids)
            if gain < required_bytes:
                continue
            loss = sum(s.loss for s in combo)
            key = (loss, len(ids), tuple(ids))
            if best is None or key < best[0]:
                best = (key, ids, loss, gain)
    return best


def brute_force_max_reclaim(problem, loss_budget):
    """Exhaustive reference implementation (budget objective)."""
    snaps = problem.snapshots
    best = None
    for r in range(len(snaps) + 1):
        for combo in itertools.combinations(snaps, r):
            ids = sorted(s.snap_id for s in combo)
            loss = sum(s.loss for s in combo)
            if loss > loss_budget:
                continue
            gain = compute_reclaim(problem, ids)
            key = (-gain, loss, len(ids), tuple(ids))
            if best is None or key < best[0]:
                best = (key, ids, loss, gain)
    return best


class AcceptanceBoundaryTest(unittest.TestCase):
    """Two snapshots share one 10-byte block: deleting either alone
    reclaims 0, deleting both reclaims 10.  Individual amounts must not
    be summed."""

    def setUp(self):
        self.problem = make_problem(
            {"blk": 10},
            [("snap-1", 4, ["blk"]), ("snap-2", 6, ["blk"])])

    def test_single_deletion_reclaims_nothing(self):
        self.assertEqual(compute_reclaim(self.problem, ["snap-1"]), 0)
        self.assertEqual(compute_reclaim(self.problem, ["snap-2"]), 0)

    def test_double_deletion_reclaims_block(self):
        self.assertEqual(compute_reclaim(self.problem, ["snap-1", "snap-2"]), 10)

    def test_solver_requires_both_for_10_bytes(self):
        result = solve_min_loss(self.problem, 10)
        self.assertTrue(result.feasible)
        self.assertTrue(result.proven_optimal)
        self.assertEqual(result.selected, ["snap-1", "snap-2"])
        self.assertEqual(result.total_loss, 10)
        self.assertEqual(result.reclaimed_bytes, 10)

    def test_solver_returns_empty_for_zero_requirement(self):
        result = solve_min_loss(self.problem, 0)
        self.assertTrue(result.feasible)
        self.assertEqual(result.selected, [])
        self.assertEqual(result.total_loss, 0)


class ComplementarityTest(unittest.TestCase):
    """Shared blocks make per-snapshot amounts non-additive."""

    def setUp(self):
        # b1 shared by s1+s2, b2 shared by s1+s3, b3 only in s3.
        self.problem = make_problem(
            {"b1": 10, "b2": 5, "b3": 7},
            [("s1", 1, ["b1", "b2"]),
             ("s2", 1, ["b1"]),
             ("s3", 100, ["b2", "b3"])])

    def test_individual_reclaims(self):
        self.assertEqual(compute_reclaim(self.problem, ["s1"]), 0)
        self.assertEqual(compute_reclaim(self.problem, ["s2"]), 0)
        self.assertEqual(compute_reclaim(self.problem, ["s3"]), 7)

    def test_joint_reclaim_exceeds_sum_of_individuals(self):
        # 0 + 0 individually, but 10 together: complementarity.
        self.assertEqual(compute_reclaim(self.problem, ["s1", "s2"]), 10)

    def test_solver_picks_complementary_pair(self):
        # Naive per-snapshot accounting would see only s3 as "productive".
        result = solve_min_loss(self.problem, 10)
        self.assertTrue(result.proven_optimal)
        self.assertEqual(result.selected, ["s1", "s2"])
        self.assertEqual(result.total_loss, 2)
        self.assertEqual(result.reclaimed_bytes, 10)


class TieBreakTest(unittest.TestCase):
    def test_fewer_deletions_wins_on_equal_loss(self):
        # {solo} and {a, b} both reclaim 10 with loss 5; prefer {solo}.
        problem = make_problem(
            {"b1": 10, "b2": 6, "b3": 4},
            [("solo", 5, ["b1"]),
             ("a", 2, ["b2"]),
             ("b", 3, ["b3"])])
        result = solve_min_loss(problem, 10)
        self.assertEqual(result.selected, ["solo"])
        self.assertEqual(result.total_loss, 5)

    def test_lexicographic_ids_win_on_equal_loss_and_count(self):
        problem = make_problem(
            {"b1": 10, "b2": 10},
            [("snap-z", 5, ["b1"]),
             ("snap-a", 5, ["b2"])])
        result = solve_min_loss(problem, 10)
        self.assertEqual(result.selected, ["snap-a"])

    def test_lexicographic_compares_elementwise(self):
        # Feasible pairs: {a, z} frees p+q, {b, c} frees shared r.
        # Both cost 6 with 2 deletions; {a, z} wins lexicographically.
        problem = make_problem(
            {"p": 6, "q": 6, "r": 12},
            [("a", 1, ["p"]),
             ("z", 5, ["q"]),
             ("b", 2, ["r"]),
             ("c", 4, ["r"])])
        result = solve_min_loss(problem, 12)
        self.assertEqual(result.selected, ["a", "z"])
        self.assertEqual(result.total_loss, 6)


class BudgetModeTest(unittest.TestCase):
    def test_max_reclaim_within_budget(self):
        problem = make_problem(
            {"b1": 10, "b2": 20, "b3": 40},
            [("s1", 3, ["b1"]),
             ("s2", 6, ["b2"]),
             ("s3", 10, ["b3"])])
        result = solve_max_reclaim(problem, 9)
        self.assertTrue(result.proven_optimal)
        self.assertEqual(result.selected, ["s1", "s2"])
        self.assertEqual(result.reclaimed_bytes, 30)
        self.assertEqual(result.total_loss, 9)

    def test_budget_respects_shared_blocks(self):
        # b1 shared: deleting only one of s1/s2 wastes budget.
        problem = make_problem(
            {"b1": 10, "b2": 5},
            [("s1", 4, ["b1"]),
             ("s2", 4, ["b1"]),
             ("s3", 4, ["b2"])])
        result = solve_max_reclaim(problem, 8)
        self.assertEqual(result.selected, ["s1", "s2"])
        self.assertEqual(result.reclaimed_bytes, 10)

    def test_zero_budget_selects_nothing(self):
        problem = make_problem({"b1": 10}, [("s1", 1, ["b1"])])
        result = solve_max_reclaim(problem, 0)
        self.assertEqual(result.selected, [])
        self.assertEqual(result.reclaimed_bytes, 0)


class EdgeCaseTest(unittest.TestCase):
    def test_infeasible_requirement(self):
        problem = make_problem({"b1": 10}, [("s1", 1, ["b1"])])
        result = solve_min_loss(problem, 11)
        self.assertFalse(result.feasible)
        self.assertTrue(result.proven_optimal)  # exhaustively proven

    def test_zero_loss_snapshots(self):
        problem = make_problem(
            {"b1": 10, "b2": 10},
            [("free", 0, ["b1"]), ("costly", 5, ["b2"])])
        result = solve_min_loss(problem, 10)
        self.assertEqual(result.selected, ["free"])
        self.assertEqual(result.total_loss, 0)

    def test_unreferenced_blocks_never_reclaimed(self):
        problem = make_problem(
            {"b1": 10, "orphan": 99},
            [("s1", 1, ["b1"])])
        self.assertEqual(compute_reclaim(problem, ["s1"]), 10)

    def test_unknown_snapshot_id_rejected(self):
        problem = make_problem({"b1": 10}, [("s1", 1, ["b1"])])
        with self.assertRaises(ValueError):
            compute_reclaim(problem, ["nope"])

    def test_node_limit_marks_result_not_proven(self):
        problem = make_problem(
            {"b1": 10, "b2": 10},
            [("s1", 1, ["b1"]), ("s2", 1, ["b2"])])
        result = solve_min_loss(problem, 20, node_limit=1)
        self.assertFalse(result.proven_optimal)

    def test_invalid_input_rejected(self):
        with self.assertRaises(ProblemFormatError):
            problem_from_dict({"blocks": {"b": 1}, "snapshots": [
                {"id": "s", "loss": -1, "file_versions": []}]})
        with self.assertRaises(ProblemFormatError):
            problem_from_dict({"blocks": {"b": 1}, "snapshots": [
                {"id": "s", "loss": 0, "file_versions": ["missing"]}]})


class FileVersionIndirectionTest(unittest.TestCase):
    def test_blocks_shared_through_file_versions(self):
        problem = problem_from_dict({
            "blocks": {"blk": 10},
            "file_versions": {"fv-a": ["blk"], "fv-b": ["blk"]},
            "snapshots": [
                {"id": "snap-1", "loss": 1, "file_versions": ["fv-a"]},
                {"id": "snap-2", "loss": 1, "file_versions": ["fv-b"]},
            ],
        })
        self.assertEqual(compute_reclaim(problem, ["snap-1"]), 0)
        self.assertEqual(compute_reclaim(problem, ["snap-1", "snap-2"]), 10)


class BruteForceCrossCheckTest(unittest.TestCase):
    """Random synthetic instances: exact solver must match exhaustive
    enumeration on both objectives, including all tie-breaks."""

    def test_random_instances(self):
        rng = random.Random(20260927)
        for trial in range(60):
            n_blocks = rng.randint(1, 6)
            n_snaps = rng.randint(1, 9)
            block_sizes = {f"b{i}": rng.randint(1, 20)
                           for i in range(n_blocks)}
            block_ids = list(block_sizes)
            specs = []
            for s in range(n_snaps):
                k = rng.randint(1, n_blocks)
                blocks = rng.sample(block_ids, k)
                specs.append((f"s{s}", rng.randint(0, 15), blocks))
            problem = make_problem(block_sizes, specs)

            total = sum(block_sizes.values())
            required = rng.randint(0, total + 5)
            result = solve_min_loss(problem, required)
            self.assertTrue(result.proven_optimal)
            expected = brute_force_min_loss(problem, required)
            if expected is None:
                self.assertFalse(result.feasible,
                                 f"trial {trial}: solver found solution "
                                 "but brute force found none")
            else:
                (_, ids, loss, gain) = expected
                self.assertTrue(result.feasible)
                self.assertEqual(result.selected, ids, f"trial {trial}")
                self.assertEqual(result.total_loss, loss)
                self.assertEqual(result.reclaimed_bytes, gain)

            budget = rng.randint(0, 30)
            result = solve_max_reclaim(problem, budget)
            self.assertTrue(result.proven_optimal)
            (_, ids, loss, gain) = brute_force_max_reclaim(problem, budget)
            self.assertEqual(result.selected, ids, f"trial {trial}")
            self.assertEqual(result.total_loss, loss)
            self.assertEqual(result.reclaimed_bytes, gain)


class CliTest(unittest.TestCase):
    def run_cli(self, *args, stdin_text=None):
        repo = Path(__file__).resolve().parent.parent
        return subprocess.run(
            [sys.executable, "-m", "snapreclaim", *args],
            cwd=repo, input=stdin_text, capture_output=True, text=True)

    def test_solve_required_bytes(self):
        repo = Path(__file__).resolve().parent.parent
        proc = self.run_cli("solve",
                            str(repo / "examples" / "shared_block.json"),
                            "--required-bytes", "10")
        self.assertEqual(proc.returncode, 0, proc.stderr)
        out = json.loads(proc.stdout)
        self.assertEqual(out["selected"], ["snap-1", "snap-2"])
        self.assertEqual(out["total_loss"], 10)
        self.assertEqual(out["reclaimed_bytes"], 10)
        self.assertTrue(out["proven_optimal"])

    def test_solve_loss_budget(self):
        repo = Path(__file__).resolve().parent.parent
        proc = self.run_cli("solve", str(repo / "examples" / "sample.json"),
                            "--loss-budget", "10")
        self.assertEqual(proc.returncode, 0, proc.stderr)
        out = json.loads(proc.stdout)
        self.assertTrue(out["proven_optimal"])
        self.assertLessEqual(out["total_loss"], 10)

    def test_reclaim_subcommand(self):
        repo = Path(__file__).resolve().parent.parent
        path = str(repo / "examples" / "shared_block.json")
        proc = self.run_cli("reclaim", path, "--delete", "snap-1")
        self.assertEqual(proc.returncode, 0, proc.stderr)
        self.assertEqual(json.loads(proc.stdout)["reclaimed_bytes"], 0)
        proc = self.run_cli("reclaim", path, "--delete", "snap-1", "snap-2")
        self.assertEqual(json.loads(proc.stdout)["reclaimed_bytes"], 10)

    def test_stdin_input(self):
        doc = json.dumps({
            "blocks": {"b": 5},
            "snapshots": [{"id": "s", "loss": 1, "file_versions": []}],
        })
        # file_versions may not be empty; use a version instead
        doc = json.dumps({
            "blocks": {"b": 5},
            "file_versions": {"fv": ["b"]},
            "snapshots": [{"id": "s", "loss": 1, "file_versions": ["fv"]}],
        })
        proc = self.run_cli("solve", "-", "--required-bytes", "5",
                            stdin_text=doc)
        self.assertEqual(proc.returncode, 0, proc.stderr)
        self.assertEqual(json.loads(proc.stdout)["selected"], ["s"])

    def test_infeasible_exit_code(self):
        doc = json.dumps({
            "blocks": {"b": 5},
            "file_versions": {"fv": ["b"]},
            "snapshots": [{"id": "s", "loss": 1, "file_versions": ["fv"]}],
        })
        with tempfile.NamedTemporaryFile(
                "w", suffix=".json", delete=False) as tmp:
            tmp.write(doc)
            tmp_path = tmp.name
        proc = self.run_cli("solve", tmp_path, "--required-bytes", "6")
        self.assertEqual(proc.returncode, 1)
        self.assertFalse(json.loads(proc.stdout)["feasible"])


if __name__ == "__main__":
    unittest.main()
