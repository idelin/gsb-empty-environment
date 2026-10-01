"""Unittests for the budgeted minimum rule deletion component.

All instance data is synthetic.
"""
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

from rule_prune.solver import (  # noqa: E402
    Rule,
    build_adjacency,
    find_cycle,
    parse_instance,
    solve,
    topological_order,
)
from rule_prune.verifier import verify_solution  # noqa: E402


def make_rules(spec):
    """spec: iterable of (id, cost, src, dst)."""
    return [Rule(rid, cost, src, dst) for rid, cost, src, dst in spec]


def brute_force_best(rules):
    """Exhaustive optimum under the (cost, count, lex) ordering."""
    cost_of = {r.id: r.cost for r in rules}
    best_key = None
    best_set = frozenset()
    ids = [r.id for r in rules]
    for k in range(len(ids) + 1):
        for combo in itertools.combinations(ids, k):
            deleted = frozenset(combo)
            if find_cycle(build_adjacency(rules, deleted)) is not None:
                continue
            cost = sum(cost_of[e] for e in deleted)
            key = (cost, len(deleted), sorted(deleted))
            if best_key is None or key < best_key:
                best_key = key
                best_set = deleted
    return best_key[0], best_set


class ThreeCycleTest(unittest.TestCase):
    """Acceptance boundary: a 3-cycle with edge costs 1, 2, 3 only needs
    the cost-1 edge deleted."""

    def setUp(self):
        self.rules = make_rules([
            ("e1", 1, "A", "B"),
            ("e2", 2, "B", "C"),
            ("e3", 3, "C", "A"),
        ])

    def test_deletes_only_cheapest_edge(self):
        result = solve(self.rules)
        self.assertEqual(result.status, "optimal")
        self.assertEqual(result.deleted, ["e1"])
        self.assertEqual(result.deleted_cost, 1)

    def test_topological_order_valid(self):
        result = solve(self.rules)
        self.assertEqual(result.topological_order, ["B", "C", "A"])

    def test_optimality_independently_proven(self):
        result = solve(self.rules)
        verdict = verify_solution(self.rules, result.to_dict())
        self.assertTrue(verdict["ok"])
        self.assertTrue(verdict["optimality_proven"])


class SharedEdgeTest(unittest.TestCase):
    """Acceptance boundary: an edge shared by two cycles must not be
    charged once per cycle.  Deleting the shared cheap edge e1 (cost 1)
    breaks both cycles; a naive per-cycle minimum sum would suggest 2."""

    def setUp(self):
        # Cycles: A->B->C->A and A->B->C->D->A share e1 (A->B) and e2.
        self.rules = make_rules([
            ("e1", 1, "A", "B"),
            ("e2", 5, "B", "C"),
            ("e3", 5, "C", "A"),
            ("e4", 5, "C", "D"),
            ("e5", 5, "D", "A"),
        ])

    def test_shared_edge_deleted_once(self):
        result = solve(self.rules)
        self.assertEqual(result.status, "optimal")
        self.assertEqual(result.deleted, ["e1"])
        self.assertEqual(result.deleted_cost, 1)

    def test_lower_bound_not_double_counted(self):
        result = solve(self.rules)
        # The packing bound counts the shared edge at most once.
        self.assertEqual(result.certified_lower_bound, 1)
        self.assertEqual(result.lower_bound, 1)

    def test_certificate_edge_disjoint(self):
        result = solve(self.rules)
        seen = set()
        for cycle in result.lower_bound_certificate:
            for eid in cycle:
                self.assertNotIn(eid, seen)
                seen.add(eid)


class AcyclicTest(unittest.TestCase):
    def test_no_deletion_needed(self):
        rules = make_rules([
            ("r1", 4, "A", "B"),
            ("r2", 7, "B", "C"),
            ("r3", 2, "A", "C"),
        ])
        result = solve(rules)
        self.assertEqual(result.status, "optimal")
        self.assertEqual(result.deleted, [])
        self.assertEqual(result.deleted_cost, 0)
        self.assertEqual(result.topological_order, ["A", "B", "C"])
        verdict = verify_solution(rules, result.to_dict())
        self.assertTrue(verdict["ok"])
        self.assertTrue(verdict["optimality_proven"])


class TieBreakTest(unittest.TestCase):
    def test_equal_cost_prefers_fewer_deletions(self):
        # Cycles {hub, a1} and {hub, a2} share the hub edge.  Deleting
        # "hub" alone (cost 2, one deletion) ties on cost with deleting
        # a1 and a2 (cost 1+1, two deletions); fewer deletions wins.
        rules = make_rules([
            ("hub", 2, "A", "B"),
            ("a1", 1, "B", "A"),
            ("a2", 1, "B", "A"),
        ])
        result = solve(rules)
        self.assertEqual(result.status, "optimal")
        self.assertEqual(result.deleted, ["hub"])
        self.assertEqual(result.deleted_cost, 2)

    def test_equal_cost_equal_count_prefers_lexicographic(self):
        rules = make_rules([
            ("b", 1, "A", "B"),
            ("a", 1, "B", "A"),
        ])
        result = solve(rules)
        self.assertEqual(result.deleted, ["a"])


class SelfLoopTest(unittest.TestCase):
    def test_self_loop_must_be_deleted(self):
        rules = make_rules([
            ("loop", 9, "A", "A"),
            ("ok", 1, "A", "B"),
        ])
        result = solve(rules)
        self.assertEqual(result.deleted, ["loop"])
        self.assertEqual(result.deleted_cost, 9)
        self.assertEqual(result.topological_order, ["A", "B"])


class BudgetExhaustionTest(unittest.TestCase):
    def setUp(self):
        # Several interacting cycles so the search tree branches.
        self.rules = make_rules([
            ("s", 2, "A", "B"),
            ("p", 3, "B", "C"),
            ("q", 3, "C", "A"),
            ("r", 3, "B", "D"),
            ("t", 3, "D", "A"),
            ("u", 1, "C", "D"),
        ])

    def test_zero_budget_returns_feasible_with_valid_bound(self):
        result = solve(self.rules, budget=0)
        self.assertEqual(result.status, "feasible")
        # A feasible incumbent is always available (greedy).
        self.assertGreaterEqual(len(result.deleted), 1)
        # The reported lower bound is valid.
        self.assertLessEqual(result.lower_bound, result.deleted_cost)
        self.assertGreaterEqual(
            result.lower_bound, result.certified_lower_bound
        )
        verdict = verify_solution(self.rules, result.to_dict())
        self.assertTrue(verdict["ok"])

    def test_large_budget_proves_optimum(self):
        result = solve(self.rules, budget=100_000)
        self.assertEqual(result.status, "optimal")
        self.assertEqual(result.lower_bound, result.deleted_cost)
        expected_cost, expected_set = brute_force_best(self.rules)
        self.assertEqual(result.deleted_cost, expected_cost)
        self.assertEqual(frozenset(result.deleted), expected_set)

    def test_time_limit_zero_returns_feasible(self):
        result = solve(self.rules, budget=100_000, time_limit=0.0)
        self.assertEqual(result.status, "feasible")
        self.assertLessEqual(result.lower_bound, result.deleted_cost)


class VerifierRejectionTest(unittest.TestCase):
    def setUp(self):
        self.rules = make_rules([
            ("e1", 1, "A", "B"),
            ("e2", 2, "B", "C"),
            ("e3", 3, "C", "A"),
        ])
        self.good = solve(self.rules).to_dict()

    def verdict(self, **mutations):
        sol = json.loads(json.dumps(self.good))
        sol.update(mutations)
        return verify_solution(self.rules, sol)

    def test_valid_solution_accepted(self):
        self.assertTrue(verify_solution(self.rules, self.good)["ok"])

    def test_rejects_wrong_cost(self):
        self.assertFalse(self.verdict(deleted_cost=0)["ok"])

    def test_rejects_remaining_cycle(self):
        self.assertFalse(self.verdict(deleted=[], deleted_cost=0)["ok"])

    def test_rejects_bad_topological_order(self):
        self.assertFalse(
            self.verdict(topological_order=["B", "A", "C"])["ok"]
        )

    def test_rejects_unknown_rule_id(self):
        self.assertFalse(
            self.verdict(deleted=["e1", "nope"], deleted_cost=1)["ok"]
        )

    def test_rejects_inflated_certificate(self):
        sol = json.loads(json.dumps(self.good))
        sol["certified_lower_bound"] = sol["deleted_cost"] + 1
        self.assertFalse(verify_solution(self.rules, sol)["ok"])

    def test_rejects_forged_certificate_cycle(self):
        sol = json.loads(json.dumps(self.good))
        sol["lower_bound_certificate"] = [["e1", "e2"]]  # not a cycle
        sol["certified_lower_bound"] = 1
        self.assertFalse(verify_solution(self.rules, sol)["ok"])


class BruteForceCrossCheckTest(unittest.TestCase):
    """Cross-check the exact solver against exhaustive enumeration on
    random synthetic graphs."""

    def test_random_small_graphs(self):
        rng = random.Random(20261001)
        for trial in range(60):
            n = rng.randint(2, 5)
            nodes = [f"n{i}" for i in range(n)]
            m = rng.randint(1, 8)
            spec = []
            for j in range(m):
                src = rng.choice(nodes)
                dst = rng.choice(nodes)
                cost = rng.randint(1, 5)
                spec.append((f"e{j}", cost, src, dst))
            rules = make_rules(spec)
            result = solve(rules, budget=1_000_000)
            self.assertEqual(
                result.status, "optimal", f"trial {trial}: {spec}"
            )
            expected_cost, expected_set = brute_force_best(rules)
            self.assertEqual(
                result.deleted_cost, expected_cost, f"trial {trial}: {spec}"
            )
            self.assertEqual(
                frozenset(result.deleted), expected_set,
                f"trial {trial}: {spec}",
            )
            verdict = verify_solution(rules, result.to_dict())
            self.assertTrue(verdict["ok"], f"trial {trial}: {spec}")

    def test_budgeted_bounds_are_valid(self):
        # With a tiny budget the search stops early; the reported lower
        # bound must still never exceed the true optimum.
        rng = random.Random(7)
        for trial in range(40):
            n = rng.randint(2, 4)
            nodes = [f"n{i}" for i in range(n)]
            m = rng.randint(2, 7)
            spec = [
                (f"e{j}", rng.randint(1, 5),
                 rng.choice(nodes), rng.choice(nodes))
                for j in range(m)
            ]
            rules = make_rules(spec)
            opt_cost, _ = brute_force_best(rules)
            result = solve(rules, budget=rng.randint(0, 3))
            # Tiny budgets usually stop early, but trivially solved
            # instances may still complete; both are acceptable.
            self.assertIn(result.status, ("feasible", "optimal"))
            self.assertLessEqual(
                result.lower_bound, opt_cost, f"trial {trial}: {spec}"
            )
            self.assertLessEqual(
                result.certified_lower_bound, opt_cost,
                f"trial {trial}: {spec}",
            )
            self.assertGreaterEqual(
                result.deleted_cost, opt_cost, f"trial {trial}: {spec}"
            )
            verdict = verify_solution(rules, result.to_dict())
            self.assertTrue(verdict["ok"], f"trial {trial}: {spec}")


class ParseInstanceTest(unittest.TestCase):
    def test_rejects_nonpositive_cost(self):
        with self.assertRaises(ValueError):
            parse_instance({"rules": [
                {"id": "x", "cost": 0, "src": "A", "dst": "B"},
            ]})

    def test_rejects_duplicate_ids(self):
        with self.assertRaises(ValueError):
            parse_instance({"rules": [
                {"id": "x", "cost": 1, "src": "A", "dst": "B"},
                {"id": "x", "cost": 2, "src": "B", "dst": "C"},
            ]})

    def test_rejects_bool_cost(self):
        with self.assertRaises(ValueError):
            parse_instance({"rules": [
                {"id": "x", "cost": True, "src": "A", "dst": "B"},
            ]})


class CliTest(unittest.TestCase):
    def setUp(self):
        self.repo = Path(__file__).resolve().parent.parent
        self.instance = {
            "rules": [
                {"id": "e1", "cost": 1, "src": "A", "dst": "B"},
                {"id": "e2", "cost": 2, "src": "B", "dst": "C"},
                {"id": "e3", "cost": 3, "src": "C", "dst": "A"},
            ]
        }
        self.tmp = tempfile.TemporaryDirectory()
        self.inst_path = Path(self.tmp.name) / "instance.json"
        self.inst_path.write_text(json.dumps(self.instance), encoding="utf-8")

    def tearDown(self):
        self.tmp.cleanup()

    def run_cli(self, *argv, stdin=None):
        return subprocess.run(
            [sys.executable, "-m", "rule_prune", *argv],
            cwd=self.repo,
            input=stdin,
            capture_output=True,
            text=True,
        )

    def test_solve_then_verify(self):
        proc = self.run_cli("solve", str(self.inst_path))
        self.assertEqual(proc.returncode, 0, proc.stderr)
        solution = json.loads(proc.stdout)
        self.assertEqual(solution["status"], "optimal")
        self.assertEqual(solution["deleted"], ["e1"])
        self.assertEqual(solution["deleted_cost"], 1)
        self.assertEqual(solution["topological_order"], ["B", "C", "A"])

        sol_path = Path(self.tmp.name) / "solution.json"
        sol_path.write_text(proc.stdout, encoding="utf-8")
        vproc = self.run_cli("verify", str(self.inst_path), str(sol_path))
        self.assertEqual(vproc.returncode, 0, vproc.stdout + vproc.stderr)
        verdict = json.loads(vproc.stdout)
        self.assertTrue(verdict["ok"])
        self.assertTrue(verdict["optimality_proven"])

    def test_solve_budget_exit_code_and_verify(self):
        proc = self.run_cli("solve", str(self.inst_path), "--budget", "0")
        self.assertEqual(proc.returncode, 2, proc.stderr)
        solution = json.loads(proc.stdout)
        self.assertEqual(solution["status"], "feasible")
        self.assertLessEqual(
            solution["lower_bound"], solution["deleted_cost"]
        )

        sol_path = Path(self.tmp.name) / "solution.json"
        sol_path.write_text(proc.stdout, encoding="utf-8")
        vproc = self.run_cli("verify", str(self.inst_path), str(sol_path))
        self.assertEqual(vproc.returncode, 0, vproc.stdout + vproc.stderr)

    def test_verify_rejects_tampered_solution(self):
        proc = self.run_cli("solve", str(self.inst_path))
        solution = json.loads(proc.stdout)
        solution["deleted_cost"] = 0
        sol_path = Path(self.tmp.name) / "bad.json"
        sol_path.write_text(json.dumps(solution), encoding="utf-8")
        vproc = self.run_cli("verify", str(self.inst_path), str(sol_path))
        self.assertEqual(vproc.returncode, 1)

    def test_solve_reads_stdin(self):
        proc = self.run_cli("solve", "-", stdin=json.dumps(self.instance))
        self.assertEqual(proc.returncode, 0, proc.stderr)
        self.assertEqual(json.loads(proc.stdout)["deleted"], ["e1"])


if __name__ == "__main__":
    unittest.main()
