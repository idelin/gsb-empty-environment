"""Test suite: planner / verifier / brute-force cross-checks.

Run with:  python3.11 run_tests.py
"""
from __future__ import annotations

import math
import unittest

from bruteforce import BruteForce
from mealy import Model
from planner import Planner, policy_depth
from verifier import verify

S = "samples/"


def load(name):
    return Model.from_json_file(S + name + ".json")


def plan(model, budget=100_000):
    return Planner(model, budget=budget).plan()


def goal():
    return {"kind": "goal"}


def inp(sym, branches):
    return {"kind": "action", "action": {"type": "input", "symbol": sym},
            "branches": branches}


def rst(child):
    return {"kind": "action", "action": {"type": "reset"},
            "branches": {"": child}}


def doc(root, cost):
    return {"format": "mealy-policy-v1", "model_name": "t",
            "claimed_worst_cost": cost, "root": root}


class TestOptimalValues(unittest.TestCase):
    def test_identify_then_act(self):
        res = plan(load("identify_then_act"))
        self.assertEqual(res["status"], "optimal")
        self.assertEqual(res["cost"], 4)

    def test_same_output_different_edges(self):
        res = plan(load("same_output_different_edges"))
        self.assertEqual(res["status"], "optimal")
        self.assertEqual(res["cost"], 7)

    def test_partial_reset(self):
        res = plan(load("partial_reset"))
        self.assertEqual(res["status"], "optimal")
        self.assertEqual(res["cost"], 9)


class TestBruteForceCrossCheck(unittest.TestCase):
    """The independent depth-bounded exhaustive search must agree with the
    planner on the optimal value, and must not beat it with less depth."""

    def check(self, name):
        model = load(name)
        res = plan(model)
        self.assertEqual(res["status"], "optimal")
        cost = res["cost"]
        depth = policy_depth(res["policy"]["root"])
        bf = BruteForce(model, depth)
        self.assertEqual(bf.solve(bf.initial_node(), depth), cost)
        bf2 = BruteForce(model, depth - 1)
        self.assertGreaterEqual(bf2.solve(bf2.initial_node(), depth - 1), cost)

    def test_identify(self):
        self.check("identify_then_act")

    def test_same_output(self):
        self.check("same_output_different_edges")

    def test_partial_reset(self):
        self.check("partial_reset")


class TestVerifierAccepts(unittest.TestCase):
    def check(self, name):
        model = load(name)
        res = plan(model)
        rep = verify(model, res["policy"])
        self.assertTrue(rep["ok"], rep["errors"])
        self.assertEqual(rep["worst_cost"], res["cost"])

    def test_identify(self):
        self.check("identify_then_act")

    def test_same_output(self):
        self.check("same_output_different_edges")

    def test_partial_reset(self):
        self.check("partial_reset")


class TestCounterExamples(unittest.TestCase):
    def test_union_coverage_cheat_rejected(self):
        """b,a,b on same_output_different_edges: the union of per-trajectory
        coverage looks like 3 edges, but the definite (intersection)
        coverage is only {(s1,a),(s1,b)}; (s0,a),(s0,b) are missing."""
        model = load("same_output_different_edges")
        bad = doc(inp("b", {"1": inp("a", {"0": inp("b", {"1": goal()})})}), 3)
        rep = verify(model, bad)
        self.assertFalse(rep["ok"])
        self.assertTrue(any("definite coverage" in e for e in rep["errors"]))

    def test_illegal_reset_rejected(self):
        """reset at the initial belief of partial_reset is illegal because
        the actual state may be s1, where reset is not allowed."""
        model = load("partial_reset")
        bad = doc(rst(goal()), 2)
        rep = verify(model, bad)
        self.assertFalse(rep["ok"])
        self.assertTrue(any("reset is not allowed" in e for e in rep["errors"]))

    def test_unsafe_input_rejected(self):
        """y at the initial belief of identify_then_act is undefined at p1."""
        model = load("identify_then_act")
        bad = doc(inp("y", {"0": goal()}), 1)
        rep = verify(model, bad)
        self.assertFalse(rep["ok"])
        self.assertTrue(any("not defined" in e for e in rep["errors"]))

    def test_missing_output_branch_rejected(self):
        """Policy that only handles output 0 of the probe x; the trajectory
        from p1 produces output 1 and has nowhere to go."""
        model = load("identify_then_act")
        bad = doc(inp("x", {"0": inp("y", {"0": goal()})}), 2)
        rep = verify(model, bad)
        self.assertFalse(rep["ok"])
        self.assertTrue(any("no such branch" in e for e in rep["errors"]))

    def test_wrong_claimed_cost_rejected(self):
        model = load("same_output_different_edges")
        res = plan(model)
        pol = res["policy"]
        pol["claimed_worst_cost"] = res["cost"] - 1
        rep = verify(model, pol)
        self.assertFalse(rep["ok"])
        self.assertTrue(any("claimed worst cost" in e for e in rep["errors"]))

    def test_goal_with_branches_rejected(self):
        model = load("identify_then_act")
        bad = doc({"kind": "goal", "branches": {"0": goal()}}, 0)
        rep = verify(model, bad)
        self.assertFalse(rep["ok"])


class TestSemanticsInternals(unittest.TestCase):
    def test_no_edge_merging_across_hidden_states(self):
        """After input b on the initial belief of same_output_different_edges
        the union of covered edges is {(s0,b),(s1,b)} but the definite
        coverage (intersection) must be empty."""
        model = load("same_output_different_edges")
        pl = Planner(model)
        node = pl.initial_node()
        branches, _ = pl.apply(node, ("input", "b"))
        child = branches["1"]
        union = set().union(*(cov for _s, cov in child))
        self.assertEqual(union, {("s0", "b"), ("s1", "b")})
        self.assertEqual(pl.definite_covered(child), frozenset())

    def test_adaptive_policy_shape(self):
        """identify_then_act must probe x and branch on its output."""
        res = plan(load("identify_then_act"))
        root = res["policy"]["root"]
        self.assertEqual(root["action"], {"type": "input", "symbol": "x"})
        self.assertEqual(set(root["branches"]), {"0", "1"})
        # the two branches continue differently (state-dependent choice)
        a0 = root["branches"]["0"]["action"]
        a1 = root["branches"]["1"]["action"]
        self.assertNotEqual(a0, a1)


class TestUnsolvable(unittest.TestCase):
    def test_sink_lost_coverage(self):
        res = plan(load("sink_lost_coverage"))
        self.assertEqual(res["status"], "unsolvable")
        loops = res["explanation"]["loops_without_progress"]
        self.assertTrue(loops)
        lost = {tuple(t) for e in loops for t in e["uncovered_definite"]}
        self.assertIn(("s0", "a"), lost)
        self.assertIn(("s0", "b"), lost)

    def test_precheck_unreachable_transition(self):
        """q1 is unreachable from q0 and vice versa; the trajectory from q0
        can never execute (q1,a) -> immediate unsolvable with explanation."""
        model = Model.from_dict({
            "name": "disconnected",
            "states": ["q0", "q1"],
            "inputs": ["a"],
            "outputs": ["0"],
            "transitions": {"q0": {"a": ["q0", "0"]},
                            "q1": {"a": ["q1", "0"]}},
            "initial_candidates": ["q0", "q1"],
            "input_costs": {"a": 1},
        })
        res = plan(model)
        self.assertEqual(res["status"], "unsolvable")
        pre = res["explanation"]["precheck"]
        self.assertTrue(any(p["transition"] == ["q1", "a"]
                            and p["initial_state"] == "q0" for p in pre))
        self.assertTrue(any(p["transition"] == ["q0", "a"]
                            and p["initial_state"] == "q1" for p in pre))


class TestBudget(unittest.TestCase):
    def test_budget_exhaustion_is_unknown_not_unsolvable(self):
        model = load("same_output_different_edges")
        res = plan(model, budget=3)
        self.assertEqual(res["status"], "unknown")
        self.assertIn("NOT a proof of unsolvability", res["reason"])
        # valid bounds: lb <= optimum <= ub (ub may be absent)
        self.assertLessEqual(res["lower_bound"], 7)
        if res["upper_bound"] is not None:
            self.assertGreaterEqual(res["upper_bound"], 7)

    def test_tight_budget_still_finds_optimum(self):
        model = load("identify_then_act")
        res = plan(model, budget=11)  # exactly the expansion count needed
        self.assertEqual(res["status"], "optimal")
        self.assertEqual(res["cost"], 4)


if __name__ == "__main__":
    unittest.main(verbosity=2)
