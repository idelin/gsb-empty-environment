#!/usr/bin/env python3.11
"""Unittests for shelf_life_flow, including acceptance-boundary cases and a
brute-force cross-check that enumerates every per-unit path assignment."""
from __future__ import annotations

import itertools
import json
import os
import random
import subprocess
import sys
import tempfile
import unittest

import shelf_life_flow as slf

HERE = os.path.dirname(os.path.abspath(__file__))
CLI = os.path.join(HERE, "shelf_life_flow.py")
PYTHON = sys.executable


def make_problem(horizon, edges, batches):
    return slf.load_problem({
        "horizon": horizon,
        "edges": [dict(zip(("from", "to", "duration", "capacity", "cost"), e))
                  for e in edges],
        "batches": [dict(zip(("id", "origin", "destination", "quantity",
                              "depart", "expiry"), b)) for b in batches],
    })


def brute_force(problem):
    """Independent exact solver: enumerate every combination of one path per
    individual unit of every batch. Returns min feasible cost or None."""
    per_batch_paths = []
    for b in problem.batches:
        paths, truncated = slf.enumerate_paths(problem, b, max_paths=10 ** 7)
        assert not truncated, "test instances must stay small"
        per_batch_paths.append(paths)
    unit_options = []
    for b, paths in zip(problem.batches, per_batch_paths):
        if b.quantity > 0 and not paths:
            return None
        for _ in range(b.quantity):
            unit_options.append(paths)
    best = None
    for combo in itertools.product(*unit_options):
        usage = {}
        cost = 0
        ok = True
        for path in combo:
            cost += path.cost
            if best is not None and cost >= best:
                ok = False
                break
            for key in path.legs:
                used = usage.get(key, 0) + 1
                if used > problem.edges[key[0]].capacity:
                    ok = False
                    break
                usage[key] = used
            if not ok:
                break
        if ok and (best is None or cost < best):
            best = cost
    return best


class AcceptanceBoundaryTests(unittest.TestCase):
    def test_arrival_exactly_at_expiry_is_expired(self):
        # Batch ready at 0, expires at 3, direct edge of duration 3:
        # arrival at t=3 equals expiry -> expired -> infeasible.
        p = make_problem(5, [("A", "B", 3, 5, 1)],
                         [("b1", "A", "B", 1, 0, 3)])
        r = slf.solve(p)
        self.assertEqual(r["status"], "infeasible")
        self.assertIsNone(r["total_cost"])

    def test_two_legs_arriving_before_expiry_ok(self):
        # Two segments of duration 1 arrive at t=2 < expiry 3: usable.
        p = make_problem(5, [("A", "B", 1, 5, 2), ("B", "C", 1, 5, 3)],
                         [("b1", "A", "C", 1, 0, 3)])
        r = slf.solve(p)
        self.assertEqual(r["status"], "optimal")
        self.assertEqual(r["total_cost"], 5)
        legs = r["batches"][0]["paths"][0]["legs"]
        self.assertEqual(legs[-1]["arrive"], 2)
        self.assertTrue(r["checks"]["all"])

    def test_capacity_one_cannot_move_two_units_in_one_slot(self):
        # Only departure slot t=0 is usable (t=1 would arrive at expiry 2),
        # capacity 1 there -> quantity 2 is infeasible.
        p = make_problem(5, [("A", "B", 1, 1, 1)],
                         [("b1", "A", "B", 2, 0, 2)])
        r = slf.solve(p)
        self.assertEqual(r["status"], "infeasible")


class SemanticsTests(unittest.TestCase):
    def test_capacity_is_per_departure_slot(self):
        # Same edge, expiry 3: slots t=0 and t=1 both usable, 1 unit each.
        p = make_problem(5, [("A", "B", 1, 1, 4)],
                         [("b1", "A", "B", 2, 0, 3)])
        r = slf.solve(p)
        self.assertEqual(r["status"], "optimal")
        self.assertEqual(r["total_cost"], 8)
        departs = sorted(u["depart"] for u in r["edge_usage"])
        self.assertEqual(departs, [0, 1])

    def test_prefers_cheaper_longer_route(self):
        p = make_problem(5, [("A", "C", 1, 1, 10),
                             ("A", "B", 1, 1, 1),
                             ("B", "C", 1, 1, 1)],
                         [("b1", "A", "C", 1, 0, 3)])
        r = slf.solve(p)
        self.assertEqual(r["status"], "optimal")
        self.assertEqual(r["total_cost"], 2)
        route = [(l["from"], l["to"])
                 for l in r["batches"][0]["paths"][0]["legs"]]
        self.assertEqual(route, [("A", "B"), ("B", "C")])

    def test_waiting_for_capacity(self):
        # Two batches, one slot per time step: one must wait until t=1.
        p = make_problem(5, [("A", "B", 1, 1, 1)],
                         [("b1", "A", "B", 1, 0, 4),
                          ("b2", "A", "B", 1, 0, 4)])
        r = slf.solve(p)
        self.assertEqual(r["status"], "optimal")
        self.assertEqual(r["total_cost"], 2)
        departs = sorted(u["depart"] for u in r["edge_usage"])
        self.assertEqual(departs, [0, 1])

    def test_batches_keep_own_expiry(self):
        # Capacity 1 per slot. b1 (expiry 2) must take slot t=0;
        # b2 (expiry 3) can wait. Feasible only if expiries are not mixed.
        p = make_problem(5, [("A", "B", 1, 1, 1)],
                         [("b1", "A", "B", 1, 0, 2),
                          ("b2", "A", "B", 1, 0, 3)])
        r = slf.solve(p)
        self.assertEqual(r["status"], "optimal")
        b1_leg = r["batches"][0]["paths"][0]["legs"][0]
        b2_leg = r["batches"][1]["paths"][0]["legs"][0]
        self.assertEqual(b1_leg["depart"], 0)
        self.assertEqual(b2_leg["depart"], 1)

    def test_two_early_batches_infeasible(self):
        p = make_problem(5, [("A", "B", 1, 1, 1)],
                         [("b1", "A", "B", 1, 0, 2),
                          ("b2", "A", "B", 1, 0, 2)])
        r = slf.solve(p)
        self.assertEqual(r["status"], "infeasible")

    def test_origin_equals_destination(self):
        p = make_problem(5, [("A", "B", 1, 1, 1)],
                         [("b1", "A", "A", 3, 0, 2)])
        r = slf.solve(p)
        self.assertEqual(r["status"], "optimal")
        self.assertEqual(r["total_cost"], 0)

    def test_zero_quantity_batch(self):
        p = make_problem(5, [("A", "B", 1, 1, 1)],
                         [("b1", "A", "B", 0, 0, 2)])
        r = slf.solve(p)
        self.assertEqual(r["status"], "optimal")
        self.assertEqual(r["total_cost"], 0)

    def test_conservation_and_usage_checks(self):
        p = make_problem(6, [("A", "B", 1, 2, 2), ("B", "C", 2, 2, 3),
                             ("A", "C", 4, 1, 9)],
                         [("b1", "A", "C", 2, 0, 4),
                          ("b2", "B", "C", 1, 1, 5)])
        r = slf.solve(p)
        self.assertEqual(r["status"], "optimal")
        self.assertTrue(r["checks"]["all"])
        for u in r["edge_usage"]:
            self.assertLessEqual(u["used"], u["capacity"])

    def test_validation_rejects_bad_input(self):
        with self.assertRaises(slf.ProblemError):
            make_problem(5, [("A", "B", 0, 1, 1)], [])  # duration 0
        with self.assertRaises(slf.ProblemError):
            make_problem(5, [("A", "B", 1, 1, -1)], [])  # negative cost


class BruteForceCrossCheck(unittest.TestCase):
    def test_random_small_instances_match_brute_force(self):
        rng = random.Random(20261001)
        for trial in range(60):
            nodes = ["A", "B", "C"]
            edges = []
            for _ in range(rng.randint(1, 4)):
                u, v = rng.sample(nodes, 2)
                edges.append((u, v, rng.randint(1, 2), rng.randint(0, 2),
                              rng.randint(0, 5)))
            batches = []
            for i in range(rng.randint(1, 2)):
                u, v = rng.sample(nodes, 2)
                depart = rng.randint(0, 2)
                expiry = depart + rng.randint(1, 4)
                batches.append((f"b{i}", u, v, rng.randint(0, 2),
                                depart, expiry))
            p = make_problem(4, edges, batches)
            r = slf.solve(p)
            expected = brute_force(p)
            if expected is None:
                self.assertEqual(r["status"], "infeasible",
                                 f"trial {trial}: solver={r}")
            else:
                self.assertEqual(r["status"], "optimal",
                                 f"trial {trial}: solver={r}")
                self.assertEqual(r["total_cost"], expected,
                                 f"trial {trial}: solver={r} brute={expected}")
                self.assertTrue(r["checks"]["all"])


class CliTests(unittest.TestCase):
    def test_cli_end_to_end(self):
        problem = {
            "horizon": 5,
            "edges": [{"from": "A", "to": "B", "duration": 1, "capacity": 1,
                       "cost": 2},
                      {"from": "B", "to": "C", "duration": 1, "capacity": 1,
                       "cost": 3}],
            "batches": [{"id": "b1", "origin": "A", "destination": "C",
                         "quantity": 1, "depart": 0, "expiry": 3}],
        }
        with tempfile.NamedTemporaryFile("w", suffix=".json",
                                         delete=False) as fh:
            json.dump(problem, fh)
            path = fh.name
        try:
            proc = subprocess.run([PYTHON, CLI, path],
                                  capture_output=True, text=True, timeout=60)
        finally:
            os.unlink(path)
        self.assertEqual(proc.returncode, 0, proc.stderr)
        out = json.loads(proc.stdout)
        self.assertEqual(out["status"], "optimal")
        self.assertEqual(out["total_cost"], 5)
        self.assertTrue(out["checks"]["all"])

    def test_cli_stdin_and_example(self):
        proc = subprocess.run([PYTHON, CLI, "--example"],
                              capture_output=True, text=True, timeout=60)
        self.assertEqual(proc.returncode, 0, proc.stderr)
        example = proc.stdout
        proc = subprocess.run([PYTHON, CLI], input=example,
                              capture_output=True, text=True, timeout=60)
        self.assertEqual(proc.returncode, 0, proc.stderr)
        out = json.loads(proc.stdout)
        # Direct edge A->C has duration 3 = expiry window, so it is unusable;
        # the two 1-hour legs via B (cost 2+3) are the only option.
        self.assertEqual(out["status"], "optimal")
        self.assertEqual(out["total_cost"], 5)

    def test_cli_bad_input_exit_code(self):
        proc = subprocess.run([PYTHON, CLI], input="{not json",
                              capture_output=True, text=True, timeout=60)
        self.assertEqual(proc.returncode, 2)


if __name__ == "__main__":
    unittest.main()
