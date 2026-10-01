"""Unit tests for min_delete (budgeted exact minimum rule deletion).

All instances are synthetic.
"""
from __future__ import annotations

import itertools
import json
import os
import random
import subprocess
import sys
import tempfile
import unittest

import min_delete as md

BIG_BUDGET = 10**9


def rules(*specs):
    """specs: (id, src, dst, cost) tuples."""
    return [md.Rule(i, s, d, c) for i, s, d, c in specs]


def solve(rs, budget=BIG_BUDGET):
    return md.solve_instance(rs, budget)


def brute_force(rs):
    """Exhaustive subset enumeration with the same tie-breaking key."""
    n = len(rs)
    graph = md.Graph(rs)
    best = None
    for del_mask in range(1 << n):
        alive = ((1 << n) - 1) & ~del_mask
        if md.find_cycle(graph, alive) is not None:
            continue
        cost = sum(rs[i].cost for i in range(n) if (del_mask >> i) & 1)
        ids = tuple(sorted(rs[i].id for i in range(n) if (del_mask >> i) & 1))
        key = (cost, len(ids), ids)
        if best is None or key < best:
            best = key
    return best  # (cost, count, ids)


class AcceptanceTests(unittest.TestCase):
    def test_three_cycle_deletes_cheapest_edge_only(self):
        # Cycle A->B->C->A with edge costs 1, 2, 3: only the cost-1 edge
        # needs to be deleted.
        rs = rules(
            ("e1", "A", "B", 1),
            ("e2", "B", "C", 2),
            ("e3", "C", "A", 3),
        )
        res = solve(rs)
        self.assertTrue(res["optimal"])
        self.assertEqual(res["deleted"], ["e1"])
        self.assertEqual(res["deleted_cost"], 1)
        self.assertEqual(res["deleted_count"], 1)
        self.assertEqual(res["lower_bound"], 1)
        check = md.verify_solution(rs, res["deleted"], res["topological_order"])
        self.assertTrue(check["ok"], check["errors"])

    def test_shared_edge_not_double_counted(self):
        # Edge s (A->B) is shared by cycles A->B->C->A and A->B->D->A.
        # Per-cycle naive deletion would charge min(3,4,4) + min(3,4,4) = 6;
        # deleting the shared edge alone costs 3 and breaks both cycles.
        rs = rules(
            ("s", "A", "B", 3),
            ("c1", "B", "C", 4),
            ("c2", "C", "A", 4),
            ("d1", "B", "D", 4),
            ("d2", "D", "A", 4),
        )
        res = solve(rs)
        self.assertTrue(res["optimal"])
        self.assertEqual(res["deleted"], ["s"])
        self.assertEqual(res["deleted_cost"], 3)
        self.assertLess(res["deleted_cost"], 6)
        check = md.verify_solution(rs, res["deleted"], res["topological_order"])
        self.assertTrue(check["ok"], check["errors"])


class TieBreakTests(unittest.TestCase):
    def test_fewer_deletions_win_on_cost_tie(self):
        # Shared edge cost 4 breaks both cycles (1 deletion) vs deleting one
        # private edge per cycle (2 deletions, cost 2+2=4). Same cost: the
        # single deletion must win.
        rs = rules(
            ("s", "A", "B", 4),
            ("c1", "B", "C", 2),
            ("c2", "C", "A", 2),
            ("d1", "B", "D", 2),
            ("d2", "D", "A", 2),
        )
        res = solve(rs)
        self.assertTrue(res["optimal"])
        self.assertEqual(res["deleted_cost"], 4)
        self.assertEqual(res["deleted"], ["s"])

    def test_lexicographic_tie_break(self):
        # 2-cycle, both edges cost 1: delete the lexicographically smaller id.
        rs = rules(
            ("b", "A", "B", 1),
            ("a", "B", "A", 1),
        )
        res = solve(rs)
        self.assertTrue(res["optimal"])
        self.assertEqual(res["deleted"], ["a"])

    def test_lexicographic_tie_break_on_sets(self):
        # Two disjoint 2-cycles; cheapest solution deletes one edge per cycle.
        rs = rules(
            ("z", "A", "B", 1),
            ("y", "B", "A", 1),
            ("x", "C", "D", 1),
            ("w", "D", "C", 1),
        )
        res = solve(rs)
        self.assertTrue(res["optimal"])
        self.assertEqual(res["deleted"], ["w", "y"])


class SolverPropertyTests(unittest.TestCase):
    def test_acyclic_input_needs_no_deletion(self):
        rs = rules(
            ("r1", "A", "B", 5),
            ("r2", "B", "C", 5),
            ("r3", "A", "C", 5),
        )
        res = solve(rs)
        self.assertTrue(res["optimal"])
        self.assertEqual(res["deleted"], [])
        self.assertEqual(res["deleted_cost"], 0)
        self.assertEqual(res["topological_order"], ["A", "B", "C"])

    def test_self_loop_must_be_deleted(self):
        rs = rules(
            ("loop", "A", "A", 7),
            ("r1", "A", "B", 1),
        )
        res = solve(rs)
        self.assertTrue(res["optimal"])
        self.assertEqual(res["deleted"], ["loop"])

    def test_empty_instance(self):
        res = solve([])
        self.assertTrue(res["optimal"])
        self.assertEqual(res["deleted"], [])
        self.assertEqual(res["topological_order"], [])

    def test_parallel_edges(self):
        rs = rules(
            ("p1", "A", "B", 2),
            ("p2", "A", "B", 3),
            ("back", "B", "A", 1),
        )
        res = solve(rs)
        self.assertTrue(res["optimal"])
        self.assertEqual(res["deleted"], ["back"])
        self.assertEqual(res["deleted_cost"], 1)

    def test_matches_brute_force_on_random_instances(self):
        rng = random.Random(20261001)
        for case in range(40):
            n_nodes = rng.randint(2, 5)
            nodes = [f"n{i}" for i in range(n_nodes)]
            n_edges = rng.randint(0, 8)
            rs = [
                md.Rule(
                    f"e{i}",
                    rng.choice(nodes),
                    rng.choice(nodes),
                    rng.randint(1, 5),
                )
                for i in range(n_edges)
            ]
            res = solve(rs)
            self.assertTrue(res["optimal"])
            expected = brute_force(rs)
            got = (res["deleted_cost"], res["deleted_count"], tuple(res["deleted"]))
            self.assertEqual(got, expected, f"case {case}: {rs}")
            check = md.verify_solution(
                rs, res["deleted"], res["topological_order"]
            )
            self.assertTrue(check["ok"], check["errors"])


class BudgetTests(unittest.TestCase):
    def _dense_instance(self):
        rng = random.Random(7)
        nodes = [f"n{i}" for i in range(6)]
        rs = []
        k = 0
        for _ in range(14):
            a, b = rng.sample(nodes, 2)
            rs.append(md.Rule(f"e{k}", a, b, rng.randint(1, 9)))
            k += 1
        return rs

    def test_budget_exhaustion_returns_feasible_with_valid_lower_bound(self):
        rs = self._dense_instance()
        exact = solve(rs)  # proven optimum
        self.assertTrue(exact["optimal"])

        res = solve(rs, budget=0)
        self.assertFalse(res["optimal"])
        self.assertTrue(res["budget_exhausted"])
        # Feasible: independent verifier accepts it.
        check = md.verify_solution(rs, res["deleted"], res["topological_order"])
        self.assertTrue(check["ok"], check["errors"])
        # Lower bound is valid: lb <= optimum <= incumbent cost.
        self.assertLessEqual(res["lower_bound"], exact["deleted_cost"])
        self.assertLessEqual(exact["deleted_cost"], res["deleted_cost"])

    def test_larger_budget_converges_to_optimum(self):
        rs = self._dense_instance()
        exact = solve(rs)
        res = solve(rs, budget=100_000)
        self.assertTrue(res["optimal"])
        self.assertEqual(res["deleted_cost"], exact["deleted_cost"])
        self.assertEqual(res["deleted"], exact["deleted"])

    def test_zero_budget_still_reports_greedy_incumbent(self):
        rs = rules(
            ("e1", "A", "B", 1),
            ("e2", "B", "C", 2),
            ("e3", "C", "A", 3),
        )
        res = solve(rs, budget=0)
        self.assertTrue(res["budget_exhausted"])
        self.assertEqual(res["deleted"], ["e1"])  # greedy already optimal here
        self.assertLessEqual(res["lower_bound"], res["deleted_cost"])


class VerifierTests(unittest.TestCase):
    def setUp(self):
        self.rs = rules(
            ("e1", "A", "B", 1),
            ("e2", "B", "C", 2),
            ("e3", "C", "A", 3),
        )

    def test_accepts_valid_solution(self):
        res = solve(self.rs)
        check = md.verify_solution(self.rs, res["deleted"], res["topological_order"])
        self.assertTrue(check["ok"])
        self.assertEqual(check["cost"], 1)

    def test_rejects_remaining_cycle(self):
        check = md.verify_solution(self.rs, [], ["A", "B", "C"])
        self.assertFalse(check["ok"])

    def test_rejects_bad_order(self):
        check = md.verify_solution(self.rs, ["e1"], ["C", "B", "A"])
        self.assertFalse(check["ok"])

    def test_rejects_unknown_and_duplicate_deletions(self):
        check = md.verify_solution(self.rs, ["nope"], ["A", "B", "C"])
        self.assertFalse(check["ok"])
        check = md.verify_solution(self.rs, ["e1", "e1"], ["A", "B", "C"])
        self.assertFalse(check["ok"])

    def test_rejects_incomplete_node_coverage(self):
        check = md.verify_solution(self.rs, ["e1"], ["A", "B"])
        self.assertFalse(check["ok"])


class InputValidationTests(unittest.TestCase):
    def test_rejects_bad_inputs(self):
        bad = [
            {},
            {"rules": "x"},
            {"rules": [{"id": "a", "from": "A", "to": "B"}]},
            {"rules": [{"id": "a", "from": "A", "to": "B", "cost": 0}]},
            {"rules": [{"id": "a", "from": "A", "to": "B", "cost": -3}]},
            {"rules": [{"id": "a", "from": "A", "to": "B", "cost": 1.5}]},
            {"rules": [{"id": "a", "from": "A", "to": "B", "cost": True}]},
            {"rules": [
                {"id": "a", "from": "A", "to": "B", "cost": 1},
                {"id": "a", "from": "B", "to": "C", "cost": 1},
            ]},
        ]
        for doc in bad:
            with self.assertRaises(md.InputError, msg=doc):
                md.parse_rules(doc)


class CliTests(unittest.TestCase):
    def test_cli_solve_and_verify_roundtrip(self):
        instance = {
            "rules": [
                {"id": "e1", "from": "A", "to": "B", "cost": 1},
                {"id": "e2", "from": "B", "to": "C", "cost": 2},
                {"id": "e3", "from": "C", "to": "A", "cost": 3},
            ]
        }
        exe = sys.executable
        script = os.path.join(os.path.dirname(__file__), "min_delete.py")
        with tempfile.TemporaryDirectory() as tmp:
            inst = os.path.join(tmp, "in.json")
            out = os.path.join(tmp, "out.json")
            with open(inst, "w", encoding="utf-8") as fh:
                json.dump(instance, fh)
            proc = subprocess.run(
                [exe, script, inst, "--output", out],
                capture_output=True, text=True,
            )
            self.assertEqual(proc.returncode, 0, proc.stderr)
            result = json.loads(proc.stdout)
            self.assertTrue(result["optimal"])
            self.assertTrue(result["verified"])
            self.assertEqual(result["deleted"], ["e1"])
            proc = subprocess.run(
                [exe, script, inst, "--verify", out],
                capture_output=True, text=True,
            )
            self.assertEqual(proc.returncode, 0, proc.stderr)
            self.assertTrue(json.loads(proc.stdout)["verified"])

    def test_cli_rejects_malformed_input(self):
        exe = sys.executable
        script = os.path.join(os.path.dirname(__file__), "min_delete.py")
        with tempfile.TemporaryDirectory() as tmp:
            inst = os.path.join(tmp, "bad.json")
            with open(inst, "w", encoding="utf-8") as fh:
                fh.write('{"rules": [{"id": "x", "from": "A", "to": "B", "cost": -1}]}')
            proc = subprocess.run(
                [exe, script, inst], capture_output=True, text=True
            )
            self.assertEqual(proc.returncode, 2)
            self.assertIn("error:", proc.stderr)


if __name__ == "__main__":
    unittest.main()
