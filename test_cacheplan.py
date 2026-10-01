"""Unittest suite for cacheplan (Python 3.11, stdlib only)."""

import json
import os
import subprocess
import sys
import tempfile
import unittest

import cacheplan
from cacheplan import Graph, Node, parse_spec, solve, verify_plan


def make_graph(memory_limit, nodes, targets):
    return Graph(
        nodes={n.name: n for n in nodes},
        memory_limit=memory_limit,
        targets=tuple(targets),
    )


def compute_count(steps, name):
    return sum(1 for s in steps if s["action"] == "compute" and s["node"] == name)


class SharedParentReuseTest(unittest.TestCase):
    """Two outputs sharing a parent must reuse it when memory suffices."""

    def setUp(self):
        self.graph = make_graph(
            memory_limit=6,
            nodes=[
                Node("a", cost=4, size=2, parents=()),
                Node("b", cost=3, size=2, parents=("a",)),
                Node("c", cost=2, size=2, parents=("a",)),
            ],
            targets=["b", "c"],
        )

    def test_parent_computed_exactly_once(self):
        result = solve(self.graph)
        self.assertEqual(result["status"], "optimal")
        self.assertEqual(result["total_cost"], 4 + 3 + 2)
        self.assertEqual(compute_count(result["steps"], "a"), 1)

    def test_plan_verifies_and_never_exceeds_memory(self):
        result = solve(self.graph)
        outcome = verify_plan(self.graph, result["steps"])
        self.assertTrue(outcome["ok"], outcome["error"])
        self.assertEqual(outcome["total_cost"], result["total_cost"])
        for step in result["steps"]:
            self.assertLessEqual(step["memory_after"], self.graph.memory_limit)
            if step["action"] == "compute":
                self.assertTrue(step["parents_resident"])


class InfeasibilityTest(unittest.TestCase):
    """A node whose parents + own result exceed the limit is infeasible;
    the planner may not implicitly drop parents to make it fit."""

    def test_oversized_needed_node_is_infeasible(self):
        graph = make_graph(
            memory_limit=10,
            nodes=[
                Node("a", cost=1, size=6, parents=()),
                Node("b", cost=1, size=6, parents=("a",)),
            ],
            targets=["b"],
        )
        result = solve(graph)
        self.assertEqual(result["status"], "infeasible")
        self.assertEqual(result["steps"], [])
        self.assertTrue(any("'b'" in v for v in result["violations"]))

    def test_oversized_irrelevant_node_does_not_block(self):
        graph = make_graph(
            memory_limit=10,
            nodes=[
                Node("a", cost=1, size=6, parents=()),
                Node("b", cost=1, size=6, parents=("a",)),
            ],
            targets=["a"],
        )
        result = solve(graph)
        self.assertEqual(result["status"], "optimal")
        self.assertEqual(result["total_cost"], 1)

    def test_unreachable_goal_is_infeasible(self):
        # Final targets fit together, but no state can reach them.
        graph = make_graph(
            memory_limit=4,
            nodes=[
                Node("s", cost=1, size=2, parents=()),
                Node("x", cost=1, size=2, parents=("s",)),
                Node("y", cost=1, size=2, parents=("s",)),
                Node("t1", cost=1, size=1, parents=("x",)),
                Node("t2", cost=1, size=1, parents=("y",)),
            ],
            targets=["t1", "t2"],
        )
        result = solve(graph)
        self.assertEqual(result["status"], "infeasible")


class ForcedRecomputeTest(unittest.TestCase):
    """Tight memory forces eviction and recomputation of a shared source.

    Layout (cost, size): s(5,3) -> x(1,3) -> t1(1,2); s -> y(1,2) -> t2(1,2).
    Memory 7.  Computing t1 requires evicting s (s+x+t1 = 8 > 7), so s must
    be computed twice; the optimum is 5+1+1+5+1+1 = 14.
    """

    def setUp(self):
        self.graph = make_graph(
            memory_limit=7,
            nodes=[
                Node("s", cost=5, size=3, parents=()),
                Node("x", cost=1, size=3, parents=("s",)),
                Node("t1", cost=1, size=2, parents=("x",)),
                Node("y", cost=1, size=2, parents=("s",)),
                Node("t2", cost=1, size=2, parents=("y",)),
            ],
            targets=["t1", "t2"],
        )

    def test_optimal_cost_and_recompute(self):
        result = solve(self.graph)
        self.assertEqual(result["status"], "optimal")
        self.assertEqual(result["total_cost"], 14)
        self.assertEqual(compute_count(result["steps"], "s"), 2)

    def test_plan_verifies(self):
        result = solve(self.graph)
        outcome = verify_plan(self.graph, result["steps"])
        self.assertTrue(outcome["ok"], outcome["error"])
        self.assertEqual(outcome["total_cost"], 14)


class BudgetInterruptionTest(unittest.TestCase):
    """Budget interruption must preserve a certified lower bound."""

    def setUp(self):
        self.graph = make_graph(
            memory_limit=7,
            nodes=[
                Node("s", cost=5, size=3, parents=()),
                Node("x", cost=1, size=3, parents=("s",)),
                Node("t1", cost=1, size=2, parents=("x",)),
                Node("y", cost=1, size=2, parents=("s",)),
                Node("t2", cost=1, size=2, parents=("y",)),
            ],
            targets=["t1", "t2"],
        )
        self.optimal = solve(self.graph)["total_cost"]

    def test_zero_budget_interrupts_immediately(self):
        result = solve(self.graph, expansion_budget=0)
        self.assertEqual(result["status"], "interrupted")
        self.assertEqual(result["lower_bound"], 0)

    def test_small_budget_bound_is_valid(self):
        for budget in (1, 3, 7):
            result = solve(self.graph, expansion_budget=budget)
            if result["status"] == "interrupted":
                self.assertLessEqual(result["lower_bound"], self.optimal)
                self.assertGreaterEqual(result["lower_bound"], 0)
                self.assertLessEqual(result["expansions"], budget)
            else:
                self.assertEqual(result["status"], "optimal")
                self.assertEqual(result["total_cost"], self.optimal)

    def test_large_budget_recovers_optimum(self):
        result = solve(self.graph, expansion_budget=10**9)
        self.assertEqual(result["status"], "optimal")
        self.assertEqual(result["total_cost"], self.optimal)


class VerifierTest(unittest.TestCase):
    def setUp(self):
        self.graph = make_graph(
            memory_limit=4,
            nodes=[
                Node("a", cost=2, size=2, parents=()),
                Node("b", cost=3, size=2, parents=("a",)),
            ],
            targets=["b"],
        )

    def test_valid_plan_accepted(self):
        steps = [
            {"action": "compute", "node": "a", "memory_after": 2},
            {"action": "compute", "node": "b", "memory_after": 4},
        ]
        outcome = verify_plan(self.graph, steps)
        self.assertTrue(outcome["ok"], outcome["error"])
        self.assertEqual(outcome["total_cost"], 5)

    def test_missing_parent_rejected(self):
        steps = [{"action": "compute", "node": "b", "memory_after": 2}]
        outcome = verify_plan(self.graph, steps)
        self.assertFalse(outcome["ok"])
        self.assertIn("parents", outcome["error"])

    def test_memory_overflow_rejected(self):
        graph = make_graph(
            memory_limit=3,
            nodes=[
                Node("a", cost=1, size=2, parents=()),
                Node("b", cost=1, size=2, parents=()),
            ],
            targets=["b"],
        )
        steps = [
            {"action": "compute", "node": "a", "memory_after": 2},
            {"action": "compute", "node": "b", "memory_after": 4},
        ]
        outcome = verify_plan(graph, steps)
        self.assertFalse(outcome["ok"])
        self.assertIn("memory_limit", outcome["error"])

    def test_missing_target_rejected(self):
        steps = [{"action": "compute", "node": "a", "memory_after": 2}]
        outcome = verify_plan(self.graph, steps)
        self.assertFalse(outcome["ok"])
        self.assertIn("targets", outcome["error"])

    def test_wrong_declared_memory_rejected(self):
        steps = [
            {"action": "compute", "node": "a", "memory_after": 99},
            {"action": "compute", "node": "b", "memory_after": 4},
        ]
        outcome = verify_plan(self.graph, steps)
        self.assertFalse(outcome["ok"])


class SpecParsingTest(unittest.TestCase):
    def test_cycle_rejected(self):
        with self.assertRaises(cacheplan.SpecError):
            parse_spec({
                "memory_limit": 10,
                "nodes": [
                    {"id": "a", "cost": 1, "size": 1, "parents": ["b"]},
                    {"id": "b", "cost": 1, "size": 1, "parents": ["a"]},
                ],
                "targets": ["a"],
            })

    def test_unknown_parent_rejected(self):
        with self.assertRaises(cacheplan.SpecError):
            parse_spec({
                "memory_limit": 10,
                "nodes": [{"id": "a", "cost": 1, "size": 1, "parents": ["zz"]}],
                "targets": ["a"],
            })

    def test_negative_cost_rejected(self):
        with self.assertRaises(cacheplan.SpecError):
            parse_spec({
                "memory_limit": 10,
                "nodes": [{"id": "a", "cost": -1, "size": 1}],
                "targets": ["a"],
            })


class CliTest(unittest.TestCase):
    SPEC = {
        "memory_limit": 6,
        "nodes": [
            {"id": "a", "cost": 4, "size": 2, "parents": []},
            {"id": "b", "cost": 3, "size": 2, "parents": ["a"]},
            {"id": "c", "cost": 2, "size": 2, "parents": ["a"]},
        ],
        "targets": ["b", "c"],
    }

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.spec_path = os.path.join(self.tmp.name, "spec.json")
        with open(self.spec_path, "w", encoding="utf-8") as handle:
            json.dump(self.SPEC, handle)
        self.script = os.path.join(os.path.dirname(os.path.abspath(__file__)),
                                   "cacheplan.py")

    def tearDown(self):
        self.tmp.cleanup()

    def run_cli(self, *args):
        return subprocess.run(
            [sys.executable, self.script, *args],
            capture_output=True, text=True, check=False,
        )

    def test_solve_then_verify_roundtrip(self):
        proc = self.run_cli("solve", self.spec_path)
        self.assertEqual(proc.returncode, 0, proc.stderr)
        result = json.loads(proc.stdout)
        self.assertEqual(result["status"], "optimal")
        self.assertEqual(result["total_cost"], 9)

        plan_path = os.path.join(self.tmp.name, "plan.json")
        with open(plan_path, "w", encoding="utf-8") as handle:
            json.dump(result, handle)
        proc = self.run_cli("verify", self.spec_path, plan_path)
        self.assertEqual(proc.returncode, 0, proc.stderr)
        self.assertTrue(json.loads(proc.stdout)["ok"])

    def test_cli_budget_interruption(self):
        proc = self.run_cli("solve", self.spec_path, "--budget", "0")
        self.assertEqual(proc.returncode, 1)
        result = json.loads(proc.stdout)
        self.assertEqual(result["status"], "interrupted")
        self.assertIn("lower_bound", result)

    def test_cli_bad_spec_exit_code(self):
        bad_path = os.path.join(self.tmp.name, "bad.json")
        with open(bad_path, "w", encoding="utf-8") as handle:
            json.dump({"nodes": []}, handle)
        proc = self.run_cli("solve", bad_path)
        self.assertEqual(proc.returncode, 2)
        self.assertIn("memory_limit", proc.stderr)


if __name__ == "__main__":
    unittest.main()
