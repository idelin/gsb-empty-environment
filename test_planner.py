"""Unit tests for planner.py. All graphs are synthetic."""

import heapq
import itertools
import json
import random
import subprocess
import sys
import tempfile
import unittest

from planner import PlanError, solve, validate_nodes


def make_nodes(specs):
    return validate_nodes([dict(s) for s in specs])


SHARED_PARENT = [
    {"id": "P", "cost": 5, "size": 2, "parents": []},
    {"id": "A", "cost": 1, "size": 1, "parents": ["P"]},
    {"id": "B", "cost": 1, "size": 1, "parents": ["P"]},
]

# A is evicted unavoidably when C is computed (size(A)+size(C) > limit),
# but A is needed again afterwards as a parent of E -> A must be recomputed.
FORCED_RECOMPUTE = [
    {"id": "A", "cost": 5, "size": 4, "parents": []},
    {"id": "B", "cost": 1, "size": 2, "parents": ["A"]},
    {"id": "C", "cost": 1, "size": 4, "parents": ["B"]},
    {"id": "D", "cost": 1, "size": 2, "parents": ["C"]},
    {"id": "E", "cost": 1, "size": 1, "parents": ["D", "A"]},
]

INFEASIBLE_PEAK = [
    {"id": "P1", "cost": 1, "size": 6, "parents": []},
    {"id": "P2", "cost": 1, "size": 6, "parents": []},
    {"id": "Q", "cost": 1, "size": 1, "parents": ["P1", "P2"]},
]


def reference_solve(nodes, outputs, memory_limit):
    """Independent reference: Dijkstra allowing ALL fitting kept subsets.

    Cross-checks that the maximal-kept-set restriction in the main solver
    does not lose optimality.
    """
    outputs = frozenset(outputs)
    sizes = {n: s["size"] for n, s in nodes.items()}
    needed = set()
    stack = list(outputs)
    while stack:
        x = stack.pop()
        if x in needed:
            continue
        needed.add(x)
        stack.extend(nodes[x]["parents"])
    for n in needed:
        peak = nodes[n]["size"] + sum(sizes[p] for p in nodes[n]["parents"])
        if peak > memory_limit:
            return "infeasible", None
    if sum(sizes[o] for o in outputs) > memory_limit:
        return "infeasible", None

    def all_kept_sets(base, must_keep):
        others = sorted(base - {must_keep})
        kept_sets = []
        for r in range(len(others) + 1):
            for combo in itertools.combinations(others, r):
                kept = frozenset(combo) | {must_keep}
                if sum(sizes[x] for x in kept) <= memory_limit:
                    kept_sets.append(kept)
        return kept_sets

    start = frozenset()
    dist = {start: 0}
    tie = itertools.count()
    pq = [(0, next(tie), start)]
    while pq:
        cost, _, state = heapq.heappop(pq)
        if cost > dist.get(state, float("inf")):
            continue
        if outputs <= state:
            return "optimal", cost
        for nid in needed - state:
            spec = nodes[nid]
            if not all(p in state for p in spec["parents"]):
                continue
            for kept in all_kept_sets(state | {nid}, nid):
                new_cost = cost + spec["cost"]
                if new_cost < dist.get(kept, float("inf")):
                    dist[kept] = new_cost
                    heapq.heappush(pq, (new_cost, next(tie), kept))
    return "infeasible", None


class AcceptanceTests(unittest.TestCase):
    def test_shared_parent_is_reused(self):
        """Two outputs sharing a parent: parent computed exactly once."""
        result = solve(make_nodes(SHARED_PARENT), ["A", "B"], 4)
        self.assertEqual(result["status"], "optimal")
        self.assertEqual(result["total_cost"], 7)  # 5 + 1 + 1
        computes = [s["compute"] for s in result["steps"]]
        self.assertEqual(computes.count("P"), 1)
        self.assertEqual(computes, ["P", "A", "B"])
        self.assertEqual(set(result["steps"][-1]["cached_after"]),
                         {"P", "A", "B"})

    def test_infeasible_when_node_plus_parents_exceed_limit(self):
        """Node plus resident parents over the limit is infeasible;
        parents must never be dropped implicitly."""
        result = solve(make_nodes(INFEASIBLE_PEAK), ["Q"], 10)
        self.assertEqual(result["status"], "infeasible")
        self.assertEqual(result["violations"],
                         [{"node": "Q", "peak_memory": 13}])

    def test_forced_recompute(self):
        """Tight memory forces A to be evicted and recomputed once."""
        result = solve(make_nodes(FORCED_RECOMPUTE), ["E"], 7)
        self.assertEqual(result["status"], "optimal")
        self.assertEqual(result["total_cost"], 14)
        computes = [s["compute"] for s in result["steps"]]
        self.assertEqual(computes.count("A"), 2)
        self.assertEqual(computes, ["A", "B", "C", "D", "A", "E"])

    def test_outputs_cannot_coexist(self):
        nodes = make_nodes([
            {"id": "X", "cost": 1, "size": 4, "parents": []},
            {"id": "Y", "cost": 1, "size": 4, "parents": []},
        ])
        result = solve(nodes, ["X", "Y"], 5)
        self.assertEqual(result["status"], "infeasible")
        self.assertEqual(result["outputs_size"], 8)


class PlanConsistencyTests(unittest.TestCase):
    """Every step's dependency evidence must hold against the plan itself."""

    def check_plan(self, nodes, outputs, limit):
        result = solve(nodes, outputs, limit)
        self.assertEqual(result["status"], "optimal")
        sizes = {n: s["size"] for n, s in nodes.items()}
        cached = set()
        total = 0
        for step in result["steps"]:
            for parent in step["parents"]:
                self.assertTrue(parent["resident"])
                self.assertIn(parent["id"], cached)
            self.assertLessEqual(step["peak_memory"], limit)
            total += step["cost"]
            cached -= set(step["evicted"])
            cached.add(step["compute"])
            self.assertEqual(cached, set(step["cached_after"]))
            self.assertEqual(step["memory_after"],
                             sum(sizes[x] for x in cached))
            self.assertLessEqual(step["memory_after"], limit)
        self.assertEqual(total, result["total_cost"])
        self.assertTrue(set(outputs) <= cached)
        return result

    def test_shared_parent_plan_consistency(self):
        self.check_plan(make_nodes(SHARED_PARENT), ["A", "B"], 4)

    def test_forced_recompute_plan_consistency(self):
        self.check_plan(make_nodes(FORCED_RECOMPUTE), ["E"], 7)


class BudgetInterruptTests(unittest.TestCase):
    def test_zero_budget_interrupts_with_lower_bound(self):
        result = solve(make_nodes(FORCED_RECOMPUTE), ["E"], 7,
                       expansion_budget=0)
        self.assertEqual(result["status"], "interrupted")
        self.assertEqual(result["expansions"], 0)
        self.assertEqual(result["lower_bound"], 0)
        self.assertIsNone(result["upper_bound"])
        self.assertNotIn("steps", result)

    def test_small_budget_bound_is_valid(self):
        nodes = make_nodes(FORCED_RECOMPUTE)
        optimal = solve(nodes, ["E"], 7)["total_cost"]
        result = solve(nodes, ["E"], 7, expansion_budget=2)
        self.assertEqual(result["status"], "interrupted")
        self.assertLessEqual(result["lower_bound"], optimal)
        if result["upper_bound"] is not None:
            self.assertGreaterEqual(result["upper_bound"], optimal)

    def test_large_budget_reaches_optimum(self):
        result = solve(make_nodes(FORCED_RECOMPUTE), ["E"], 7,
                       expansion_budget=100000)
        self.assertEqual(result["status"], "optimal")
        self.assertEqual(result["total_cost"], 14)


class ValidationTests(unittest.TestCase):
    def test_cycle_rejected(self):
        with self.assertRaises(PlanError):
            make_nodes([
                {"id": "A", "cost": 1, "size": 1, "parents": ["B"]},
                {"id": "B", "cost": 1, "size": 1, "parents": ["A"]},
            ])

    def test_unknown_parent_rejected(self):
        with self.assertRaises(PlanError):
            make_nodes([{"id": "A", "cost": 1, "size": 1, "parents": ["ZZ"]}])

    def test_duplicate_id_rejected(self):
        with self.assertRaises(PlanError):
            make_nodes([
                {"id": "A", "cost": 1, "size": 1, "parents": []},
                {"id": "A", "cost": 2, "size": 2, "parents": []},
            ])

    def test_negative_cost_rejected(self):
        with self.assertRaises(PlanError):
            make_nodes([{"id": "A", "cost": -1, "size": 1, "parents": []}])

    def test_unknown_output_rejected(self):
        with self.assertRaises(PlanError):
            solve(make_nodes(SHARED_PARENT), ["ZZ"], 4)


class CrossCheckTests(unittest.TestCase):
    """Random small DAGs: main solver must match the all-subsets reference."""

    def test_random_dags_match_reference(self):
        rng = random.Random(20261001)
        checked = 0
        for trial in range(60):
            count = rng.randint(2, 7)
            specs = []
            for i in range(count):
                parents = [f"n{j}" for j in range(i)
                           if rng.random() < 0.4]
                specs.append({
                    "id": f"n{i}",
                    "cost": rng.randint(0, 6),
                    "size": rng.randint(1, 4),
                    "parents": parents,
                })
            nodes = make_nodes(specs)
            outputs = rng.sample([s["id"] for s in specs],
                                 rng.randint(1, min(3, count)))
            limit = rng.randint(4, 12)
            main = solve(nodes, outputs, limit)
            ref_status, ref_cost = reference_solve(nodes, outputs, limit)
            self.assertEqual(main["status"], ref_status,
                             f"trial {trial}: {specs} {outputs} {limit}")
            if ref_status == "optimal":
                self.assertEqual(main["total_cost"], ref_cost,
                                 f"trial {trial}: {specs} {outputs} {limit}")
                checked += 1
        self.assertGreater(checked, 10)  # ensure the loop was meaningful


class CliTests(unittest.TestCase):
    def run_cli(self, problem, extra_args=()):
        with tempfile.NamedTemporaryFile(
                "w", suffix=".json", delete=False) as handle:
            json.dump(problem, handle)
            path = handle.name
        try:
            proc = subprocess.run(
                [sys.executable, "planner.py", path, *extra_args],
                capture_output=True, text=True)
            return proc
        finally:
            import os
            os.unlink(path)

    def test_cli_optimal(self):
        problem = {
            "memory_limit": 4,
            "nodes": SHARED_PARENT,
            "outputs": ["A", "B"],
        }
        proc = self.run_cli(problem)
        self.assertEqual(proc.returncode, 0, proc.stderr)
        result = json.loads(proc.stdout)
        self.assertEqual(result["status"], "optimal")
        self.assertEqual(result["total_cost"], 7)
        self.assertEqual(len(result["steps"]), 3)

    def test_cli_infeasible_exit_code(self):
        problem = {
            "memory_limit": 10,
            "nodes": INFEASIBLE_PEAK,
            "outputs": ["Q"],
        }
        proc = self.run_cli(problem)
        self.assertEqual(proc.returncode, 1)
        self.assertEqual(json.loads(proc.stdout)["status"], "infeasible")

    def test_cli_budget_flag(self):
        problem = {
            "memory_limit": 7,
            "nodes": FORCED_RECOMPUTE,
            "outputs": ["E"],
        }
        proc = self.run_cli(problem, extra_args=["--budget", "0"])
        self.assertEqual(proc.returncode, 1)
        result = json.loads(proc.stdout)
        self.assertEqual(result["status"], "interrupted")
        self.assertEqual(result["lower_bound"], 0)

    def test_cli_bad_input_exit_code(self):
        problem = {"memory_limit": 4, "nodes": [], "outputs": []}
        proc = self.run_cli(problem)
        self.assertEqual(proc.returncode, 2)
        self.assertEqual(json.loads(proc.stdout)["status"], "error")


if __name__ == "__main__":
    unittest.main()
